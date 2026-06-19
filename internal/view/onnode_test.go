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

func TestOnNodeRequiresArg(t *testing.T) {
	c := fake.NewClientset()
	var buf bytes.Buffer
	err := OnNode(context.Background(), c, kube.Flags{}, nil, &buf)
	if err == nil || !strings.Contains(err.Error(), "requires a node") {
		t.Fatalf("expected node-required error, got %v", err)
	}
}

func TestOnNodeFilters(t *testing.T) {
	c := fake.NewClientset(
		pod("a", "default", "n1"),
		pod("b", "default", "n2"),
	)
	var buf bytes.Buffer
	if err := OnNode(context.Background(), c, kube.Flags{}, []string{"n1"}, &buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "a") {
		t.Fatalf("pod a (on n1) should be listed:\n%s", out)
	}
	if strings.Contains(out, "\nb\t") || strings.Contains(out, " b ") {
		t.Fatalf("pod b (on n2) must not be listed:\n%s", out)
	}
}

func TestOnNodeColor(t *testing.T) {
	running := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "p1", Namespace: "ns"},
		Spec:       corev1.PodSpec{NodeName: "node-a"},
		Status:     corev1.PodStatus{Phase: corev1.PodRunning},
	}
	c := fake.NewClientset(running)
	var buf bytes.Buffer
	if err := OnNode(context.Background(), c, kube.Flags{Color: true, AllNamespaces: true}, []string{"node-a"}, &buf); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "\x1b[32mRunning\x1b[0m") {
		t.Fatalf("Running phase not green:\n%s", buf.String())
	}
}
