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

func TestDefaultSAFilters(t *testing.T) {
	c := fake.NewClientset(
		&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: "pod-default", Namespace: "default"},
			Spec:       corev1.PodSpec{ServiceAccountName: "default"},
		},
		&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: "pod-custom", Namespace: "default"},
			Spec:       corev1.PodSpec{ServiceAccountName: "custom-sa"},
		},
	)
	var buf bytes.Buffer
	if err := DefaultSA(context.Background(), c, kube.Flags{}, nil, &buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "pod-default") {
		t.Fatalf("pod-default (SA=default) should be listed:\n%s", out)
	}
	if strings.Contains(out, "pod-custom") {
		t.Fatalf("pod-custom (SA=custom-sa) must not be listed:\n%s", out)
	}
}
