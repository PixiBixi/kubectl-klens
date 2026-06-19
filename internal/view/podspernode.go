package view

import (
	"context"
	"io"
	"sort"
	"strconv"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/PixiBixi/kubectl-klens/internal/kube"
)

// PodsPerNode counts pods grouped by node, sorted by count descending.
func PodsPerNode(ctx context.Context, c kubernetes.Interface, f kube.Flags, args []string, out io.Writer) error {
	pods, err := c.CoreV1().Pods(f.NamespaceScope()).List(ctx, metav1.ListOptions{})
	if err != nil {
		return err
	}
	counts := map[string]int{}
	for _, p := range pods.Items {
		node := p.Spec.NodeName
		if node == "" {
			node = "<unscheduled>"
		}
		counts[node]++
	}
	type entry struct {
		node string
		n    int
	}
	list := make([]entry, 0, len(counts))
	for node, n := range counts {
		list = append(list, entry{node, n})
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].n != list[j].n {
			return list[i].n > list[j].n
		}
		return list[i].node < list[j].node
	})
	paint := kube.NewPainter(f)
	t := kube.NewTable(out, paint, "NODE", "PODS")
	for _, e := range list {
		t.Row(e.node, strconv.Itoa(e.n))
	}
	t.SortBy(f.Sort)
	return t.Flush()
}
