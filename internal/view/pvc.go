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
	pods, err := c.CoreV1().Pods(f.NamespaceScope()).List(ctx, metav1.ListOptions{})
	if err != nil {
		return err
	}
	paint := kube.NewPainter(f)
	t := kube.NewTable(out, paint, "NS", "POD", "NODE", "PVC")
	for _, p := range pods.Items {
		for _, vol := range p.Spec.Volumes {
			if vol.PersistentVolumeClaim != nil {
				t.Row(p.Namespace, p.Name, p.Spec.NodeName, vol.PersistentVolumeClaim.ClaimName)
			}
		}
	}
	t.SortBy(f.Sort)
	return t.Flush()
}
