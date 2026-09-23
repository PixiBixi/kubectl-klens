package view

import (
	"context"
	"fmt"
	"io"
	"strings"

	corev1 "k8s.io/api/core/v1"

	"github.com/PixiBixi/kubectl-klens/internal/kube"
)

// Taints lists each node's taints as key=value:effect, comma-joined.
func Taints(ctx context.Context, c kube.Clients, f kube.Flags, args []string, out io.Writer) error {
	return nodeTable(ctx, c, f, out, []string{"NAME", "TAINTS"}, func(paint kube.Painter, n *corev1.Node) []string {
		if len(n.Spec.Taints) == 0 {
			return []string{n.Name, paint.Muted("<none>")}
		}
		ts := make([]string, len(n.Spec.Taints))
		for i, taint := range n.Spec.Taints {
			ts[i] = fmt.Sprintf("%s=%s:%s", taint.Key, taint.Value, taintEffect(paint, string(taint.Effect)))
		}
		return []string{n.Name, strings.Join(ts, ",")}
	})
}

// taintEffect colors a taint's effect by how aggressively it repels pods:
// NoExecute (evicts running pods) is bad, NoSchedule is a warning, and the soft
// PreferNoSchedule is muted.
func taintEffect(paint kube.Painter, effect string) string {
	switch effect {
	case "NoExecute":
		return paint.Bad(effect)
	case "NoSchedule":
		return paint.Warn(effect)
	case "PreferNoSchedule":
		return paint.Muted(effect)
	}
	return effect
}
