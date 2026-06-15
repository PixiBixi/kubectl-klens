package view

import (
	"context"
	"fmt"
	"io"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/PixiBixi/kubectl-klens/internal/kube"
)

// SvcFQDN lists services and prints their in-cluster FQDN.
func SvcFQDN(ctx context.Context, c kubernetes.Interface, f kube.Flags, args []string, out io.Writer) error {
	services, err := c.CoreV1().Services(f.NamespaceScope()).List(ctx, metav1.ListOptions{})
	if err != nil {
		return err
	}
	t := kube.NewTable(out, "NS", "SERVICE", "FQDN")
	for _, s := range services.Items {
		fqdn := fmt.Sprintf("%s.%s.svc.cluster.local", s.Name, s.Namespace)
		t.Row(s.Namespace, s.Name, fqdn)
	}
	return t.Flush()
}
