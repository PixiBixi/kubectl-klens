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

// Images counts container image occurrences across pods, sorted desc.
func Images(ctx context.Context, c kubernetes.Interface, f kube.Flags, args []string, out io.Writer) error {
	pods, err := c.CoreV1().Pods(f.NamespaceScope()).List(ctx, metav1.ListOptions{})
	if err != nil {
		return err
	}
	counts := map[string]int{}
	for _, p := range pods.Items {
		for _, ctr := range p.Spec.Containers {
			counts[ctr.Image]++
		}
	}
	type entry struct {
		image string
		n     int
	}
	list := make([]entry, 0, len(counts))
	for image, n := range counts {
		list = append(list, entry{image, n})
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].n != list[j].n {
			return list[i].n > list[j].n
		}
		return list[i].image < list[j].image
	})
	t := kube.NewTable(out, "COUNT", "IMAGE")
	for _, e := range list {
		t.Row(strconv.Itoa(e.n), e.image)
	}
	return t.Flush()
}
