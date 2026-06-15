package view

import (
	"bytes"
	"context"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/PixiBixi/kubectl-klens/internal/kube"
)

func TestSvcFQDN(t *testing.T) {
	c := fake.NewClientset(
		&corev1.Service{
			ObjectMeta: metav1.ObjectMeta{Name: "api", Namespace: "team-a"},
		},
	)
	var buf bytes.Buffer
	if err := SvcFQDN(context.Background(), c, kube.Flags{}, nil, &buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "api.team-a.svc.cluster.local") {
		t.Fatalf("expected FQDN api.team-a.svc.cluster.local in output:\n%s", out)
	}
}
