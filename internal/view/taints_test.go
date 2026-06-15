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

func TestTaints(t *testing.T) {
	tainted := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "n1"},
		Spec: corev1.NodeSpec{Taints: []corev1.Taint{
			{Key: "dedicated", Value: "gpu", Effect: corev1.TaintEffectNoSchedule},
		}},
	}
	clean := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "n2"}}
	c := fake.NewClientset(tainted, clean)
	var buf bytes.Buffer
	if err := Taints(context.Background(), c, kube.Flags{}, nil, &buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "dedicated=gpu:NoSchedule") {
		t.Fatalf("missing taint:\n%s", out)
	}
	if !strings.Contains(out, "<none>") {
		t.Fatalf("clean node should show <none>:\n%s", out)
	}
}
