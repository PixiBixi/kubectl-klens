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

func TestPrivileged(t *testing.T) {
	yes := true
	c := fake.NewClientset(
		&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: "safe", Namespace: "default"},
			Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "app"}}},
		},
		&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: "priv", Namespace: "default"},
			Spec: corev1.PodSpec{Containers: []corev1.Container{
				{Name: "app", SecurityContext: &corev1.SecurityContext{Privileged: &yes}},
			}},
		},
		&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: "host", Namespace: "default"},
			Spec: corev1.PodSpec{
				HostNetwork: true,
				Containers:  []corev1.Container{{Name: "app"}},
			},
		},
	)
	var buf bytes.Buffer
	if err := Privileged(context.Background(), c, kube.Flags{}, nil, &buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if strings.Contains(out, "safe") {
		t.Fatalf("container with no security flags must be omitted:\n%s", out)
	}
	if !strings.Contains(out, "priv") || !strings.Contains(out, "privileged") {
		t.Fatalf("want 'priv' flagged privileged:\n%s", out)
	}
	if !strings.Contains(out, "host") || !strings.Contains(out, "hostNetwork") {
		t.Fatalf("want 'host' flagged hostNetwork:\n%s", out)
	}
}

func TestPrivilegedColor(t *testing.T) {
	yes := true
	c := fake.NewClientset(&corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "priv", Namespace: "default"},
		Spec: corev1.PodSpec{Containers: []corev1.Container{
			{Name: "app", SecurityContext: &corev1.SecurityContext{Privileged: &yes}},
		}},
	})
	var buf bytes.Buffer
	if err := Privileged(context.Background(), c, kube.Flags{Color: true}, nil, &buf); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "\x1b[31mprivileged\x1b[0m") {
		t.Fatalf("flags not red:\n%s", buf.String())
	}
}
