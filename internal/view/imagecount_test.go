package view

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"k8s.io/client-go/kubernetes/fake"

	"github.com/PixiBixi/kubectl-klens/internal/kube"
)

func TestImageCount(t *testing.T) {
	c := fake.NewClientset(
		podImg("a", "nginx:1.25"),
		podImg("b", "nginx:1.25"),
		podImg("c", "registry.k8s.io/pause:3.9"),
	)
	var buf bytes.Buffer
	if err := ImageCount(context.Background(), c, kube.Flags{}, nil, &buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{
		"COUNT", "REGISTRY", "IMAGE", "TAG",
		"docker.io", "nginx", "1.25",
		"registry.k8s.io", "pause", "3.9",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("output missing %q:\n%s", want, out)
		}
	}
	// nginx (2) must sort before pause (1).
	if strings.Index(out, "nginx") > strings.Index(out, "pause") {
		t.Fatalf("nginx (2) should sort before pause (1):\n%s", out)
	}
}

func TestImageCountColorLatest(t *testing.T) {
	c := fake.NewClientset(podImg("p1", "nginx:latest"))
	var buf bytes.Buffer
	if err := ImageCount(context.Background(), c, kube.Flags{Color: true}, nil, &buf); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "\x1b[33mlatest\x1b[0m") {
		t.Fatalf("latest tag not yellow:\n%s", buf.String())
	}
}

func TestImageCountSortByTag(t *testing.T) {
	c := fake.NewClientset(
		podImg("a", "nginx:9"),
		podImg("b", "nginx:1"),
	)
	var buf bytes.Buffer
	if err := ImageCount(context.Background(), c, kube.Flags{Sort: "tag"}, nil, &buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	// TAG is the last column, so each data row ends with its tag. Ascending by
	// tag puts "1" before "9".
	if strings.Index(out, "1\n") > strings.Index(out, "9\n") {
		t.Fatalf("want tag 1 before 9:\n%s", out)
	}
}

func TestImageCountInvalidSort(t *testing.T) {
	c := fake.NewClientset(podImg("a", "nginx:1"))
	var buf bytes.Buffer
	err := ImageCount(context.Background(), c, kube.Flags{Sort: "bogus"}, nil, &buf)
	if err == nil || !strings.Contains(err.Error(), "invalid --sort") {
		t.Fatalf("want invalid --sort error, got %v", err)
	}
}

func TestParseImageRef(t *testing.T) {
	cases := []struct {
		ref, registry, repo, tag string
	}{
		{"nginx:1.25", "docker.io", "nginx", "1.25"},
		{"redis", "docker.io", "redis", "latest"},
		{"haproxytech/kubernetes-ingress:3.1.2", "docker.io", "haproxytech/kubernetes-ingress", "3.1.2"},
		{"registry.k8s.io/pause:3.9", "registry.k8s.io", "pause", "3.9"},
		{"gcr.io/google-containers/foo:v1", "gcr.io", "google-containers/foo", "v1"},
		{"localhost:5000/app:dev", "localhost:5000", "app", "dev"},
	}
	for _, tc := range cases {
		registry, repo, tag := parseImageRef(tc.ref)
		if registry != tc.registry || repo != tc.repo || tag != tc.tag {
			t.Errorf("parseImageRef(%q) = (%q, %q, %q), want (%q, %q, %q)",
				tc.ref, registry, repo, tag, tc.registry, tc.repo, tc.tag)
		}
	}
}
