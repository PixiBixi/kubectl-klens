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

func podWithLimits(name, ns string, limits corev1.ResourceList) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
		Spec: corev1.PodSpec{Containers: []corev1.Container{
			{Name: "app", Resources: corev1.ResourceRequirements{Limits: limits}},
		}},
	}
}

func TestNoLimits(t *testing.T) {
	c := fake.NewClientset(
		podWithLimits("full", "default", corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse("100m"),
			corev1.ResourceMemory: resource.MustParse("128Mi"),
		}),
		podWithLimits("nomem", "default", corev1.ResourceList{
			corev1.ResourceCPU: resource.MustParse("100m"),
		}),
		podWithLimits("none", "default", nil),
		podWithLimits("system", "kube-system", nil),
	)
	var buf bytes.Buffer
	if err := NoLimits(context.Background(), c, kube.Flags{}, nil, &buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if strings.Contains(out, "full") {
		t.Fatalf("container with both limits must be omitted:\n%s", out)
	}
	if strings.Contains(out, "system") {
		t.Fatalf("kube-system must be excluded:\n%s", out)
	}
	if !strings.Contains(out, "nomem") || !strings.Contains(out, "memory") {
		t.Fatalf("want nomem flagged as missing memory:\n%s", out)
	}
	if !strings.Contains(out, "cpu,memory") {
		t.Fatalf("want 'none' flagged as missing cpu,memory:\n%s", out)
	}
}
