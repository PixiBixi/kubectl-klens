package view

import (
	corev1 "k8s.io/api/core/v1"

	"github.com/PixiBixi/kubectl-klens/internal/kube"
)

// nodeStatus returns Ready / NotReady / Unknown from a node's conditions.
func nodeStatus(n corev1.Node) string {
	for _, cond := range n.Status.Conditions {
		if cond.Type == corev1.NodeReady {
			if cond.Status == corev1.ConditionTrue {
				return "Ready"
			}
			return "NotReady"
		}
	}
	return "Unknown"
}

// qtyOrNone returns the string form of a resource quantity, or a muted "none"
// if unset.
func qtyOrNone(paint kube.Painter, rl corev1.ResourceList, name corev1.ResourceName) string {
	if q, ok := rl[name]; ok {
		return q.String()
	}
	return paint.Muted("none")
}
