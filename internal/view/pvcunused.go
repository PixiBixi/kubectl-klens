package view

import (
	"context"
	"io"
	"slices"
	"strconv"
	"strings"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	"github.com/PixiBixi/kubectl-klens/internal/kube"
)

// PvcUnused lists PVCs no pod mounts - provisioned disks nobody is reading or
// writing, billed all the same. It is the counterpart to pvc, which shows the
// claims that are in use.
//
// A cloud-side audit (grafana/unused and friends) cannot find these: the disk is
// attached to a live PV that a live PVC references, so from the provider's side
// it is in use. Only the pod side of the join shows that nothing mounts it.
// Rows default to VERDICT (risk) order, riskiest at the bottom.
func PvcUnused(ctx context.Context, c kube.Clients, f kube.Flags, args []string, out io.Writer) error {
	var (
		pvcs     []corev1.PersistentVolumeClaim
		pods     []corev1.Pod
		stateful []appsv1.StatefulSet
	)
	scope := f.Scope()
	err := kube.Concurrent(
		func() (err error) {
			pvcs, err = kube.ListPersistentVolumeClaims(ctx, c, scope, metav1.ListOptions{})
			return err
		},
		func() (err error) {
			pods, err = kube.ListPods(ctx, c, scope, metav1.ListOptions{})
			return err
		},
		func() (err error) {
			stateful, err = kube.ListStatefulSets(ctx, c, scope, metav1.ListOptions{})
			return err
		},
	)
	if err != nil {
		return err
	}
	mounted := mountedClaims(pods)
	owners := statefulSetClaims(stateful)
	paint := kube.NewPainter(f)

	type entry struct {
		pvc          *corev1.PersistentVolumeClaim
		verdict, sev string
	}
	var list []entry
	for i := range pvcs {
		p := &pvcs[i]
		if mounted[objKey(&p.ObjectMeta)] {
			continue
		}
		v, sev := pvcVerdict(p, owners)
		list = append(list, entry{p, v, sev})
	}
	slices.SortStableFunc(list, func(a, b entry) int { return byNsName(&a.pvc.ObjectMeta, &b.pvc.ObjectMeta) })

	t := kube.NewTable(out, paint, "NS", "PVC", "STATUS", "CAPACITY", "CLASS", "VOLUME", "VERDICT")
	for i := range list {
		e := &list[i]
		t.Row(
			e.pvc.Namespace,
			e.pvc.Name,
			paint.Status(string(e.pvc.Status.Phase)),
			pvcCapacity(e.pvc),
			storageClassCell(paint, e.pvc),
			orMutedDash(paint, e.pvc.Spec.VolumeName),
			sevPaint(paint, e.sev)(e.verdict),
		)
	}
	return flushVerdicts(t, f.Sort, "LOST", "ORPHAN", "SCALED-DOWN", "STS-RESERVED", "UNBOUND")
}

// pvcVerdict grades an unmounted claim. A claim a StatefulSet can still hand
// back to a pod is not waste in the same way an ownerless one is: scaling the
// set back up reuses it, which is the whole point of a volumeClaimTemplate.
func pvcVerdict(p *corev1.PersistentVolumeClaim, owners map[string]stsClaim) (verdict, sev string) {
	switch p.Status.Phase {
	case corev1.ClaimLost:
		return "LOST", "bad" // the bound volume is gone: the data is not coming back
	case corev1.ClaimPending:
		return "UNBOUND", "muted" // nothing provisioned yet, so nothing billed yet
	}
	owner, ok := matchStatefulSetClaim(p.Namespace+"/"+p.Name, owners)
	switch {
	case !ok:
		return "ORPHAN", "bad" // no pod, no owner that could ever mount it again
	case owner.beyondScale:
		return "SCALED-DOWN", "warn" // left behind by a scale-down; reused only if the set grows back
	default:
		return "STS-RESERVED", "warn" // within the set's replica count: its pod is expected back
	}
}

// mountedClaims is the set of claims some pod references.
func mountedClaims(pods []corev1.Pod) map[types.NamespacedName]bool {
	mounted := map[types.NamespacedName]bool{}
	for claim := range claimRefs(pods) {
		mounted[claim] = true
	}
	return mounted
}

// stsClaim describes the StatefulSet slot a claim belongs to.
type stsClaim struct {
	replicas    int
	beyondScale bool
}

// statefulSetClaims indexes the claim-name prefixes a StatefulSet's
// volumeClaimTemplates generate, keyed "ns/<template>-<set>-" so a claim is
// matched by stripping its ordinal. This is the naming contract the StatefulSet
// controller itself uses to find a pod's volumes again after a restart.
func statefulSetClaims(sets []appsv1.StatefulSet) map[string]stsClaim {
	out := map[string]stsClaim{}
	for i := range sets {
		s := &sets[i]
		for j := range s.Spec.VolumeClaimTemplates {
			tmpl := &s.Spec.VolumeClaimTemplates[j]
			out[s.Namespace+"/"+tmpl.Name+"-"+s.Name+"-"] = stsClaim{replicas: replicasOrOne(s.Spec.Replicas)}
		}
	}
	return out
}

// matchStatefulSetClaim resolves a namespaced claim ("ns/name") to the
// StatefulSet slot that owns it, reporting whether the slot sits beyond the
// set's current replica count.
//
// A claim is "<template>-<set>-<ordinal>" and the ordinal holds no "-", so the
// owner is a direct lookup on everything up to the last one.
func matchStatefulSetClaim(key string, owners map[string]stsClaim) (stsClaim, bool) {
	prefix, rest, ok := strings.CutLast(key, "-")
	if !ok {
		return stsClaim{}, false
	}
	owner, ok := owners[prefix+"-"]
	if !ok {
		return stsClaim{}, false
	}
	ordinal, err := strconv.Atoi(rest)
	if err != nil {
		return stsClaim{}, false
	}
	owner.beyondScale = ordinal >= owner.replicas
	return owner, true
}

// pvcCapacity prefers the bound size over the requested one: what is billed is
// what the provisioner actually created, which can be rounded up.
func pvcCapacity(p *corev1.PersistentVolumeClaim) string {
	if q, ok := p.Status.Capacity[corev1.ResourceStorage]; ok {
		return q.String()
	}
	if q, ok := p.Spec.Resources.Requests[corev1.ResourceStorage]; ok {
		return q.String()
	}
	return "-"
}

func storageClassCell(paint kube.Painter, p *corev1.PersistentVolumeClaim) string {
	if name := className(p); name != "" {
		return name
	}
	return paint.Muted("<default>")
}
