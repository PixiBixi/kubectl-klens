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
