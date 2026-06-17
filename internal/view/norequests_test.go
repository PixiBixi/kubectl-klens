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

func podWithRequests(name, ns string, requests corev1.ResourceList) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
		Spec: corev1.PodSpec{Containers: []corev1.Container{
			{Name: "app", Resources: corev1.ResourceRequirements{Requests: requests}},
		}},
	}
}

func TestNoRequests(t *testing.T) {
	c := fake.NewClientset(
		podWithRequests("full", "default", corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse("100m"),
			corev1.ResourceMemory: resource.MustParse("128Mi"),
		}),
		podWithRequests("none", "default", nil),
	)
	var buf bytes.Buffer
	if err := NoRequests(context.Background(), c, kube.Flags{}, nil, &buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if strings.Contains(out, "full") {
		t.Fatalf("container with both requests must be omitted:\n%s", out)
	}
	if !strings.Contains(out, "none") || !strings.Contains(out, "cpu,memory") {
		t.Fatalf("want 'none' flagged as missing cpu,memory:\n%s", out)
	}
}
