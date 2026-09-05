package kube

import (
	"context"
	"strconv"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

func nsObjs(names ...string) []runtime.Object {
	objs := make([]runtime.Object, len(names))
	for i, n := range names {
		objs[i] = &corev1.Namespace{Name: n}
	}
	return objs
}

func TestIsPattern(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want bool
	}{
		{"be-znof", false},
		{"", false},
		{"be-*", true},
		{"be-?nof", true},
		{"be-[ab]", true},
	} {
		if got := IsPattern(tc.in); got != tc.want {
			t.Errorf("IsPattern(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

func TestResolveScopeLiteral(t *testing.T) {
	c := fake.NewClientset(nsObjs("be-znof", "be-other")...)
	f := Flags{Namespace: "be-znof"}
	if err := ResolveScope(context.Background(), c, &f); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got, _ := f.Scope().One(); got != "be-znof" {
		t.Fatalf("want scope be-znof, got %v", f.Scope().Names())
	}
}

// TestResolveScopeRejectsUnknownNamespace is the whole point of resolving: a
// typo used to print an empty table, which reads as "nothing to report".
func TestResolveScopeRejectsUnknownNamespace(t *testing.T) {
	c := fake.NewClientset(nsObjs("be-znof")...)
	f := Flags{Namespace: "be-znoff"}
	err := ResolveScope(context.Background(), c, &f)
	if err == nil {
		t.Fatal("want an error for an unknown namespace")
	}
	if !strings.Contains(err.Error(), `namespace "be-znoff" not found`) {
		t.Fatalf("want a not-found message naming the namespace, got %q", err)
	}
}

// TestResolveScopeSurfacesForbidden checks a 403 is not reported as "not found":
// the namespace may well exist, and telling the user to fix a typo would send
// them after the wrong problem.
func TestResolveScopeSurfacesForbidden(t *testing.T) {
	c := fake.NewClientset()
	c.PrependReactor("get", "namespaces", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewForbidden(schema.GroupResource{Resource: "namespaces"}, "be-znof", nil)
	})
	f := Flags{Namespace: "be-znof"}
	err := ResolveScope(context.Background(), c, &f)
	if err == nil || strings.Contains(err.Error(), "not found") {
		t.Fatalf("want a forbidden error distinct from not-found, got %v", err)
	}
	if !strings.Contains(err.Error(), "cannot verify") {
		t.Fatalf("want a cannot-verify message, got %q", err)
	}
}

func TestResolveScopeExpandsGlob(t *testing.T) {
	c := fake.NewClientset(nsObjs("be-znof", "be-alpha", "fe-web", "kube-system")...)
	f := Flags{Namespace: "be-*"}
	if err := ResolveScope(context.Background(), c, &f); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	want := []string{"be-alpha", "be-znof"}
	if got := f.Scope().Names(); len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("want %v (sorted), got %v", want, got)
	}
	if _, ok := f.Scope().One(); ok {
		t.Fatal("a multi-namespace scope must not answer One")
	}
}

// TestResolveScopeGlobMatchingNothing: an empty match is an error for the same
// reason a typo'd literal is - both would otherwise print a reassuring empty
// table.
func TestResolveScopeGlobMatchingNothing(t *testing.T) {
	c := fake.NewClientset(nsObjs("fe-web")...)
	f := Flags{Namespace: "be-*"}
	err := ResolveScope(context.Background(), c, &f)
	if err == nil || !strings.Contains(err.Error(), `no namespace matches "be-*"`) {
		t.Fatalf("want a no-match error, got %v", err)
	}
}

func TestResolveScopeRejectsBadPattern(t *testing.T) {
	c := fake.NewClientset(nsObjs("be-znof")...)
	f := Flags{Namespace: "be-[a"}
	err := ResolveScope(context.Background(), c, &f)
	if err == nil || !strings.Contains(err.Error(), "invalid namespace pattern") {
		t.Fatalf("want an invalid-pattern error, got %v", err)
	}
}

// TestResolveScopeSkipsAllNamespaces: -A means every namespace, so there is
// nothing to validate and no reason to spend a request on it.
func TestResolveScopeSkipsAllNamespaces(t *testing.T) {
	c := fake.NewClientset()
	f := Flags{AllNamespaces: true, Namespace: "ignored"}
	if err := ResolveScope(context.Background(), c, &f); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if len(c.Actions()) != 0 {
		t.Fatalf("want no api call for -A, got %v", c.Actions())
	}
	if !f.Scope().All() {
		t.Fatal("-A must scope to all namespaces")
	}
}

// TestListScopedFansOut checks a multi-namespace scope issues one targeted List
// per namespace rather than a cluster-wide one.
func TestListScopedFansOut(t *testing.T) {
	objs := nsObjs()
	for _, ns := range []string{"be-a", "be-b", "fe-c"} {
		objs = append(objs, &corev1.Pod{Name: "p", Namespace: ns})
	}
	c := fake.NewClientset(objs...)
	pods, err := ListPods(context.Background(), c, NamespaceSet("be-a", "be-b"), metav1.ListOptions{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(pods) != 2 {
		t.Fatalf("want 2 pods from the two matched namespaces, got %d", len(pods))
	}
	var listed []string
	for _, a := range c.Actions() {
		if a.GetVerb() == "list" && a.GetResource().Resource == "pods" {
			listed = append(listed, a.GetNamespace())
		}
	}
	if len(listed) != 2 || listed[0] == "" || listed[1] == "" {
		t.Fatalf("want two namespace-scoped lists, got %v", listed)
	}
}

// TestListScopedFallsBackPastFanout: past the cap the targeted lists cost more
// round trips than the bytes they save, so it becomes one cluster-wide list
// filtered locally - and the filter must still drop the unmatched namespaces.
func TestListScopedFallsBackPastFanout(t *testing.T) {
	var objs []runtime.Object
	var want []string
	for i := range MaxNamespaceFanout + 1 {
		ns := "be-" + strconv.Itoa(i)
		want = append(want, ns)
		objs = append(objs, &corev1.Pod{Name: "p", Namespace: ns})
	}
	objs = append(objs, &corev1.Pod{Name: "p", Namespace: "fe-out"})
	c := fake.NewClientset(objs...)
	pods, err := ListPods(context.Background(), c, NamespaceSet(want...), metav1.ListOptions{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(pods) != len(want) {
		t.Fatalf("want %d pods, got %d", len(want), len(pods))
	}
	for i := range pods {
		if pods[i].Namespace == "fe-out" {
			t.Fatal("an unmatched namespace leaked through the wide list")
		}
	}
	var lists int
	for _, a := range c.Actions() {
		if a.GetVerb() == "list" && a.GetResource().Resource == "pods" {
			lists++
			if a.GetNamespace() != "" {
				t.Fatalf("want a cluster-wide list past the cap, got one scoped to %q", a.GetNamespace())
			}
		}
	}
	if lists != 1 {
		t.Fatalf("want exactly one list past the cap, got %d", lists)
	}
}

// TestResolveScopeHintsAtRegexp: -n takes a glob, so "." is a literal and a
// regexp silently matches nothing. The error has to say so, or the user is left
// staring at a pattern that looks right.
func TestResolveScopeHintsAtRegexp(t *testing.T) {
	c := fake.NewClientset(nsObjs("be-znof")...)
	for _, tc := range []struct{ pattern, want string }{
		{"be.*", `did you mean "be*"?`},
		{"^be.*$", `did you mean "be*"?`},
		{"zz.+", `did you mean "zz?*"?`},
	} {
		f := Flags{Namespace: tc.pattern}
		err := ResolveScope(context.Background(), c, &f)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: want a hint containing %q, got %v", tc.pattern, tc.want, err)
		}
		if err == nil || !strings.Contains(err.Error(), "not a regexp") {
			t.Errorf("%s: want the glob-vs-regexp explanation, got %v", tc.pattern, err)
		}
	}
	// A plain glob that matches nothing gets no hint: there is nothing to fix
	// about its syntax.
	f := Flags{Namespace: "fe-*"}
	err := ResolveScope(context.Background(), c, &f)
	if err == nil || strings.Contains(err.Error(), "did you mean") {
		t.Errorf("want no hint for a well-formed glob, got %v", err)
	}
}
