package view

import (
	"context"
	"io"

	corev1 "k8s.io/api/core/v1"

	"github.com/PixiBixi/kubectl-klens/internal/kube"
)

// Capacity shows CPU/memory capacity and allocatable per node.
func Capacity(ctx context.Context, c kube.Clients, f kube.Flags, args []string, out io.Writer) error {
	return nodeTable(ctx, c, f, out, []string{"NAME", "CPU_CAP", "CPU_ALLOC", "MEM_CAP", "MEM_ALLOC"}, func(paint kube.Painter, n *corev1.Node) []string {
		return appendCPUMem([]string{n.Name}, paint, n.Status.Capacity, n.Status.Allocatable)
	})
}
