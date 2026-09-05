package view

import (
	"bytes"
	"context"
	"slices"
	"strconv"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/PixiBixi/kubectl-klens/internal/kube"
)

// resizingPvc builds a bound claim whose spec asks for requested while the
// provisioner has delivered capacity.
func resizingPvc(name, ns, class, capacity, requested string) *corev1.PersistentVolumeClaim {
	p := pvcFor(name, ns, class, capacity, corev1.ClaimBound)
	p.Spec.Resources = corev1.VolumeResourceRequirements{
		Requests: corev1.ResourceList{corev1.ResourceStorage: resource.MustParse(requested)},
	}
	return p
}

func storageClass(name string, expansion bool) *storagev1.StorageClass {
	return &storagev1.StorageClass{
		Name:                 name,
		AllowVolumeExpansion: &expansion,
	}
}

func withCondition(p *corev1.PersistentVolumeClaim, t corev1.PersistentVolumeClaimConditionType) *corev1.PersistentVolumeClaim {
	p.Status.Conditions = append(p.Status.Conditions, corev1.PersistentVolumeClaimCondition{
		Type: t, Status: corev1.ConditionTrue,
	})
	return p
}

func withResourceStatus(p *corev1.PersistentVolumeClaim, s corev1.ClaimResourceStatus) *corev1.PersistentVolumeClaim {
	p.Status.AllocatedResourceStatuses = map[corev1.ResourceName]corev1.ClaimResourceStatus{
		corev1.ResourceStorage: s,
	}
	return p
}

func runPvcResize(t *testing.T, objs ...runtime.Object) string {
	t.Helper()
	c := fake.NewClientset(objs...)
	var buf bytes.Buffer
	if err := PvcResize(context.Background(), clients(c), kube.Flags{}, nil, &buf); err != nil {
		t.Fatal(err)
	}
	return buf.String()
}

// verdictOn returns the VERDICT cell of the row naming pvc, so an assertion
// cannot be satisfied by another row - "PENDING" is a substring of "FS-PENDING".
func verdictOn(t *testing.T, out, pvc string) string {
	t.Helper()
	for line := range strings.Lines(out) {
		fields := strings.Fields(line)
		if len(fields) == 7 && fields[1] == pvc {
			return fields[6]
		}
	}
	t.Fatalf("no row for %q:\n%s", pvc, out)
	return ""
}

func TestPvcResize(t *testing.T) {
	out := runPvcResize(t,
		storageClass("expandable", true),
		storageClass("fixed", false),
		// Settled: capacity already matches the request, nothing in flight.
		resizingPvc("settled", "app", "expandable", "10Gi", "10Gi"),
		resizingPvc("asked", "app", "expandable", "150Gi", "180Gi"),
		withCondition(resizingPvc("growing", "app", "expandable", "150Gi", "180Gi"), corev1.PersistentVolumeClaimResizing),
		withCondition(resizingPvc("remount", "app", "expandable", "180Gi", "180Gi"), corev1.PersistentVolumeClaimFileSystemResizePending),
		resizingPvc("blocked", "app", "fixed", "150Gi", "180Gi"),
		withResourceStatus(resizingPvc("refused", "app", "expandable", "150Gi", "9000Gi"), corev1.PersistentVolumeClaimControllerResizeInfeasible),
		withCondition(resizingPvc("errored", "app", "expandable", "150Gi", "180Gi"), corev1.PersistentVolumeClaimNodeResizeError),
		resizingPvc("lowered", "app", "expandable", "180Gi", "150Gi"),
	)
	if strings.Contains(out, "settled") {
		t.Fatalf("a claim already at its requested size must not be listed:\n%s", out)
	}
	for _, want := range []string{"NS", "PVC", "CAPACITY", "REQUESTED", "CLASS", "POD", "VERDICT", "150Gi", "180Gi"} {
		if !strings.Contains(out, want) {
			t.Fatalf("output missing %q:\n%s", want, out)
		}
	}
	for pvc, want := range map[string]string{
		"asked":   "PENDING",
		"growing": "RESIZING",
		"remount": "FS-PENDING",
		"blocked": "SC-NO-EXPAND",
		"refused": "INFEASIBLE",
		"errored": "FAILED",
		"lowered": "SHRINK",
	} {
		if got := verdictOn(t, out, pvc); got != want {
			t.Errorf("%s: verdict %q, want %q\n%s", pvc, got, want, out)
		}
	}
}

