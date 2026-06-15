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

func TestAutoscalerReturnsStatus(t *testing.T) {
	c := fake.NewClientset(
		&corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "cluster-autoscaler-status",
				Namespace: "kube-system",
			},
			Data: map[string]string{"status": "Health: Healthy"},
		},
	)
	var buf bytes.Buffer
	if err := Autoscaler(context.Background(), c, kube.Flags{}, nil, &buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "Healthy") {
		t.Fatalf("expected Healthy in output:\n%s", out)
	}
}

func TestAutoscalerMissingConfigMap(t *testing.T) {
	c := fake.NewClientset()
	var buf bytes.Buffer
	err := Autoscaler(context.Background(), c, kube.Flags{}, nil, &buf)
	if err == nil {
		t.Fatal("expected error when configmap is missing, got nil")
	}
}
