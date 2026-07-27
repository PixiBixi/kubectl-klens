package view

import (
	"context"
	"io"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/PixiBixi/kubectl-klens/internal/kube"
)

// Pvc lists PVCs bound to a pod together with the pod's node.
func Pvc(ctx context.Context, c kubernetes.Interface, f kube.Flags, args []string, out io.Writer) error {
	pods, err := kube.ListPods(ctx, c, f.NamespaceScope(), metav1.ListOptions{})
	if err != nil {
		return err
	}
	paint := kube.NewPainter(f)
	t := kube.NewTable(out, paint, "NS", "POD", "NODE", "PVC")
	for i := range pods {
		p := &pods[i]
		for i := range p.Spec.Volumes {
			vol := &p.Spec.Volumes[i]
			if vol.PersistentVolumeClaim != nil {
				t.Row(p.Namespace, p.Name, p.Spec.NodeName, vol.PersistentVolumeClaim.ClaimName)
			}
		}
	}
	t.SortBy(f.Sort)
	return t.Flush()
}
