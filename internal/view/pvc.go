package view

import (
	"context"
	"io"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	"github.com/PixiBixi/kubectl-klens/internal/kube"
)

// Pvc lists PVCs bound to a pod together with the pod's node, storage class and
// provisioned size. Fill rate is deliberately absent: it lives in the kubelet
// volume stats, not the typed API, and df-pv already answers it.
func Pvc(ctx context.Context, c kube.Clients, f kube.Flags, args []string, out io.Writer) error {
	var (
		pods []corev1.Pod
		pvcs []corev1.PersistentVolumeClaim
	)
	scope := f.Scope()
	err := kube.Concurrent(
		func() (err error) {
			pods, err = kube.ListPods(ctx, c, scope, metav1.ListOptions{})
			return err
		},
		func() (err error) {
			pvcs, err = kube.ListPersistentVolumeClaims(ctx, c, scope, metav1.ListOptions{})
			return err
		},
	)
	if err != nil {
		return err
	}
	byClaim := make(map[types.NamespacedName]*corev1.PersistentVolumeClaim, len(pvcs))
	for i := range pvcs {
		byClaim[objKey(&pvcs[i].ObjectMeta)] = &pvcs[i]
	}

	paint := kube.NewPainter(f)
	t := kube.NewTable(out, paint, "NS", "POD", "NODE", "PVC", "CLASS", "CAPACITY")
	for claim, p := range claimRefs(pods) {
		// The pod may reference a claim that does not exist: it will never start,
		// but it is still worth listing, so both cells fall back to a dash.
		class, capacity := paint.Muted("-"), paint.Muted("-")
		if pvc, ok := byClaim[claim]; ok {
			class, capacity = storageClassCell(paint, pvc), pvcCapacity(pvc)
		}
		t.Row(p.Namespace, p.Name, p.Spec.NodeName, claim.Name, class, capacity)
	}
	t.SortBy(f.Sort)
	return t.Flush()
}
