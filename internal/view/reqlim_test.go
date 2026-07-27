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

// TestReqlimInitContainer guards that init container requests are reported: they
// count toward the pod's effective scheduling footprint and toward ResourceQuota,
// so omitting them understated the real cost.
func TestReqlimInitContainer(t *testing.T) {
	c := fake.NewClientset(&corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "app", Namespace: "prod"},
		Spec: corev1.PodSpec{
			InitContainers: []corev1.Container{{
				Name: "migrate",
				Resources: corev1.ResourceRequirements{
					Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("2")},
				},
			}},
			Containers: []corev1.Container{{Name: "main"}},
		},
	})
	var buf bytes.Buffer
	if err := Reqlim(context.Background(), c, kube.Flags{}, nil, &buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "migrate") || !strings.Contains(out, kindInit) {
		t.Fatalf("want init container row:\n%s", out)
	}
	if !strings.Contains(out, "2") {
		t.Fatalf("want the init container's cpu request:\n%s", out)
	}
}

// TestReqlimExplicitKubeSystem guards the regression where asking for
// kube-system by name printed only headers.
func TestReqlimExplicitKubeSystem(t *testing.T) {
	c := fake.NewClientset(&corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "kube-proxy", Namespace: "kube-system"},
		Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "kube-proxy"}}},
	})
	var buf bytes.Buffer
	if err := Reqlim(context.Background(), c, kube.Flags{Namespace: "kube-system"}, nil, &buf); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "kube-proxy") {
		t.Fatalf("-n kube-system must list its own pods:\n%s", buf.String())
	}
}
