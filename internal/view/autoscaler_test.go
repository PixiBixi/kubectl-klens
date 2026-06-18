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

func TestAutoscalerLegacyFormat(t *testing.T) {
	c := fake.NewClientset(autoscalerCM(legacyStatus))
	var buf bytes.Buffer
	if err := Autoscaler(context.Background(), c, kube.Flags{}, nil, &buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()

	summary := strings.Split(out, "\n")[0]
	for _, want := range []string{"Cluster-wide: Healthy", "scaleUp=NoActivity", "scaleDown=NoCandidates", "(ready 10/10)"} {
		if !strings.Contains(summary, want) {
			t.Fatalf("summary line missing %q:\n%s", want, summary)
		}
	}

	if strings.Contains(out, "googleapis.com") {
		t.Fatalf("nodegroup name should be shortened to its last path segment:\n%s", out)
	}

	pool := rowFields(out, "gke-prod-pool-1-abc123-grp")
	want := []string{"gke-prod-pool-1-abc123-grp", "Healthy", "3", "3", "1", "10", "NoActivity", "NoCandidates"}
	if len(pool) != len(want) {
		t.Fatalf("want %d cells for pool row, got %v", len(want), pool)
	}
	for i := range want {
		if pool[i] != want[i] {
			t.Fatalf("pool row cell %d = %q, want %q (row %v)", i, pool[i], want[i], pool)
		}
	}

	spot := rowFields(out, "gke-prod-spot-def456-grp")
	if spot[3] != "7" || spot[4] != "0" || spot[5] != "20" || spot[7] != "CandidatesPresent" {
		t.Fatalf("unexpected spot row: %v", spot)
	}
}

func TestAutoscalerFallsBackToVerbatim(t *testing.T) {
	// A structured-YAML status has none of the legacy section markers, so it
	// must be echoed verbatim rather than rendered as an empty table.
	yamlStatus := "time: 2026-06-17T09:30:00Z\nautoscalerStatus: Running\nclusterWide:\n  health:\n    status: Healthy\n"
	c := fake.NewClientset(autoscalerCM(yamlStatus))
	var buf bytes.Buffer
	if err := Autoscaler(context.Background(), c, kube.Flags{}, nil, &buf); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "autoscalerStatus: Running") {
		t.Fatalf("unrecognized format should be echoed verbatim:\n%s", buf.String())
	}
}

func TestAutoscalerMissingConfigMap(t *testing.T) {
	c := fake.NewClientset()
	var buf bytes.Buffer
	if err := Autoscaler(context.Background(), c, kube.Flags{}, nil, &buf); err == nil {
		t.Fatal("expected error when configmap is missing, got nil")
	}
}
