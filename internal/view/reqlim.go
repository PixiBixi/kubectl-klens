package view

import (
	"context"
	"io"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/PixiBixi/kubectl-klens/internal/kube"
)

// Reqlim shows per-container requests/limits for all pods except kube-system.
func Reqlim(ctx context.Context, c kubernetes.Interface, f kube.Flags, args []string, out io.Writer) error {
	pods, err := c.CoreV1().Pods(f.NamespaceScope()).List(ctx, metav1.ListOptions{})
	if err != nil {
		return err
	}
	t := kube.NewTable(out, "NS", "POD", "CONTAINER", "REQ_CPU", "LIM_CPU", "REQ_MEM", "LIM_MEM")
	for _, p := range pods.Items {
		if p.Namespace == "kube-system" {
			continue
		}
		for _, ctr := range p.Spec.Containers {
			req, lim := ctr.Resources.Requests, ctr.Resources.Limits
			t.Row(
				p.Namespace, p.Name, ctr.Name,
				qtyOrNone(req, corev1.ResourceCPU),
				qtyOrNone(lim, corev1.ResourceCPU),
				qtyOrNone(req, corev1.ResourceMemory),
				qtyOrNone(lim, corev1.ResourceMemory),
			)
		}
	}
	t.SortBy(f.Sort)
	return t.Flush()
}