// TestPvcResizeSkipsUnboundClaims keeps a claim that was never provisioned out:
// its request is an initial size, not a resize. pvc-unused owns that case.
func TestPvcResizeSkipsUnboundClaims(t *testing.T) {
	p := pvcFor("waiting", "app", "expandable", "5Gi", corev1.ClaimPending)
	p.Status.Capacity = nil
	p.Spec.Resources = corev1.VolumeResourceRequirements{
		Requests: corev1.ResourceList{corev1.ResourceStorage: resource.MustParse("5Gi")},
	}
	if out := runPvcResize(t, storageClass("expandable", true), p); strings.Contains(out, "waiting") {
		t.Fatalf("an unbound claim has nothing to resize:\n%s", out)
	}
}

// TestPvcResizeUnknownClassIsNotBlamed guards the SC-NO-EXPAND verdict: with the
// StorageClass unlistable (namespace-scoped token) the claim must fall back to
// PENDING rather than accuse a class we never read.
func TestPvcResizeUnknownClassIsNotBlamed(t *testing.T) {
	out := runPvcResize(t, resizingPvc("asked", "app", "somewhere-else", "150Gi", "180Gi"))
	if got := verdictOn(t, out, "asked"); got != "PENDING" {
		t.Fatalf("an unresolved StorageClass must not be blamed, got %q:\n%s", got, out)
	}
}

// TestPvcResizeInfeasibleOutranksResizing locks the verdict precedence: a
// terminal refusal wins over the in-progress condition left next to it.
func TestPvcResizeInfeasibleOutranksResizing(t *testing.T) {
	p := withResourceStatus(
		withCondition(resizingPvc("refused", "app", "expandable", "150Gi", "9000Gi"), corev1.PersistentVolumeClaimResizing),
		corev1.PersistentVolumeClaimControllerResizeInfeasible,
	)
	out := runPvcResize(t, storageClass("expandable", true), p)
	if got := verdictOn(t, out, "refused"); got != "INFEASIBLE" {
		t.Fatalf("INFEASIBLE must outrank RESIZING, got %q:\n%s", got, out)
	}
}

// TestPvcResizeReportsMountingPod is the actionable half of FS-PENDING: the pod
// to restart so the node remounts the grown filesystem.
func TestPvcResizeReportsMountingPod(t *testing.T) {
	out := runPvcResize(t,
		storageClass("expandable", true),
		withCondition(resizingPvc("data", "app", "expandable", "180Gi", "180Gi"), corev1.PersistentVolumeClaimFileSystemResizePending),
		podMounting("prometheus-0", "app", "data"),
	)
	if !strings.Contains(out, "prometheus-0") {
		t.Fatalf("output missing the mounting pod:\n%s", out)
	}
	if got := verdictOn(t, out, "data"); got != "FS-PENDING" {
		t.Fatalf("verdict %q, want FS-PENDING:\n%s", got, out)
	}
}

// TestPvcResizeIgnoresStaleFalseCondition guards the condition read: the resize
// machinery leaves Status=False conditions behind, which must not raise a verdict.
func TestPvcResizeIgnoresStaleFalseCondition(t *testing.T) {
	p := resizingPvc("settled", "app", "expandable", "180Gi", "180Gi")
	p.Status.Conditions = []corev1.PersistentVolumeClaimCondition{{
		Type: corev1.PersistentVolumeClaimResizing, Status: corev1.ConditionFalse,
	}}
	if out := runPvcResize(t, storageClass("expandable", true), p); strings.Contains(out, "settled") {
		t.Fatalf("a Status=False condition is not an in-flight resize:\n%s", out)
	}
}

// listedResources names every resource the run issued a list for. The point is
// the negative case: the pod list is the expensive one (~86 MiB on a 6500-pod
// cluster) and it must not be paid for output that is only a header line.
func listedResources(c *fake.Clientset) []string {
	var out []string
	for _, a := range c.Actions() {
		if a.GetVerb() == "list" {
			out = append(out, a.GetResource().Resource)
		}
	}
	return out
}

// TestPvcResizeSkipsPodListWhenEmpty locks the lazy pod list: with nothing to
// report there is no POD column to fill, so the pod list must not be issued.
func TestPvcResizeSkipsPodListWhenEmpty(t *testing.T) {
	c := fake.NewClientset(
		storageClass("expandable", true),
		resizingPvc("settled", "app", "expandable", "10Gi", "10Gi"),
		podMounting("web", "app", "settled"),
	)
	var buf bytes.Buffer
	if err := PvcResize(context.Background(), clients(c), kube.Flags{}, nil, &buf); err != nil {
		t.Fatal(err)
	}
	if slices.Contains(listedResources(c), "pods") {
		t.Errorf("pods were listed for an empty table: %v", listedResources(c))
	}
	if !strings.Contains(buf.String(), "VERDICT") {
		t.Errorf("the header line is still expected:\n%s", buf.String())
	}
}

