package view

import (
	"context"
	"fmt"
	"io"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/PixiBixi/kubectl-klens/internal/kube"
)

// Taints lists each node's taints as key=value:effect, comma-joined.
func Taints(ctx context.Context, c kubernetes.Interface, f kube.Flags, args []string, out io.Writer) error {
	nodes, err := c.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return err
	}
	t := kube.NewTable(out, "NAME", "TAINTS")
	for _, n := range nodes.Items {
		var ts []string
		for _, taint := range n.Spec.Taints {
			ts = append(ts, fmt.Sprintf("%s=%s:%s", taint.Key, taint.Value, taint.Effect))
		}
		val := strings.Join(ts, ",")
		if val == "" {
			val = "<none>"
		}
		t.Row(n.Name, val)
	}
	t.SortBy(f.Sort)
	return t.Flush()
}
