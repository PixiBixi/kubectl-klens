package view

import (
	"context"
	"io"

	corev1 "k8s.io/api/core/v1"

	"github.com/PixiBixi/kubectl-klens/internal/kube"
)

// Zones shows the region and zone topology labels per node.
func Zones(ctx context.Context, c kube.Clients, f kube.Flags, args []string, out io.Writer) error {
	return nodeTable(ctx, c, f, out, []string{"NAME", "REGION", "ZONE"}, func(paint kube.Painter, n *corev1.Node) []string {
		return []string{n.Name, kube.Label(paint, n.Labels, corev1.LabelTopologyRegion), kube.Label(paint, n.Labels, corev1.LabelTopologyZone)}
	})
}
