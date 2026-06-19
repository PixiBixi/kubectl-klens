package view

import (
	"context"
	"io"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/PixiBixi/kubectl-klens/internal/kube"
)

// Nodes lists nodes with their GKE nodepool and instance-type labels.
func Nodes(ctx context.Context, c kubernetes.Interface, f kube.Flags, args []string, out io.Writer) error {
	nodes, err := c.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return err
	}
	paint := kube.NewPainter(f)
	t := kube.NewTable(out, paint, "NAME", "STATUS", "NODEPOOL", "INSTANCE-TYPE")
	for _, n := range nodes.Items {
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
