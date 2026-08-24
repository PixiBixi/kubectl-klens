package view

import (
	"context"
	"io"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/PixiBixi/kubectl-klens/internal/kube"
)

// Nodes lists nodes with their GKE nodepool and instance-type labels.
func Nodes(ctx context.Context, c kube.Clients, f kube.Flags, args []string, out io.Writer) error {
	nodes, err := kube.ListNodes(ctx, c, metav1.ListOptions{})
	if err != nil {
		return err
	}
	paint := kube.NewPainter(f)
	t := kube.NewTable(out, paint, "NAME", "STATUS", "NODEPOOL", "INSTANCE-TYPE")
	for i := range nodes {
		n := &nodes[i]
		t.Row(
			n.Name,
			paint.Status(nodeStatus(n)),
			kube.Label(paint, n.Labels, "cloud.google.com/gke-nodepool"),
			kube.Label(paint, n.Labels, "node.kubernetes.io/instance-type"),
		)
	}
	t.SortBy(f.Sort)
	return t.Flush()
}
