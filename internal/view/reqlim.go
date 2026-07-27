package view

import (
	"context"
	"io"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/PixiBixi/kubectl-klens/internal/kube"
)

// Reqlim shows per-container requests/limits, including init and ephemeral
// containers: an init container's requests count toward the pod's effective
// scheduling footprint and toward ResourceQuota, so leaving them out
// understated the real cost. kube-system is excluded from the -A view only.
func Reqlim(ctx context.Context, c kubernetes.Interface, f kube.Flags, args []string, out io.Writer) error {
	pods, err := c.CoreV1().Pods(f.NamespaceScope()).List(ctx, metav1.ListOptions{})
	if err != nil {
		return err
	}
	paint := kube.NewPainter(f)
	t := kube.NewTable(out, paint, "NS", "POD", "CONTAINER", "KIND", "REQ_CPU", "LIM_CPU", "REQ_MEM", "LIM_MEM")
	for i := range pods.Items {
		p := &pods.Items[i]
		if skipNamespace(f, p.Namespace) {
			continue
		}
		for _, pc := range podContainers(p) {
			req, lim := pc.Spec.Resources.Requests, pc.Spec.Resources.Limits
			t.Row(
				p.Namespace, p.Name, pc.Spec.Name, pc.Kind,
				qtyOrNone(paint, req, corev1.ResourceCPU),
				qtyOrNone(paint, lim, corev1.ResourceCPU),
				qtyOrNone(paint, req, corev1.ResourceMemory),
				qtyOrNone(paint, lim, corev1.ResourceMemory),
			)
		}
	}
	t.SortBy(f.Sort)
	return t.Flush()
}
