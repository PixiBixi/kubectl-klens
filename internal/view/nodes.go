package view

import (
	"context"
	"io"

	corev1 "k8s.io/api/core/v1"

	"github.com/PixiBixi/kubectl-klens/internal/kube"
)

// Nodes lists nodes with cross-cloud pool, compute-class, and
// provisioning-model (spot/on-demand) labels.
func Nodes(ctx context.Context, c kube.Clients, f kube.Flags, args []string, out io.Writer) error {
	headers := []string{"NAME", "STATUS", "NODEPOOL", "INSTANCE-TYPE", "CLASS", "PROVISIONING"}
	return nodeTable(ctx, c, f, out, headers, func(paint kube.Painter, n *corev1.Node) []string {
		return []string{
			n.Name,
			paint.Status(nodeStatus(n)),
			kube.Label(paint, n.Labels, nodePoolLabels...),
			kube.Label(paint, n.Labels, corev1.LabelInstanceTypeStable),
			nodeClass(paint, n.Labels),
			paintProvisioning(paint, nodeProvisioning(n.Labels)),
		}
	})
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
