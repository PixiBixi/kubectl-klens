package view

import (
	"bytes"
	"context"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/PixiBixi/kubectl-klens/internal/kube"
)

const legacyStatus = `Cluster-autoscaler status at 2026-06-17 09:30:00.123 +0000 UTC:
Cluster-wide:
  Health:      Healthy (ready=10 unready=0 notStarted=0 registered=10 longUnregistered=0)
               LastProbeTime:      2026-06-17 09:29:55 +0000 UTC
  ScaleUp:     NoActivity (ready=10 registered=10)
               LastProbeTime:      2026-06-17 09:29:55 +0000 UTC
  ScaleDown:   NoCandidates (candidates=0)
               LastProbeTime:      2026-06-17 09:29:55 +0000 UTC

NodeGroups:
  Name:        https://www.googleapis.com/compute/v1/projects/p/zones/europe-west1-b/instanceGroups/gke-prod-pool-1-abc123-grp
  Health:      Healthy (ready=3 unready=0 registered=3 longUnregistered=0 cloudProviderTarget=3 (minSize=1, maxSize=10))
               LastProbeTime:      2026-06-17 09:29:55 +0000 UTC
  ScaleUp:     NoActivity (ready=3 cloudProviderTarget=3)
  ScaleDown:   NoCandidates (candidates=0)
  Name:        https://www.googleapis.com/compute/v1/projects/p/zones/europe-west1-c/instanceGroups/gke-prod-spot-def456-grp
  Health:      Healthy (ready=7 unready=0 registered=7 longUnregistered=0 cloudProviderTarget=7 (minSize=0, maxSize=20))
  ScaleUp:     NoActivity (ready=7 cloudProviderTarget=7)
  ScaleDown:   CandidatesPresent (candidates=2)
`

const yamlStatus = `time: 2026-06-18 13:06:59.723405459 +0000 UTC
autoscalerStatus: Running
clusterWide:
  health:
    status: Healthy
    nodeCounts:
      registered:
        total: 35
        ready: 35
        notStarted: 0
      longUnregistered: 0
      unregistered: 0
  scaleUp:
    status: NoActivity
  scaleDown:
    status: NoCandidates
nodeGroups:
  - name: https://www.googleapis.com/compute/v1/projects/p/zones/europe-west1-b/instanceGroups/gke-prod-pool-1-abc123-grp
    health:
      status: Healthy
      nodeCounts:
        registered:
          total: 3
          ready: 3
      cloudProviderTarget: 3
      minSize: 1
      maxSize: 10
      lastProbeTime: "2026-06-18T13:50:00Z"
      lastTransitionTime: "2026-06-15T10:39:23Z"
    scaleUp:
      status: NoActivity
      lastProbeTime: "2026-06-18T13:50:00Z"
      lastTransitionTime: "2026-06-18T13:44:33Z"
    scaleDown:
      status: NoCandidates
      lastProbeTime: "2026-06-18T13:50:00Z"
      lastTransitionTime: "2026-06-18T13:49:24Z"
  - name: gke-prod-spot-def456-grp
    health:
      status: Healthy
      nodeCounts:
        registered:
          total: 7
          ready: 7
      cloudProviderTarget: 7
      minSize: 0
      maxSize: 20
    scaleUp:
      status: NoActivity
    scaleDown:
      status: CandidatesPresent
      lastProbeTime: "2026-06-18T13:50:00Z"
      lastTransitionTime: "2026-06-18T12:00:00Z"
`

func autoscalerCM(status string) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "cluster-autoscaler-status", Namespace: "kube-system"},
		Data:       map[string]string{"status": status},
	}
}

// rowFields returns the whitespace-split cells of the table row whose first
// cell is name, or nil if no such row was printed.
func rowFields(out, name string) []string {
	for _, line := range strings.Split(out, "\n") {
		if f := strings.Fields(line); len(f) > 0 && f[0] == name {
			return f
		}
	}
	return nil
}

