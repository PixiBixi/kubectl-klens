package view

import (
	"context"
	"io"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/PixiBixi/kubectl-klens/internal/kube"
)

// Zones shows the region and zone topology labels per node.
func Zones(ctx context.Context, c kubernetes.Interface, f kube.Flags, args []string, out io.Writer) error {
	nodes, err := c.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return err
	}
	t := kube.NewTable(out, "NAME", "REGION", "ZONE")
	for _, n := range nodes.Items {
		t.Row(
			n.Name,
			kube.Label(n.Labels, "topology.kubernetes.io/region"),
			kube.Label(n.Labels, "topology.kubernetes.io/zone"),
		)
	}
	return t.Flush()
}