// TestPvcResizeListsPodsWhenNeeded is the other half: once a row exists the POD
// column has to be filled, so the list is expected.
func TestPvcResizeListsPodsWhenNeeded(t *testing.T) {
	c := fake.NewClientset(
		storageClass("expandable", true),
		resizingPvc("asked", "app", "expandable", "150Gi", "180Gi"),
		podMounting("web", "app", "asked"),
	)
	var buf bytes.Buffer
	if err := PvcResize(context.Background(), clients(c), kube.Flags{}, nil, &buf); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(listedResources(c), "pods") {
		t.Errorf("pods must be listed to fill the POD column: %v", listedResources(c))
	}
	if !strings.Contains(buf.String(), "web") {
		t.Errorf("output missing the mounting pod:\n%s", buf.String())
	}
}

func TestPvcResizeColor(t *testing.T) {
	c := fake.NewClientset(
		storageClass("fixed", false),
		storageClass("expandable", true),
		resizingPvc("blocked", "app", "fixed", "150Gi", "180Gi"),
		withCondition(resizingPvc("growing", "app", "expandable", "150Gi", "180Gi"), corev1.PersistentVolumeClaimResizing),
	)
	var buf bytes.Buffer
	if err := PvcResize(context.Background(), clients(c), kube.Flags{Color: true}, nil, &buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{"\x1b[31mSC-NO-EXPAND", "\x1b[33mRESIZING", "\x1b[90m<none>"} {
		if !strings.Contains(out, want) {
			t.Fatalf("output missing colored %q:\n%q", want, out)
		}
	}
}

// TestPvcResizeScopesPodListToAffectedNamespaces locks the namespace narrowing:
// with one claim resizing in one namespace, the pod list must be scoped to that
// namespace rather than sweeping the cluster.
func TestPvcResizeScopesPodListToAffectedNamespaces(t *testing.T) {
	c := fake.NewClientset(
		storageClass("expandable", true),
		resizingPvc("asked", "app", "expandable", "150Gi", "180Gi"),
		resizingPvc("settled", "other", "expandable", "10Gi", "10Gi"),
		podMounting("web", "app", "asked"),
		podMounting("noise", "other", "settled"),
	)
	var buf bytes.Buffer
	if err := PvcResize(context.Background(), clients(c), kube.Flags{}, nil, &buf); err != nil {
		t.Fatal(err)
	}
	var scopes []string
	for _, a := range c.Actions() {
		if a.GetVerb() == "list" && a.GetResource().Resource == "pods" {
			scopes = append(scopes, a.GetNamespace())
		}
	}
	if !slices.Equal(scopes, []string{"app"}) {
		t.Errorf("pod lists scoped to %v, want [app] only", scopes)
	}
	if !strings.Contains(buf.String(), "web") {
		t.Errorf("output missing the mounting pod:\n%s", buf.String())
	}
}

// TestPvcResizeFallsBackToOneListPastFanout: past maxNamespaceFanout distinct
// namespaces the per-namespace round trips stop paying, so it reverts to a
// single cluster-wide list.
func TestPvcResizeFallsBackToOneListPastFanout(t *testing.T) {
	objs := []runtime.Object{storageClass("expandable", true)}
	for i := range kube.MaxNamespaceFanout + 1 {
		objs = append(objs, resizingPvc("asked", "ns-"+strconv.Itoa(i), "expandable", "150Gi", "180Gi"))
	}
	c := fake.NewClientset(objs...)
	var buf bytes.Buffer
	if err := PvcResize(context.Background(), clients(c), kube.Flags{}, nil, &buf); err != nil {
		t.Fatal(err)
	}
	lists := 0
	for _, a := range c.Actions() {
		if a.GetVerb() == "list" && a.GetResource().Resource == "pods" {
			lists++
			if a.GetNamespace() != "" {
				t.Errorf("pod list scoped to %q, want cluster-wide", a.GetNamespace())
			}
		}
	}
	if lists != 1 {
		t.Errorf("issued %d pod lists, want 1 cluster-wide", lists)
	}
}
