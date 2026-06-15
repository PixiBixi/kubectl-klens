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

func pod(name, ns, node string) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
		Spec:       corev1.PodSpec{NodeName: node},
	}
}

func TestPodsPerNode(t *testing.T) {
	c := fake.NewClientset(
		pod("a", "default", "n1"),
		pod("b", "default", "n1"),
		pod("c", "default", "n2"),
	)
	var buf bytes.Buffer
	if err := PodsPerNode(context.Background(), c, kube.Flags{}, nil, &buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	// n1 has 2 pods and must be listed before n2 (sorted desc by count).
	if !strings.Contains(out, "n1") || !strings.Contains(out, "2") {
		t.Fatalf("missing n1 count:\n%s", out)
	}
	if strings.Index(out, "n1") > strings.Index(out, "n2") {
		t.Fatalf("n1 (2 pods) should sort before n2 (1):\n%s", out)
	}
}
