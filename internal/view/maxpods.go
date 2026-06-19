package view

import (
	"context"
	"io"
	"strconv"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/PixiBixi/kubectl-klens/internal/kube"
)

// MaxPods shows each node's pod ceiling (allocatable pods) next to the current
// pod count and the remaining free slots. Pods are counted cluster-wide since
// node saturation is independent of namespace.
func MaxPods(ctx context.Context, c kubernetes.Interface, f kube.Flags, args []string, out io.Writer) error {
	nodes, err := c.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return err
	}
	pods, err := c.CoreV1().Pods("").List(ctx, metav1.ListOptions{})
	if err != nil {
		return err
	}
	used := map[string]int{}
	for _, p := range pods.Items {
		used[p.Spec.NodeName]++
	}
	paint := kube.NewPainter(f)
	t := kube.NewTable(out, paint, "NODE", "MAXPODS", "USED", "FREE")
	for _, n := range nodes.Items {
		u := used[n.Name]
		maxCell, freeCell := paint.Muted("none"), paint.Muted("none")
		if q, ok := n.Status.Allocatable[corev1.ResourcePods]; ok {
			max := int(q.Value())
			maxCell = strconv.Itoa(max)
			freeCell = freeSlots(paint, max-u, max)
		}
		t.Row(n.Name, maxCell, strconv.Itoa(u), freeCell)
	}
	t.SortBy(f.Sort)
	return t.Flush()
}

// freeSlots colors a node's remaining pod slots by how much headroom is left:
// under 10% of the ceiling is bad, under 25% is a warning, otherwise healthy.
func freeSlots(paint kube.Painter, free, max int) string {
	s := strconv.Itoa(free)
	switch {
	case max <= 0:
		return s
	case free*10 < max:
		return paint.Bad(s)
	case free*4 < max:
		return paint.Warn(s)
	default:
		return paint.OK(s)
	}
}
