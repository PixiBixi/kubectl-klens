package view

import (
	"bytes"
	"context"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"

	"github.com/PixiBixi/kubectl-klens/internal/kube"
)

func pod(name, ns, node string) *corev1.Pod {
	return &corev1.Pod{
		Name: name, Namespace: ns,
		Spec: corev1.PodSpec{NodeName: node},
	}
}

func TestPodsPerNode(t *testing.T) {
	c := fake.NewClientset(
		pod("a", "default", "n1"),
		pod("b", "default", "n1"),
		pod("c", "default", "n2"),
	)
	var buf bytes.Buffer
	if err := PodsPerNode(context.Background(), clients(c), kube.Flags{}, nil, &buf); err != nil {
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

// TestPodsPerNodeSkipsFinishedPods: Succeeded/Failed pods hold no slot, and the
// phase filter must be pushed to the apiserver, not applied after the list.
func TestPodsPerNodeSkipsFinishedPods(t *testing.T) {
	done := pod("done", "default", "n1")
	done.Status.Phase = corev1.PodSucceeded
	c := fake.NewClientset(pod("a", "default", "n1"), done)
	var selector string
	c.PrependReactor("list", "pods", func(a k8stesting.Action) (bool, runtime.Object, error) {
		if la, ok := a.(k8stesting.ListAction); ok {
			selector = la.GetListRestrictions().Fields.String()
		}
		return false, nil, nil
	})
	var buf bytes.Buffer
	if err := PodsPerNode(context.Background(), clients(c), kube.Flags{}, nil, &buf); err != nil {
		t.Fatal(err)
	}
	if got := strings.Fields(buf.String()); got[len(got)-1] != "1" {
		t.Fatalf("want 1 pod on n1, got:\n%s", buf.String())
	}
	if selector != activePodsSelector {
		t.Fatalf("want field selector %q, got %q", activePodsSelector, selector)
	}
}
