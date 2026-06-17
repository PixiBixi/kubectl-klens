package view

import (
	"context"
	"io"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/PixiBixi/kubectl-klens/internal/kube"
)

// Capacity shows CPU/memory capacity and allocatable per node.
func Capacity(ctx context.Context, c kubernetes.Interface, f kube.Flags, args []string, out io.Writer) error {
	nodes, err := c.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return err
	}
	t := kube.NewTable(out, "NAME", "CPU_CAP", "CPU_ALLOC", "MEM_CAP", "MEM_ALLOC")
	for _, n := range nodes.Items {
		cap, alloc := n.Status.Capacity, n.Status.Allocatable
		t.Row(
			n.Name,
			qtyOrNone(cap, corev1.ResourceCPU),
			qtyOrNone(alloc, corev1.ResourceCPU),
			qtyOrNone(cap, corev1.ResourceMemory),
			qtyOrNone(alloc, corev1.ResourceMemory),
		)
	}
	t.SortBy(f.Sort)
	return t.Flush()
}
