package view

import (
	"bytes"
	"context"
	"slices"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/PixiBixi/kubectl-klens/internal/kube"
)

func nodeMaxPods(name string, max int64) *corev1.Node {
	return &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Status: corev1.NodeStatus{
			Allocatable: corev1.ResourceList{
				corev1.ResourcePods: *resource.NewQuantity(max, resource.DecimalSI),
			},
		},
	}
}

func TestMaxPods(t *testing.T) {
	c := fake.NewClientset(
		nodeMaxPods("n1", 110),
		pod("a", "default", "n1"),
		pod("b", "kube-system", "n1"),
	)
	var buf bytes.Buffer
	if err := MaxPods(context.Background(), c, kube.Flags{}, nil, &buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	// n1: max 110, 2 pods used (counted across namespaces), 108 free.
	for _, want := range []string{"NODE", "MAXPODS", "USED", "FREE", "n1", "110", "108"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q:\n%s", want, out)
		}
	}
}

func TestMaxPodsIgnoresTerminatedPods(t *testing.T) {
	// Two running pods and one Completed (Succeeded) pod on the node. The
	// terminated pod no longer holds a kubelet slot, so USED must be 2, not 3.
	terminated := pod("done", "ns", "n1")
	terminated.Status.Phase = corev1.PodSucceeded
	c := fake.NewClientset(
		nodeMaxPods("n1", 10),
		pod("a", "ns", "n1"),
		pod("b", "ns", "n1"),
		terminated,
	)
	var buf bytes.Buffer
	if err := MaxPods(context.Background(), c, kube.Flags{}, nil, &buf); err != nil {
		t.Fatal(err)
	}
	// n1: max 10, 2 non-terminated pods, 8 free.
	fields := strings.Fields(buf.String())
	// header (4) + row: NODE MAXPODS USED FREE → n1 10 2 8
	for _, want := range []string{"n1", "10", "2", "8"} {
		found := slices.Contains(fields, want)
		if !found {
			t.Fatalf("missing %q (terminated pod should not be counted):\n%s", want, buf.String())
		}
	}
}

func TestMaxPodsColorWhenFull(t *testing.T) {
	// Node ceiling of 1 with one pod scheduled → FREE == 0.
	c := fake.NewClientset(nodeMaxPods("node-a", 1), pod("p1", "ns", "node-a"))
	var buf bytes.Buffer
	if err := MaxPods(context.Background(), c, kube.Flags{Color: true}, nil, &buf); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "\x1b[31m0\x1b[0m") {
		t.Fatalf("zero free slots not red:\n%s", buf.String())
	}
}

func TestMaxPodsColorBySaturation(t *testing.T) {
	// Healthy node: 110 ceiling, no pods → 110 free, green.
	healthy := fake.NewClientset(nodeMaxPods("roomy", 110))
	var buf bytes.Buffer
	if err := MaxPods(context.Background(), healthy, kube.Flags{Color: true}, nil, &buf); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "\x1b[32m110\x1b[0m") {
		t.Fatalf("healthy free slots not green:\n%s", buf.String())
	}
	// Near ceiling: 10 ceiling, 9 pods → 1 free (≤10% of max), yellow.
	tight := fake.NewClientset(nodeMaxPods("tight", 10),
		pod("a", "ns", "tight"), pod("b", "ns", "tight"), pod("c", "ns", "tight"),
		pod("d", "ns", "tight"), pod("e", "ns", "tight"), pod("f", "ns", "tight"),
		pod("g", "ns", "tight"), pod("h", "ns", "tight"), pod("i", "ns", "tight"))
	buf.Reset()
	if err := MaxPods(context.Background(), tight, kube.Flags{Color: true}, nil, &buf); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "\x1b[33m1\x1b[0m") {
		t.Fatalf("near-ceiling free slots not yellow:\n%s", buf.String())
	}
}

func TestMaxPodsUnknownAllocatable(t *testing.T) {
	c := fake.NewClientset(&corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "n1"}})
	var buf bytes.Buffer
	if err := MaxPods(context.Background(), c, kube.Flags{}, nil, &buf); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "none") {
		t.Fatalf("want 'none' when allocatable pods is unset:\n%s", buf.String())
	}
}
