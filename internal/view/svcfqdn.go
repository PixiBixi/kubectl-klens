package view

import (
	"context"
	"fmt"
	"io"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/PixiBixi/kubectl-klens/internal/kube"
)

// SvcFQDN lists services and prints their in-cluster FQDN.
func SvcFQDN(ctx context.Context, c kube.Clients, f kube.Flags, args []string, out io.Writer) error {
	services, err := kube.ListServices(ctx, c, f.NamespaceScope(), metav1.ListOptions{})
	if err != nil {
		return err
	}
	paint := kube.NewPainter(f)
	t := kube.NewTable(out, paint, "NS", "SERVICE", "FQDN")
	for i := range services {
		s := &services[i]
		fqdn := fmt.Sprintf("%s.%s.svc.cluster.local", s.Name, s.Namespace)
		t.Row(s.Namespace, s.Name, fqdn)
	}
	t.SortBy(f.Sort)
	return t.Flush()
}
