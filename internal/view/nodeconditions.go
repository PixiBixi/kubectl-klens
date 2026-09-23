package view

import (
	"context"
	"io"

	corev1 "k8s.io/api/core/v1"

	"github.com/PixiBixi/kubectl-klens/internal/kube"
)

// NodeConditions shows each node's readiness and its pressure conditions, where
// a "True" memory/disk/pid column flags a node under that pressure.
func NodeConditions(ctx context.Context, c kube.Clients, f kube.Flags, args []string, out io.Writer) error {
	return nodeTable(ctx, c, f, out, []string{"NAME", "STATUS", "MEMORY", "DISK", "PID"}, func(paint kube.Painter, n *corev1.Node) []string {
		return []string{
			n.Name,
			paint.Status(nodeStatus(n)),
			pressure(paint, conditionStatus(n, corev1.NodeMemoryPressure)),
			pressure(paint, conditionStatus(n, corev1.NodeDiskPressure)),
			pressure(paint, conditionStatus(n, corev1.NodePIDPressure)),
		}
	})
}

// conditionStatus returns the status (True/False/Unknown) of a node condition,
// or "Unknown" when the node does not report it.
func conditionStatus(n *corev1.Node, condType corev1.NodeConditionType) string {
	for _, cond := range n.Status.Conditions {
		if cond.Type == condType {
			return string(cond.Status)
		}
	}
	return "Unknown"
}

// pressure colors a node pressure condition: under pressure (True) is bad,
// no pressure (False) is muted, anything else (Unknown) is left plain.
func pressure(paint kube.Painter, status string) string {
	switch status {
	case "True":
		return paint.Bad(status)
	case "False":
		return paint.Muted(status)
	}
	return status
}
