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

func podImg(name, image string) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
		Spec: corev1.PodSpec{Containers: []corev1.Container{
			{Name: "c", Image: image, ImagePullPolicy: corev1.PullIfNotPresent},
		}},
	}
}

func TestImages(t *testing.T) {
	c := fake.NewClientset(
		podImg("web-0", "haproxytech/kubernetes-ingress:3.1.2"),
		podImg("cache-0", "redis:7"),
	)
	var buf bytes.Buffer
	if err := Images(context.Background(), c, kube.Flags{}, nil, &buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{
		"PODNAME", "CONTAINER", "PULL", "IMAGE", "TAG",
		"web-0", "c", "IfNotPresent", "haproxytech/kubernetes-ingress", "3.1.2",
		"redis", "7",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("output missing %q:\n%s", want, out)
		}
	}
}

func TestImagesColorLatest(t *testing.T) {
	c := fake.NewClientset(podImg("p1", "nginx")) // implicit :latest
	var buf bytes.Buffer
	if err := Images(context.Background(), c, kube.Flags{Color: true, AllNamespaces: true}, nil, &buf); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "\x1b[33mlatest\x1b[0m") {
		t.Fatalf("latest tag not yellow:\n%s", buf.String())
	}
}

func TestSplitImageTag(t *testing.T) {
	cases := []struct {
		ref, name, tag string
	}{
		{"redis:7", "redis", "7"},
		{"haproxytech/kubernetes-ingress:3.1.2", "haproxytech/kubernetes-ingress", "3.1.2"},
		{"registry.k8s.io/pause:3.9", "registry.k8s.io/pause", "3.9"},
		{"localhost:5000/app", "localhost:5000/app", "latest"},
		{"nginx", "nginx", "latest"},
		{"busybox@sha256:abc", "busybox", "sha256:abc"},
	}
	for _, tc := range cases {
		name, tag := splitImageTag(tc.ref)
		if name != tc.name || tag != tc.tag {
			t.Errorf("splitImageTag(%q) = (%q, %q), want (%q, %q)", tc.ref, name, tag, tc.name, tc.tag)
		}
	}
}
