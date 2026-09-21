package view

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/PixiBixi/kubectl-klens/internal/kube"
)

// pvFor builds a volume in the given phase, with a CSI handle shaped the way a
// cloud provisioner writes it so the DISK column has something to trim.
func pvFor(name, size string, phase corev1.PersistentVolumePhase, policy corev1.PersistentVolumeReclaimPolicy) *corev1.PersistentVolume {
	pv := &corev1.PersistentVolume{
		Name:              name,
		CreationTimestamp: metav1.NewTime(time.Now().Add(-48 * time.Hour)),
		Spec: corev1.PersistentVolumeSpec{
			Capacity:                      corev1.ResourceList{corev1.ResourceStorage: resource.MustParse(size)},
			PersistentVolumeReclaimPolicy: policy,
			PersistentVolumeSource: corev1.PersistentVolumeSource{
				CSI: &corev1.CSIPersistentVolumeSource{
					Driver:       "pd.csi.storage.gke.io",
					VolumeHandle: "projects/p/zones/europe-west4-c/disks/" + name,
				},
			},
			ClaimRef: &corev1.ObjectReference{Namespace: "elk", Name: "data-" + name},
		},
		Status: corev1.PersistentVolumeStatus{Phase: phase},
	}
	return pv
}

func runPvOrphan(t *testing.T, objs ...runtime.Object) string {
	t.Helper()
	cs := fake.NewClientset(objs...)
	var buf bytes.Buffer
	if err := PvOrphan(context.Background(), kube.Clients{Interface: cs}, kube.Flags{}, nil, &buf); err != nil {
		t.Fatalf("PvOrphan: %v", err)
	}
	return buf.String()
}

func TestPvOrphanReportsRetainedVolumes(t *testing.T) {
	out := runPvOrphan(t,
		pvFor("pvc-retained", "20Gi", corev1.VolumeReleased, corev1.PersistentVolumeReclaimRetain),
	)
	for _, want := range []string{"pvc-retained", "Released", "Retain", "20Gi", "elk/data-pvc-retained", "RETAINED"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

// A bound volume is in use: reporting it would make the command useless on any
// healthy cluster, which is the whole point of the phase filter.
func TestPvOrphanSkipsBoundVolumes(t *testing.T) {
	out := runPvOrphan(t,
		pvFor("pvc-live", "20Gi", corev1.VolumeBound, corev1.PersistentVolumeReclaimDelete),
	)
	if strings.Contains(out, "pvc-live") {
		t.Errorf("bound volume reported:\n%s", out)
	}
}

func TestPvOrphanVerdictPerPhaseAndPolicy(t *testing.T) {
	for _, tc := range []struct {
		name   string
		phase  corev1.PersistentVolumePhase
		policy corev1.PersistentVolumeReclaimPolicy
		want   string
	}{
		{"retain kept", corev1.VolumeReleased, corev1.PersistentVolumeReclaimRetain, "RETAINED"},
		{"delete pending", corev1.VolumeReleased, corev1.PersistentVolumeReclaimDelete, "RECLAIMING"},
		{"reclaim errored", corev1.VolumeFailed, corev1.PersistentVolumeReclaimDelete, "FAILED"},
		{"never claimed", corev1.VolumeAvailable, corev1.PersistentVolumeReclaimDelete, "UNCLAIMED"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out := runPvOrphan(t, pvFor("pvc-x", "8Gi", tc.phase, tc.policy))
			if !strings.Contains(out, tc.want) {
				t.Errorf("want verdict %q:\n%s", tc.want, out)
			}
		})
	}
}

// The handle is the actionable cell: a cloud CLI takes the disk name, not the
// full CSI path.
func TestPvOrphanTrimsCSIHandleToDiskName(t *testing.T) {
	out := runPvOrphan(t,
		pvFor("pvc-disk", "8Gi", corev1.VolumeReleased, corev1.PersistentVolumeReclaimRetain),
	)
	if strings.Contains(out, "projects/p/zones") {
		t.Errorf("full CSI path leaked into the table:\n%s", out)
	}
	if !strings.Contains(out, "pvc-disk") {
		t.Errorf("disk name missing:\n%s", out)
	}
}

// A volume provisioned outside CSI has no handle to show, and must not print an
// empty cell that reads as a missing disk.
func TestPvOrphanHandlesVolumeWithoutCSISource(t *testing.T) {
	pv := pvFor("pvc-nocsi", "8Gi", corev1.VolumeReleased, corev1.PersistentVolumeReclaimRetain)
	pv.Spec.CSI = nil
	pv.Spec.ClaimRef = nil
	out := runPvOrphan(t, pv)
	var row string
	for l := range strings.SplitSeq(out, "\n") {
		if strings.HasPrefix(l, "pvc-nocsi") {
			row = l
		}
	}
	if row == "" {
		t.Fatalf("volume missing:\n%s", out)
	}
	// Both the disk handle and the former claim are unknown here, so the row
	// carries two placeholders rather than blanks.
	if n := strings.Count(row, " - "); n < 2 {
		t.Errorf("want 2 placeholder cells, got %d in %q", n, row)
	}
}

// Risk order puts the rows worth acting on nearest the prompt.
func TestPvOrphanSortsRiskiestLast(t *testing.T) {
	out := runPvOrphan(t,
		pvFor("pvc-a", "8Gi", corev1.VolumeAvailable, corev1.PersistentVolumeReclaimDelete),
		pvFor("pvc-b", "8Gi", corev1.VolumeFailed, corev1.PersistentVolumeReclaimDelete),
	)
	failed, unclaimed := strings.Index(out, "FAILED"), strings.Index(out, "UNCLAIMED")
	if failed < 0 || unclaimed < 0 {
		t.Fatalf("both verdicts expected:\n%s", out)
	}
	if failed < unclaimed {
		t.Errorf("FAILED should sort below UNCLAIMED:\n%s", out)
	}
}
