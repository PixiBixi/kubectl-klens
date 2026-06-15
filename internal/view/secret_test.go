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

func TestSecretRequiresArg(t *testing.T) {
	c := fake.NewClientset()
	var buf bytes.Buffer
	err := Secret(context.Background(), c, kube.Flags{}, nil, &buf)
	if err == nil || !strings.Contains(err.Error(), "requires a name") {
		t.Fatalf("expected name-required error, got %v", err)
	}
}

func TestSecretDecodes(t *testing.T) {
	c := fake.NewClientset(
		&corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: "db-creds", Namespace: "default"},
			Data: map[string][]byte{
				"username": []byte("admin"),
				"password": []byte("s3cr3t"),
			},
		},
	)
	var buf bytes.Buffer
	if err := Secret(context.Background(), c, kube.Flags{Namespace: "default"}, []string{"db-creds"}, &buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "admin") {
		t.Fatalf("expected 'admin' in output:\n%s", out)
	}
	if !strings.Contains(out, "s3cr3t") {
		t.Fatalf("expected 's3cr3t' in output:\n%s", out)
	}
	if !strings.Contains(out, "username") {
		t.Fatalf("expected key 'username' in output:\n%s", out)
	}
	if !strings.Contains(out, "password") {
		t.Fatalf("expected key 'password' in output:\n%s", out)
	}
}
