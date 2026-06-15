package view

import (
	corev1 "k8s.io/api/core/v1"
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

// qtyOrNone returns the string form of a resource quantity, or "none" if unset.
func qtyOrNone(rl corev1.ResourceList, name corev1.ResourceName) string {
	if q, ok := rl[name]; ok {
		return q.String()
	}
	return "none"
}
