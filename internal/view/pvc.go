package view

import (
	"context"
	"io"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

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
	ns := f.NamespaceScope()
	err := allLists(
		func() (err error) {
			pods, err = kube.ListPods(ctx, c, ns, metav1.ListOptions{})
			return err
		},
		func() (err error) {
			pvcs, err = kube.ListPersistentVolumeClaims(ctx, c, ns, metav1.ListOptions{})
			return err
		},
	)
	if err != nil {
		return err
	}
	byClaim := make(map[string]*corev1.PersistentVolumeClaim, len(pvcs))
	for i := range pvcs {
		byClaim[pvcs[i].Namespace+"/"+pvcs[i].Name] = &pvcs[i]
	}

	paint := kube.NewPainter(f)
	t := kube.NewTable(out, paint, "NS", "POD", "NODE", "PVC", "CLASS", "CAPACITY")
	for i := range pods {
		p := &pods[i]
		for j := range p.Spec.Volumes {
			vol := &p.Spec.Volumes[j]
			if vol.PersistentVolumeClaim == nil {
				continue
			}
			// The pod may reference a claim that does not exist: it will never start,
			// but it is still worth listing, so both cells fall back to a dash.
			class, capacity := paint.Muted("-"), paint.Muted("-")
			if pvc, ok := byClaim[p.Namespace+"/"+vol.PersistentVolumeClaim.ClaimName]; ok {
				class, capacity = storageClassCell(paint, pvc), pvcCapacity(pvc)
			}
			t.Row(p.Namespace, p.Name, p.Spec.NodeName, vol.PersistentVolumeClaim.ClaimName, class, capacity)
		}
	}
	t.SortBy(f.Sort)
	return t.Flush()
}
