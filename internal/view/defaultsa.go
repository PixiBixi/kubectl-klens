package view

import (
	"context"
	"io"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/PixiBixi/kubectl-klens/internal/kube"
)

// DefaultSA lists pods whose serviceAccountName is "default".
func DefaultSA(ctx context.Context, c kubernetes.Interface, f kube.Flags, args []string, out io.Writer) error {
	pods, err := c.CoreV1().Pods(f.NamespaceScope()).List(ctx, metav1.ListOptions{})
	if err != nil {
		return err
	}
	t := kube.NewTable(out, "NS", "POD")
	for _, p := range pods.Items {
		if p.Spec.ServiceAccountName != "default" {
			continue
		}
		t.Row(p.Namespace, p.Name)
	}
	t.SortBy(f.Sort)
	return t.Flush()
}
