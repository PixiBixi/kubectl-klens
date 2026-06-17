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

func nodeConditions(name string, conds ...corev1.NodeCondition) *corev1.Node {
	return &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Status:     corev1.NodeStatus{Conditions: conds},
	}
}

func TestNodeConditions(t *testing.T) {
	c := fake.NewClientset(nodeConditions("n1",
		corev1.NodeCondition{Type: corev1.NodeReady, Status: corev1.ConditionTrue},
		corev1.NodeCondition{Type: corev1.NodeMemoryPressure, Status: corev1.ConditionTrue},
		corev1.NodeCondition{Type: corev1.NodeDiskPressure, Status: corev1.ConditionFalse},
	))
	var buf bytes.Buffer
	if err := NodeConditions(context.Background(), c, kube.Flags{}, nil, &buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{"NAME", "STATUS", "MEMORY", "DISK", "PID", "n1", "Ready", "True", "False"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q:\n%s", want, out)
		}
	}
	// PIDPressure is absent from the node and must read as Unknown.
	if !strings.Contains(out, "Unknown") {
		t.Fatalf("want Unknown for the unreported pid condition:\n%s", out)
	}
}
