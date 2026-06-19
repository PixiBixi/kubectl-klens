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

func TestNodes(t *testing.T) {
	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "gke-pool-1-abc",
			Labels: map[string]string{
				"cloud.google.com/gke-nodepool":    "pool-1",
				"node.kubernetes.io/instance-type": "e2-standard-4",
			},
		},
		Status: corev1.NodeStatus{
			Conditions: []corev1.NodeCondition{{Type: corev1.NodeReady, Status: corev1.ConditionTrue}},
		},
	}
	c := fake.NewClientset(node)
	var buf bytes.Buffer
	if err := Nodes(context.Background(), c, kube.Flags{}, nil, &buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{"NAME", "gke-pool-1-abc", "Ready", "pool-1", "e2-standard-4"} {
		if !strings.Contains(out, want) {
			t.Fatalf("output missing %q:\n%s", want, out)
		}
	}
}

func TestNodesColor(t *testing.T) {
	ready := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "ok"},
		Status:     corev1.NodeStatus{Conditions: []corev1.NodeCondition{{Type: corev1.NodeReady, Status: corev1.ConditionTrue}}},
	}
	down := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "down"},
		Status:     corev1.NodeStatus{Conditions: []corev1.NodeCondition{{Type: corev1.NodeReady, Status: corev1.ConditionFalse}}},
	}
	c := fake.NewClientset(ready, down)
	var buf bytes.Buffer
	if err := Nodes(context.Background(), c, kube.Flags{Color: true}, nil, &buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "\x1b[32mReady\x1b[0m") {
		t.Fatalf("Ready not green:\n%s", out)
	}
	if !strings.Contains(out, "\x1b[31mNotReady\x1b[0m") {
		t.Fatalf("NotReady not red:\n%s", out)
	}
}

func TestNodesColorUnknownAndPlaceholders(t *testing.T) {
	// Node with no Ready condition → status "Unknown"; no labels → muted <none>.
	c := fake.NewClientset(&corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "n"}})
	var buf bytes.Buffer
	if err := Nodes(context.Background(), c, kube.Flags{Color: true}, nil, &buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "\x1b[31mUnknown\x1b[0m") {
		t.Fatalf("Unknown status not red:\n%s", out)
	}
	if !strings.Contains(out, "\x1b[90m<none>\x1b[0m") {
		t.Fatalf("missing label not muted:\n%s", out)
	}
}
