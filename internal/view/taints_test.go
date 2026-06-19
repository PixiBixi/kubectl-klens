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

func TestTaintsColor(t *testing.T) {
	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "n1"},
		Spec: corev1.NodeSpec{Taints: []corev1.Taint{
			{Key: "a", Value: "1", Effect: corev1.TaintEffectNoExecute},
			{Key: "b", Value: "2", Effect: corev1.TaintEffectNoSchedule},
			{Key: "c", Value: "3", Effect: corev1.TaintEffectPreferNoSchedule},
		}},
	}
	clean := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "n2"}}
	c := fake.NewClientset(node, clean)
	var buf bytes.Buffer
	if err := Taints(context.Background(), c, kube.Flags{Color: true}, nil, &buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{
		"\x1b[31mNoExecute\x1b[0m",        // red: evicts running pods
		"\x1b[33mNoSchedule\x1b[0m",       // yellow
		"\x1b[90mPreferNoSchedule\x1b[0m", // gray: soft
		"\x1b[90m<none>\x1b[0m",           // muted placeholder on the clean node
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing colored token %q:\n%s", want, out)
		}
	}
}
