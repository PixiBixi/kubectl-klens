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

func dbCreds() *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "db-creds", Namespace: "default"},
		Type:       corev1.SecretTypeOpaque,
		Data: map[string][]byte{
			"username": []byte("admin"),
			"password": []byte("s3cr3t"),
		},
	}
}

func TestSecretListsWhenNoName(t *testing.T) {
	c := fake.NewClientset(
		dbCreds(),
		&corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "tls", Namespace: "default"}, Type: corev1.SecretTypeTLS},
	)
	var buf bytes.Buffer
	if err := Secret(context.Background(), c, kube.Flags{Namespace: "default"}, nil, &buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{"NAME", "TYPE", "KEYS", "AGE", "db-creds", "tls", "Opaque"} {
		if !strings.Contains(out, want) {
			t.Errorf("listing missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "s3cr3t") {
		t.Errorf("listing must not reveal values:\n%s", out)
	}
}

func TestSecretListsKeys(t *testing.T) {
	c := fake.NewClientset(dbCreds())
	var buf bytes.Buffer
	if err := Secret(context.Background(), c, kube.Flags{Namespace: "default"}, []string{"db-creds"}, &buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{"KEY", "BYTES", "username", "password"} {
		if !strings.Contains(out, want) {
			t.Errorf("keys listing missing %q:\n%s", want, out)
		}
	}
	for _, leak := range []string{"admin", "s3cr3t"} {
		if strings.Contains(out, leak) {
			t.Errorf("keys listing must not reveal value %q:\n%s", leak, out)
		}
	}
}

func TestSecretShowsOneKey(t *testing.T) {
	c := fake.NewClientset(dbCreds())
	var buf bytes.Buffer
	if err := Secret(context.Background(), c, kube.Flags{Namespace: "default"}, []string{"db-creds", "username"}, &buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "admin") {
		t.Errorf("expected value 'admin':\n%s", out)
	}
	if strings.Contains(out, "s3cr3t") {
		t.Errorf("must not reveal other keys' values:\n%s", out)
	}
}

func TestSecretAllDumpsValues(t *testing.T) {
	c := fake.NewClientset(dbCreds())
	var buf bytes.Buffer
	if err := Secret(context.Background(), c, kube.Flags{Namespace: "default"}, []string{"db-creds", "all"}, &buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{"username", "admin", "password", "s3cr3t"} {
		if !strings.Contains(out, want) {
			t.Errorf("dump missing %q:\n%s", want, out)
		}
	}
}

func TestSecretKeyNotFound(t *testing.T) {
	c := fake.NewClientset(dbCreds())
	var buf bytes.Buffer
	err := Secret(context.Background(), c, kube.Flags{Namespace: "default"}, []string{"db-creds", "nope"}, &buf)
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("expected not-found error, got %v", err)
	}
}