func renderTo(t *testing.T, status string) string {
	t.Helper()
	c := fake.NewClientset(autoscalerCM(status))
	var buf bytes.Buffer
	if err := Autoscaler(context.Background(), c, kube.Flags{}, nil, &buf); err != nil {
		t.Fatal(err)
	}
	return buf.String()
}

func assertNodeGroupTable(t *testing.T, out string) {
	t.Helper()
	if strings.Contains(out, "googleapis.com") {
		t.Fatalf("nodegroup name should be shortened to its last path segment:\n%s", out)
	}
	pool := rowFields(out, "gke-prod-pool-1-abc123-grp")
	// Cells 0-7 are format-independent; cell 8 (LAST-CHANGE) is asserted per
	// test since the legacy format does not carry per-condition transitions.
	want := []string{"gke-prod-pool-1-abc123-grp", "Healthy", "3", "3", "1", "10", "NoActivity", "NoCandidates"}
	if len(pool) != 9 {
		t.Fatalf("want 9 cells for pool row, got %v", pool)
	}
	for i := range want {
		if pool[i] != want[i] {
			t.Fatalf("pool row cell %d = %q, want %q (row %v)", i, pool[i], want[i], pool)
		}
	}
	spot := rowFields(out, "gke-prod-spot-def456-grp")
	if len(spot) != 9 || spot[3] != "7" || spot[4] != "0" || spot[5] != "20" || spot[7] != "CandidatesPresent" {
		t.Fatalf("unexpected spot row: %v", spot)
	}
}

func TestAutoscalerYAMLFormat(t *testing.T) {
	out := renderTo(t, yamlStatus)
	summary := strings.Split(out, "\n")[0]
	for _, want := range []string{"Cluster-wide: Healthy", "scaleUp=NoActivity", "scaleDown=NoCandidates", "(ready 35/35)", "2026-06-18"} {
		if !strings.Contains(summary, want) {
			t.Fatalf("summary line missing %q:\n%s", want, summary)
		}
	}
	assertNodeGroupTable(t, out)
	// LAST-CHANGE is the most recent lastTransitionTime across the group's
	// conditions, not lastProbeTime (which is later in the fixture).
	if got := rowFields(out, "gke-prod-pool-1-abc123-grp")[8]; got != "2026-06-18T13:49:24Z" {
		t.Fatalf("pool LAST-CHANGE = %q, want 2026-06-18T13:49:24Z", got)
	}
	if got := rowFields(out, "gke-prod-spot-def456-grp")[8]; got != "2026-06-18T12:00:00Z" {
		t.Fatalf("spot LAST-CHANGE = %q, want 2026-06-18T12:00:00Z", got)
	}
}

func TestAutoscalerLegacyFormat(t *testing.T) {
	out := renderTo(t, legacyStatus)
	summary := strings.Split(out, "\n")[0]
	for _, want := range []string{"Cluster-wide: Healthy", "scaleUp=NoActivity", "scaleDown=NoCandidates", "(ready 10/10)"} {
		if !strings.Contains(summary, want) {
			t.Fatalf("summary line missing %q:\n%s", want, summary)
		}
	}
	assertNodeGroupTable(t, out)
	// The legacy text format carries no per-condition transition times.
	if got := rowFields(out, "gke-prod-pool-1-abc123-grp")[8]; got != "-" {
		t.Fatalf("legacy pool LAST-CHANGE should be %q, got %q", "-", got)
	}
}

func TestAutoscalerFallsBackToVerbatim(t *testing.T) {
	// Neither valid structured YAML nor the legacy section markers: echo it.
	raw := "Some unexpected status text\nfrom a future cluster-autoscaler\n"
	out := renderTo(t, raw)
	if !strings.Contains(out, "Some unexpected status text") {
		t.Fatalf("unrecognized format should be echoed verbatim:\n%s", out)
	}
}

func TestAutoscalerMissingConfigMap(t *testing.T) {
	c := fake.NewClientset()
	var buf bytes.Buffer
	if err := Autoscaler(context.Background(), c, kube.Flags{}, nil, &buf); err == nil {
		t.Fatal("expected error when configmap is missing, got nil")
	}
}
