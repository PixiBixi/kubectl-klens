package view

import (
	"bytes"
	"context"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"

	"github.com/PixiBixi/kubectl-klens/internal/kube"
)

func TestDefaultSAFilters(t *testing.T) {
	// The SA match is pushed down, so the fake has to apply the field selector for
	// the custom-SA pod to be excluded the way a real apiserver excludes it.
	c := newClientsetWithFieldSelectors(
		&corev1.Pod{
			Name: "pod-default", Namespace: "default",
			Spec: corev1.PodSpec{ServiceAccountName: "default"},
		},
		&corev1.Pod{
			Name: "pod-custom", Namespace: "default",
			Spec: corev1.PodSpec{ServiceAccountName: "custom-sa"},
		},
	)
	var buf bytes.Buffer
	if err := DefaultSA(context.Background(), clients(c), kube.Flags{}, nil, &buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "pod-default") {
		t.Fatalf("pod-default (SA=default) should be listed:\n%s", out)
	}
	if strings.Contains(out, "pod-custom") {
		t.Fatalf("pod-custom (SA=custom-sa) must not be listed:\n%s", out)
	}
	assertFieldSelector(t, c, "pods", "spec.serviceAccountName=default")
}
