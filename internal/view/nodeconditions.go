package view

import (
	"context"
	"io"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/PixiBixi/kubectl-klens/internal/kube"
)

// NodeConditions shows each node's readiness and its pressure conditions, where
// a "True" memory/disk/pid column flags a node under that pressure.
func NodeConditions(ctx context.Context, c kubernetes.Interface, f kube.Flags, args []string, out io.Writer) error {
	nodes, err := c.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return err
	}
	paint := kube.NewPainter(f)
	t := kube.NewTable(out, paint, "NAME", "STATUS", "MEMORY", "DISK", "PID")
	for _, n := range nodes.Items {
		t.Row(
			n.Name,
			paint.Status(nodeStatus(n)),
			pressure(paint, conditionStatus(n, corev1.NodeMemoryPressure)),
			pressure(paint, conditionStatus(n, corev1.NodeDiskPressure)),
			pressure(paint, conditionStatus(n, corev1.NodePIDPressure)),
		)
	}
	t.SortBy(f.Sort)
	return t.Flush()
}

// conditionStatus returns the status (True/False/Unknown) of a node condition,
// or "Unknown" when the node does not report it.
func conditionStatus(n corev1.Node, condType corev1.NodeConditionType) string {
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
