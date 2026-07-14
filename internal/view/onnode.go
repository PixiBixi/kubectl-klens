package view

import (
	"context"
	"errors"
	"io"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/client-go/kubernetes"

	"github.com/PixiBixi/kubectl-klens/internal/kube"
)

// OnNode lists pods scheduled on the given node.
func OnNode(ctx context.Context, c kubernetes.Interface, f kube.Flags, args []string, out io.Writer) error {
	if len(args) < 1 || args[0] == "" {
		return errors.New("on-node requires a node name: kubectl klens on-node <node>")
	}
	node := args[0]
	pods, err := c.CoreV1().Pods(f.NamespaceScope()).List(ctx, metav1.ListOptions{
		FieldSelector: fields.OneTermEqualSelector("spec.nodeName", node).String(),
	})
	if err != nil {
		return err
	}
	paint := kube.NewPainter(f)
	t := kube.NewTable(out, paint, "NS", "POD", "STATUS", "NODE")
	for _, p := range pods.Items {
		if p.Spec.NodeName != node {
			continue // defensive: fake clientset ignores FieldSelector
		}
		t.Row(p.Namespace, p.Name, paint.Status(string(p.Status.Phase)), p.Spec.NodeName)
	}
	t.SortBy(f.Sort)
	return t.Flush()
}
