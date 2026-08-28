package view

import (
	"cmp"
	"context"
	"io"
	"slices"
	"strings"

	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/PixiBixi/kubectl-klens/internal/kube"
)

// PvcResize lists PVCs whose provisioned capacity does not match the size the
// spec asks for, plus those the resize state machine is still working through or
// has given up on. A claim already at its requested size is not listed, so an
// empty table means no expansion is in flight or stuck.
//
// `kubectl get pvc` prints status.capacity alone, which makes an expansion that
// never left the ground look exactly like one that landed. A bigger
// spec.resources.requests is only a *request*: the StorageClass has to allow
// expansion, the provisioner has to accept the new size, and a filesystem volume
// then needs its node to remount it before the pod sees the space. Each of those
// stalls in a different place, so each gets its own verdict.
//
// Rows default to VERDICT (risk) order, riskiest at the bottom.
func PvcResize(ctx context.Context, c kube.Clients, f kube.Flags, args []string, out io.Writer) error {
	var (
		pvcs    []corev1.PersistentVolumeClaim
		classes []storagev1.StorageClass
	)
	ns := f.NamespaceScope()
	err := allLists(
		func() (err error) {
			pvcs, err = kube.ListPersistentVolumeClaims(ctx, c, ns, metav1.ListOptions{})
			return err
		},
		func() error {
			// StorageClasses are cluster-scoped, so a namespace-scoped token cannot
			// list them. Degrade to an unknown expansion policy instead of failing
			// the whole view over a lookup that only sharpens one verdict.
			classes, _ = kube.ListStorageClasses(ctx, c, metav1.ListOptions{})
			return nil
		},
	)
	if err != nil {
		return err
	}
	expandable := expansionPolicies(classes)
	paint := kube.NewPainter(f)

	type entry struct {
		pvc          *corev1.PersistentVolumeClaim
		verdict, sev string
	}
	var list []entry
	for i := range pvcs {
		p := &pvcs[i]
		v, sev := resizeVerdict(p, expandable)
		if v == "" {
			continue
		}
		list = append(list, entry{p, v, sev})
	}

	t := kube.NewTable(out, paint, "NS", "PVC", "CAPACITY", "REQUESTED", "CLASS", "POD", "VERDICT")
	// The pod list is only needed for the POD column, and the usual answer here
	// is an empty table - so pay for it only once there is a row to fill in.
	// Listing every pod on a 6500-pod cluster is ~86 MiB and ~3s (see the
	// measurement in kube/client.go), which is not a price to pay for output
	// that turns out to be a header line.
	if len(list) == 0 {
		return t.Flush()
	}
	pods, err := kube.ListPods(ctx, c, ns, metav1.ListOptions{})
	if err != nil {
		return err
	}
	mounts := claimMounts(pods)

	// Deterministic tiebreak for rows sharing a verdict; the VERDICT sort applied
	// at Flush is stable, so this order survives within each verdict.
	slices.SortStableFunc(list, func(a, b entry) int {
		return cmp.Or(
			cmp.Compare(a.pvc.Namespace, b.pvc.Namespace),
			cmp.Compare(a.pvc.Name, b.pvc.Name),
		)
	})

	for i := range list {
		e := &list[i]
		t.Row(
			e.pvc.Namespace,
			e.pvc.Name,
			storageCell(paint, e.pvc.Status.Capacity),
			storageCell(paint, e.pvc.Spec.Resources.Requests),
			storageClassCell(paint, e.pvc),
			podCell(paint, mounts[e.pvc.Namespace+"/"+e.pvc.Name]),
			sevPaint(paint, e.sev)(e.verdict),
		)
	}
	t.SortRank("VERDICT", verdictRank(
		"SC-NO-EXPAND", "INFEASIBLE", "FAILED", "SHRINK", "FS-PENDING", "RESIZING", "PENDING",
	))
	t.SortBy(orDefault(f.Sort, "verdict"))
	return t.Flush()
}

