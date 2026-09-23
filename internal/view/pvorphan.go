package view

import (
	"cmp"
	"context"
	"io"
	"slices"
	"strings"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/PixiBixi/kubectl-klens/internal/kube"
)

// PvOrphan lists PersistentVolumes no claim depends on any more, the volumes
// left behind after a PVC was deleted. It is the mirror image of pvc-unused:
// that one finds claims nobody mounts, this one finds volumes nobody claims.
//
// Nothing PVC-oriented can see these, by construction: once the claim is gone
// there is no namespaced object left to list. A reclaimPolicy of Retain then
// keeps the PV in Released forever. Rows default to VERDICT (risk) order,
// riskiest at the bottom.
//
// What this proves, and what it does not: a row means no claim references the
// volume, not that a disk is still being billed. The two coincide right up to
// the moment someone deletes the disk in the provider, after which the PV
// survives as a stale object pointing at nothing. Only a cloud-side sweep
// (grafana/unused and friends) settles the billing question; conversely it
// cannot see a volume a live PVC still holds but no pod mounts, which is what
// pvc-unused is for. Reading both is how the picture closes.
//
// Volumes are read cluster-wide; a PV stuck Bound behind a deleted claim's
// finalizer is not reported, since confirming it would mean listing every
// namespace's claims.
func PvOrphan(ctx context.Context, c kube.Clients, f kube.Flags, _ []string, out io.Writer) error {
	pvs, err := kube.ListPersistentVolumes(ctx, c, metav1.ListOptions{})
	if err != nil {
		return err
	}
	paint := kube.NewPainter(f)

	type entry struct {
		pv           *corev1.PersistentVolume
		verdict, sev string
	}
	var list []entry
	for i := range pvs {
		p := &pvs[i]
		v, sev, ok := pvVerdict(p)
		if !ok {
			continue
		}
		list = append(list, entry{p, v, sev})
	}
	// Deterministic tiebreak for rows sharing a verdict; the VERDICT sort applied
	// at Flush is stable, so this order survives within each verdict.
	slices.SortStableFunc(list, func(a, b entry) int {
		return cmp.Compare(a.pv.Name, b.pv.Name)
	})

	t := kube.NewTable(out, paint, "PV", "STATUS", "RECLAIM", "CAPACITY", "CLAIM", "DISK", "AGE", "VERDICT")
	for i := range list {
		e := &list[i]
		t.Row(
			e.pv.Name,
			paint.Status(string(e.pv.Status.Phase)),
			orMutedDash(paint, e.pv.Spec.PersistentVolumeReclaimPolicy),
			pvCapacity(e.pv),
			formerClaim(paint, e.pv.Spec.ClaimRef),
			diskCell(paint, e.pv),
			age(e.pv.CreationTimestamp),
			sevPaint(paint, e.sev)(e.verdict),
		)
	}
	return flushVerdicts(t, f.Sort, "FAILED", "RETAINED", "RECLAIMING", "UNCLAIMED")
}

// pvVerdict grades a volume, reporting false for the ones a claim still uses.
// The policy is what decides whether anything will ever free the volume: Retain
// is a deliberate "keep the data", which after the claim is gone means a volume
// nobody is watching and no controller is going to reclaim.
func pvVerdict(p *corev1.PersistentVolume) (verdict, sev string, report bool) {
	switch p.Status.Phase {
	case corev1.VolumeFailed:
		return "FAILED", "bad", true // the reclaim itself errored: the disk may or may not be gone
	case corev1.VolumeReleased:
		if p.Spec.PersistentVolumeReclaimPolicy == corev1.PersistentVolumeReclaimRetain {
			return "RETAINED", "bad", true // claim gone, Retain keeps the volume, nothing will reclaim it
		}
		return "RECLAIMING", "warn", true // Delete is running, or the CSI controller is stuck part-way
	case corev1.VolumeAvailable:
		return "UNCLAIMED", "warn", true // provisioned, never bound to anything
	default:
		return "", "", false // Bound or Pending: a claim still depends on it
	}
}

// pvCapacity is what the provisioner recorded as created, which is the size the
// backing disk was given if it still exists.
func pvCapacity(p *corev1.PersistentVolume) string {
	if q, ok := p.Spec.Capacity[corev1.ResourceStorage]; ok {
		return q.String()
	}
	return "-"
}

// formerClaim names the claim the volume was bound to. It is the only clue left
// as to what the disk held, and it routinely points at an object that no longer
// exists - a StatefulSet ordinal past the current replica count, typically.
func formerClaim(paint kube.Painter, ref *corev1.ObjectReference) string {
	if ref == nil || ref.Name == "" {
		return paint.Muted("-")
	}
	if ref.Namespace == "" {
		return ref.Name
	}
	return ref.Namespace + "/" + ref.Name
}

// diskCell is the provider-side handle, the one value that makes the row
// actionable: it is what a cloud CLI takes to look the disk up or delete it.
// CSI stores it as a full path ("projects/p/zones/z/disks/d"), of which the last
// segment is the disk name. The handle is what the PV recorded at bind time, so
// it can name a disk that has since been deleted.
func diskCell(paint kube.Painter, p *corev1.PersistentVolume) string {
	if csi := p.Spec.CSI; csi != nil && csi.VolumeHandle != "" {
		_, name, found := strings.CutLast(csi.VolumeHandle, "/")
		if !found {
			return csi.VolumeHandle
		}
		return name
	}
	return paint.Muted("-")
}
