package view

import (
	"context"
	"io"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/PixiBixi/kubectl-klens/internal/kube"
)

// Zones shows the region and zone topology labels per node.
func Zones(ctx context.Context, c kube.Clients, f kube.Flags, args []string, out io.Writer) error {
	nodes, err := kube.ListNodes(ctx, c, metav1.ListOptions{})
	if err != nil {
		return err
	}
	paint := kube.NewPainter(f)
	t := kube.NewTable(out, paint, "NAME", "REGION", "ZONE")
	for i := range nodes {
		n := &nodes[i]
		t.Row(
			n.Name,
			kube.Label(paint, n.Labels, "topology.kubernetes.io/region"),
			kube.Label(paint, n.Labels, "topology.kubernetes.io/zone"),
		)
	}
	t.SortBy(f.Sort)
	return t.Flush()
}
