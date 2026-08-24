package view

import (
	"context"
	"io"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"

	"github.com/PixiBixi/kubectl-klens/internal/kube"
)

// DefaultSA lists pods whose serviceAccountName is "default". The match is
// pushed down to the apiserver rather than filtered here, so a cluster-wide scan
// transfers only the offending pods.
func DefaultSA(ctx context.Context, c kube.Clients, f kube.Flags, args []string, out io.Writer) error {
	pods, err := kube.ListPods(ctx, c, f.NamespaceScope(), metav1.ListOptions{
		FieldSelector: fields.OneTermEqualSelector("spec.serviceAccountName", "default").String(),
	})
	if err != nil {
		return err
	}
	paint := kube.NewPainter(f)
	t := kube.NewTable(out, paint, "NS", "POD")
	for i := range pods {
		p := &pods[i]
		t.Row(p.Namespace, p.Name)
	}
	t.SortBy(f.Sort)
	return t.Flush()
}
