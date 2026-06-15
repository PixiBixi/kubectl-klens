package view

import (
	"bytes"
	"context"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/PixiBixi/kubectl-klens/internal/kube"
)

func TestReqlim(t *testing.T) {
	app := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "app", Namespace: "prod"},
		Spec: corev1.PodSpec{Containers: []corev1.Container{{
			Name: "main",
			Resources: corev1.ResourceRequirements{
				Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("100m")},
				Limits:   corev1.ResourceList{corev1.ResourceMemory: resource.MustParse("256Mi")},
			},
		}}},
	}
	sys := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "kube-proxy", Namespace: "kube-system"},
		Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "kube-proxy"}}},
	}
	c := fake.NewClientset(app, sys)
	var buf bytes.Buffer
	if err := Reqlim(context.Background(), c, kube.Flags{}, nil, &buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "100m") || !strings.Contains(out, "256Mi") || !strings.Contains(out, "none") {
		t.Fatalf("missing values:\n%s", out)
	}
	if strings.Contains(out, "kube-proxy") {
		t.Fatalf("kube-system must be excluded:\n%s", out)
	}
}