// resizeVerdict grades a claim's resize state, returning "" for one that has
// nothing in flight. Order matters: a terminal refusal outranks the in-progress
// condition that may still be hanging around next to it.
//
// A claim with no status.capacity is skipped entirely - it was never provisioned,
// so its request is an initial size and not a resize (pvc-unused calls that
// UNBOUND).
func resizeVerdict(p *corev1.PersistentVolumeClaim, expandable map[string]bool) (verdict, sev string) {
	capacity, bound := p.Status.Capacity[corev1.ResourceStorage]
	if !bound {
		return "", ""
	}
	requested, asked := p.Spec.Resources.Requests[corev1.ResourceStorage]
	growing := asked && requested.Cmp(capacity) > 0

	switch {
	case hasResourceStatus(p, corev1.PersistentVolumeClaimControllerResizeInfeasible,
		corev1.PersistentVolumeClaimNodeResizeInfeasible):
		// The provisioner or the node rejected the target size outright (past the
		// disk type's ceiling, unsupported step). Retrying the same number won't fix it.
		return "INFEASIBLE", "bad"
	case hasCondition(p, corev1.PersistentVolumeClaimControllerResizeError,
		corev1.PersistentVolumeClaimNodeResizeError):
		return "FAILED", "bad"
	case growing && hasClass(expandable, className(p)) && !expandable[className(p)]:
		// allowVolumeExpansion is false on the class: the request will sit there
		// forever. The only way out is a new PVC, so this is the worst verdict.
		return "SC-NO-EXPAND", "bad"
	case hasCondition(p, corev1.PersistentVolumeClaimFileSystemResizePending) ||
		hasResourceStatus(p, corev1.PersistentVolumeClaimNodeResizePending):
		// Volume grown on the cloud side, filesystem not yet: the node resizes it on
		// the next mount, so the pod has to restart before the space shows up.
		return "FS-PENDING", "warn"
	case hasCondition(p, corev1.PersistentVolumeClaimResizing) ||
		hasResourceStatus(p, corev1.PersistentVolumeClaimControllerResizeInProgress,
			corev1.PersistentVolumeClaimNodeResizeInProgress):
		return "RESIZING", "warn"
	case growing:
		return "PENDING", "warn"
	case asked && requested.Cmp(capacity) < 0:
		// Kubernetes cannot shrink a PVC. The spec was lowered and nothing happened,
		// which reads like a completed change on a `kubectl get pvc` that only shows
		// capacity - and leaves the two numbers disagreeing for good.
		return "SHRINK", "warn"
	}
	return "", ""
}

// expansionPolicies maps a StorageClass name to its allowVolumeExpansion. A nil
// field means false, the API default.
func expansionPolicies(classes []storagev1.StorageClass) map[string]bool {
	out := make(map[string]bool, len(classes))
	for i := range classes {
		sc := &classes[i]
		out[sc.Name] = sc.AllowVolumeExpansion != nil && *sc.AllowVolumeExpansion
	}
	return out
}

// hasClass reports whether the class was actually resolved. An unnamed class, or
// one we could not list, must not be reported as refusing expansion.
func hasClass(expandable map[string]bool, name string) bool {
	if name == "" {
		return false
	}
	_, ok := expandable[name]
	return ok
}

func className(p *corev1.PersistentVolumeClaim) string {
	if p.Spec.StorageClassName == nil {
		return ""
	}
	return *p.Spec.StorageClassName
}

func hasCondition(p *corev1.PersistentVolumeClaim, types ...corev1.PersistentVolumeClaimConditionType) bool {
	for i := range p.Status.Conditions {
		cond := &p.Status.Conditions[i]
		if cond.Status != corev1.ConditionTrue {
			continue
		}
		if slices.Contains(types, cond.Type) {
			return true
		}
	}
	return false
}

func hasResourceStatus(p *corev1.PersistentVolumeClaim, statuses ...corev1.ClaimResourceStatus) bool {
	return slices.Contains(statuses, p.Status.AllocatedResourceStatuses[corev1.ResourceStorage])
}

// claimMounts indexes the pods mounting each "ns/name" claim. Which pod holds it
// is the actionable half of FS-PENDING: that is the one to restart.
func claimMounts(pods []corev1.Pod) map[string][]string {
	out := map[string][]string{}
	for i := range pods {
		p := &pods[i]
		for j := range p.Spec.Volumes {
			if pvc := p.Spec.Volumes[j].PersistentVolumeClaim; pvc != nil {
				key := p.Namespace + "/" + pvc.ClaimName
				out[key] = append(out[key], p.Name)
			}
		}
	}
	return out
}

// podCell lists the mounting pods. No pod is worth flagging rather than blanking:
// a filesystem resize needs a mount to happen at all.
func podCell(paint kube.Painter, pods []string) string {
	if len(pods) == 0 {
		return paint.Muted("<none>")
	}
	slices.Sort(pods)
	return strings.Join(pods, ",")
}

func storageCell(paint kube.Painter, l corev1.ResourceList) string {
	if q, ok := l[corev1.ResourceStorage]; ok {
		return q.String()
	}
	return paint.Muted("-")
}
