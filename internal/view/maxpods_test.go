package view

import (
	"bytes"
	"context"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/PixiBixi/kubectl-klens/internal/kube"
)

func nodeMaxPods(name string, max int64) *corev1.Node {
	return &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Status: corev1.NodeStatus{
			Allocatable: corev1.ResourceList{
				corev1.ResourcePods: *resource.NewQuantity(max, resource.DecimalSI),
			},
		},
	}
}

func TestMaxPods(t *testing.T) {
	c := fake.NewClientset(
		nodeMaxPods("n1", 110),
		pod("a", "default", "n1"),
		pod("b", "kube-system", "n1"),
	)
	var buf bytes.Buffer
	if err := MaxPods(context.Background(), c, kube.Flags{}, nil, &buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	// n1: max 110, 2 pods used (counted across namespaces), 108 free.
	for _, want := range []string{"NODE", "MAXPODS", "USED", "FREE", "n1", "110", "108"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q:\n%s", want, out)
		}
	}
}

func TestMaxPodsUnknownAllocatable(t *testing.T) {
	c := fake.NewClientset(&corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "n1"}})
	var buf bytes.Buffer
	if err := MaxPods(context.Background(), c, kube.Flags{}, nil, &buf); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "none") {
		t.Fatalf("want 'none' when allocatable pods is unset:\n%s", buf.String())
	}
}
