package view

import (
	"bytes"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"

	"github.com/PixiBixi/kubectl-klens/internal/kube"
)

func TestNodeStatus(t *testing.T) {
	node := func(conds ...corev1.NodeCondition) *corev1.Node {
		return &corev1.Node{Status: corev1.NodeStatus{Conditions: conds}}
	}
	ready := func(s corev1.ConditionStatus) corev1.NodeCondition {
		return corev1.NodeCondition{Type: corev1.NodeReady, Status: s}
	}
	tests := []struct {
		name string
		n    *corev1.Node
		want string
	}{
		{"ready", node(ready(corev1.ConditionTrue)), "Ready"},
		{"kubelet says not ready", node(ready(corev1.ConditionFalse)), "NotReady"},
		// The distinction that matters operationally: Unknown means the kubelet
		// stopped reporting, which is what starts the eviction clock.
		{"kubelet stopped reporting", node(ready(corev1.ConditionUnknown)), "Unknown"},
		{"no ready condition", node(), "Unknown"},
		{"ready among others", node(
			corev1.NodeCondition{Type: corev1.NodeMemoryPressure, Status: corev1.ConditionFalse},
			ready(corev1.ConditionTrue),
		), "Ready"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := nodeStatus(tc.n); got != tc.want {
				t.Fatalf("nodeStatus = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestPodContainersOrderAndKinds(t *testing.T) {
	p := &corev1.Pod{Spec: corev1.PodSpec{
		InitContainers: []corev1.Container{{Name: "migrate"}, {Name: "wait-db"}},
		Containers:     []corev1.Container{{Name: "api"}},
		EphemeralContainers: []corev1.EphemeralContainer{
			{Name: "debugger"},
		},
	}}
	got := podContainers(p)
	want := []podContainer{
		{Kind: kindInit}, {Kind: kindInit}, {Kind: kindApp}, {Kind: kindEph},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d containers, want %d", len(got), len(want))
	}
	wantNames := []string{"migrate", "wait-db", "api", "debugger"}
	for i := range want {
		if got[i].Kind != want[i].Kind {
			t.Errorf("container %d kind = %q, want %q", i, got[i].Kind, want[i].Kind)
		}
		if got[i].Spec.Name != wantNames[i] {
			t.Errorf("container %d name = %q, want %q", i, got[i].Spec.Name, wantNames[i])
		}
	}
}

func TestPodContainerStatusesOrderAndKinds(t *testing.T) {
	p := &corev1.Pod{Status: corev1.PodStatus{
		InitContainerStatuses:      []corev1.ContainerStatus{{Name: "migrate"}},
		ContainerStatuses:          []corev1.ContainerStatus{{Name: "api"}},
		EphemeralContainerStatuses: []corev1.ContainerStatus{{Name: "debugger"}},
	}}
	got := podContainerStatuses(p)
	wantKinds := []string{kindInit, kindApp, kindEph}
	wantNames := []string{"migrate", "api", "debugger"}
	if len(got) != len(wantKinds) {
		t.Fatalf("got %d statuses, want %d", len(got), len(wantKinds))
	}
	for i := range wantKinds {
		if got[i].Kind != wantKinds[i] || got[i].Status.Name != wantNames[i] {
			t.Errorf("status %d = (%q, %q), want (%q, %q)",
				i, got[i].Status.Name, got[i].Kind, wantNames[i], wantKinds[i])
		}
	}
}

// TestHideSystem locks in that system namespaces are only dropped from the
// cluster-wide view, with a count of what was hidden: an explicit -n kube-system
// or --include-system must return its rows rather than silently print nothing.
func TestHideSystem(t *testing.T) {
	const note = "1 row hidden in system namespaces (--include-system to show)"
	tests := []struct {
		name     string
		f        kube.Flags
		rows     []string
		wantRows []string
		wantNote bool
	}{
		{"all namespaces hides system", kube.Flags{AllNamespaces: true}, []string{"kube-system", "team-a"}, []string{"team-a"}, true},
		{"cloud prefixes are system", kube.Flags{}, []string{"gke-managed-cim", "gmp-system", "amazon-cloudwatch", "aws-observability", "azure-arc", "kube-node-lease"}, nil, false},
		{"suffix -system is not", kube.Flags{AllNamespaces: true}, []string{"payment-system"}, []string{"payment-system"}, false},
		{"explicit -n kube-system is honoured", kube.Flags{Namespace: "kube-system"}, []string{"kube-system"}, []string{"kube-system"}, false},
		{"--include-system keeps them", kube.Flags{AllNamespaces: true, IncludeSystem: true}, []string{"kube-system"}, []string{"kube-system"}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			tbl := kube.NewTable(&out, kube.Painter{}, "NS", "POD")
			for _, ns := range tc.rows {
				tbl.Row(ns, "p")
			}
			hideSystem(tbl, tc.f)
			if err := tbl.Flush(); err != nil {
				t.Fatal(err)
			}
			got := out.String()
			for _, ns := range tc.rows {
				if want := slices.Contains(tc.wantRows, ns); strings.Contains(got, ns+" ") != want {
					t.Errorf("row %q shown = %v, want %v:\n%s", ns, !want, want, got)
				}
			}
			if strings.Contains(got, note) != tc.wantNote {
				t.Errorf("note shown = %v, want %v:\n%s", !tc.wantNote, tc.wantNote, got)
			}
		})
	}
}

func TestBothListsReturnsBothResults(t *testing.T) {
	a, b, err := bothLists(
		func() ([]int, error) { return []int{1, 2}, nil },
		func() (string, error) { return "ok", nil },
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(a) != 2 || b != "ok" {
		t.Fatalf("got (%v, %q), want ([1 2], \"ok\")", a, b)
	}
}

func TestBothListsPropagatesEitherError(t *testing.T) {
	boom := errors.New("boom")
	ok := func() (int, error) { return 1, nil }
	bad := func() (int, error) { return 0, boom }
	for _, tc := range []struct {
		name string
		a, b func() (int, error)
	}{
		{"first fails", bad, ok},
		{"second fails", ok, bad},
		{"both fail", bad, bad},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, _, err := bothLists(tc.a, tc.b); !errors.Is(err, boom) {
				t.Fatalf("err = %v, want %v", err, boom)
			}
		})
	}
}

// TestBothListsRunsConcurrently proves overlap rather than merely allowing it:
// the two calls rendezvous, so if bothLists ran them one after the other the
// first would block forever waiting for a partner that has not started.
func TestBothListsRunsConcurrently(t *testing.T) {
	first, second := make(chan struct{}), make(chan struct{})
	a := func() (int, error) { close(first); <-second; return 1, nil }
	b := func() (int, error) { <-first; close(second); return 2, nil }

	done := make(chan struct{})
	go func() {
		defer close(done)
		if x, y, err := bothLists(a, b); x != 1 || y != 2 || err != nil {
			t.Errorf("got (%d, %d, %v), want (1, 2, nil)", x, y, err)
		}
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("bothLists ran its two calls sequentially")
	}
}
