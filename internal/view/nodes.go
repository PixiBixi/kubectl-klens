package view

import (
	"context"
	"io"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/PixiBixi/kubectl-klens/internal/kube"
)

// Nodes lists nodes with cross-cloud pool, compute-class, and
// provisioning-model (spot/on-demand) labels.
func Nodes(ctx context.Context, c kube.Clients, f kube.Flags, args []string, out io.Writer) error {
	nodes, err := kube.ListNodes(ctx, c, metav1.ListOptions{})
	if err != nil {
		return err
	}
	paint := kube.NewPainter(f)
	t := kube.NewTable(out, paint, "NAME", "STATUS", "NODEPOOL", "INSTANCE-TYPE", "CLASS", "PROVISIONING")
	for i := range nodes {
		n := &nodes[i]
		t.Row(
			n.Name,
			paint.Status(nodeStatus(n)),
			firstLabel(paint, n.Labels, nodePoolLabels),
			kube.Label(paint, n.Labels, "node.kubernetes.io/instance-type"),
			nodeClass(paint, n.Labels),
			paintProvisioning(paint, nodeProvisioning(n.Labels)),
		)
	}
	t.SortBy(f.Sort)
	return t.Flush()
}

// paintProvisioning colors the common on-demand state green and the reclaimable
// spot/preemptible states yellow, per the "color the healthy state too, not
// only the anomaly" preference. The other genuine values (capacity-block,
// reserved) print unstyled: neither clearly healthy nor clearly at-risk.
func paintProvisioning(paint kube.Painter, provisioning string) string {
	switch provisioning {
	case "":
		return paint.Muted("<none>")
	case "on-demand":
		return paint.OK(provisioning)
	case "spot", "preemptible":
		return paint.Warn(provisioning)
	default:
		return provisioning
	}
}
