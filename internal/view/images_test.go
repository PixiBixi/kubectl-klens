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

func podImg(name, image string) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
		Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "c", Image: image}}},
	}
}

func TestImages(t *testing.T) {
	c := fake.NewClientset(
		podImg("a", "nginx:1.25"),
		podImg("b", "nginx:1.25"),
		podImg("c", "redis:7"),
	)
	var buf bytes.Buffer
	if err := Images(context.Background(), c, kube.Flags{}, nil, &buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "nginx:1.25") || !strings.Contains(out, "redis:7") {
		t.Fatalf("missing images:\n%s", out)
	}
	// nginx (2) must sort before redis (1).
	if strings.Index(out, "nginx:1.25") > strings.Index(out, "redis:7") {
		t.Fatalf("nginx (2) should sort before redis (1):\n%s", out)
	}
}
