package view

import (
	"bytes"
	"context"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/PixiBixi/kubectl-klens/internal/kube"
)

func nodeWithAddresses(name string, addrs ...corev1.NodeAddress) *corev1.Node {
	return &corev1.Node{
		Name:   name,
		Status: corev1.NodeStatus{Addresses: addrs},
	}
}

func TestNodeIPs(t *testing.T) {
	public := nodeWithAddresses("public",
		corev1.NodeAddress{Type: corev1.NodeHostName, Address: "public"},
		corev1.NodeAddress{Type: corev1.NodeInternalIP, Address: "10.0.0.4"},
		corev1.NodeAddress{Type: corev1.NodeExternalIP, Address: "34.12.5.9"},
	)
	private := nodeWithAddresses("private",
		corev1.NodeAddress{Type: corev1.NodeInternalIP, Address: "10.0.0.5"},
	)
	c := fake.NewClientset(public, private)
	var buf bytes.Buffer
	if err := NodeIPs(context.Background(), c, kube.Flags{}, nil, &buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{"NAME", "INTERNAL-IP", "EXTERNAL-IP", "10.0.0.4", "34.12.5.9", "10.0.0.5", "<none>"} {
		if !strings.Contains(out, want) {
			t.Fatalf("output missing %q:\n%s", want, out)
		}
	}
	// The Hostname address must not leak into either IP column.
	if strings.Count(out, "public") != 1 {
		t.Fatalf("hostname address reported as an IP:\n%s", out)
	}
}

func TestNodeIPsSingleNode(t *testing.T) {
	// The filter is pushed to the apiserver, so the fake must apply it.
	c := newClientsetWithFieldSelectors(
		nodeWithAddresses("wanted", corev1.NodeAddress{Type: corev1.NodeInternalIP, Address: "10.0.0.4"}),
		nodeWithAddresses("other", corev1.NodeAddress{Type: corev1.NodeInternalIP, Address: "10.0.0.5"}),
	)
	var buf bytes.Buffer
	if err := NodeIPs(context.Background(), c, kube.Flags{}, []string{"wanted"}, &buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "10.0.0.4") {
		t.Fatalf("named node not listed:\n%s", out)
	}
	if strings.Contains(out, "other") || strings.Contains(out, "10.0.0.5") {
		t.Fatalf("other node must not be listed:\n%s", out)
	}
	assertFieldSelector(t, c, "nodes", "metadata.name=wanted")
}

func TestNodeIPsUnknownNode(t *testing.T) {
	c := newClientsetWithFieldSelectors(nodeWithAddresses("n1"))
	var buf bytes.Buffer
	err := NodeIPs(context.Background(), c, kube.Flags{}, []string{"typo"}, &buf)
	if err == nil || !strings.Contains(err.Error(), `node "typo" not found`) {
		t.Fatalf("expected not-found error, got %v (output %q)", err, buf.String())
	}
}

func TestNodeIPsEmptyArgListsEverything(t *testing.T) {
	// An empty positional arg (e.g. `node-ips ""`) must not become a selector
	// matching nothing — it lists the fleet like no arg at all.
	c := newClientsetWithFieldSelectors(
		nodeWithAddresses("a", corev1.NodeAddress{Type: corev1.NodeInternalIP, Address: "10.0.0.4"}),
		nodeWithAddresses("b", corev1.NodeAddress{Type: corev1.NodeInternalIP, Address: "10.0.0.5"}),
	)
	var buf bytes.Buffer
	if err := NodeIPs(context.Background(), c, kube.Flags{}, []string{""}, &buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "10.0.0.4") || !strings.Contains(out, "10.0.0.5") {
		t.Fatalf("both nodes should be listed:\n%s", out)
	}
}

func TestNodeIPsDualStack(t *testing.T) {
	n := nodeWithAddresses("ds",
		corev1.NodeAddress{Type: corev1.NodeInternalIP, Address: "10.0.0.4"},
		corev1.NodeAddress{Type: corev1.NodeInternalIP, Address: "fd00::4"},
	)
	var buf bytes.Buffer
	if err := NodeIPs(context.Background(), fake.NewClientset(n), kube.Flags{}, nil, &buf); err != nil {
		t.Fatal(err)
	}
	if out := buf.String(); !strings.Contains(out, "10.0.0.4,fd00::4") {
		t.Fatalf("both internal addresses not reported:\n%s", out)
	}
}

func TestNodeIPsColor(t *testing.T) {
	public := nodeWithAddresses("public",
		corev1.NodeAddress{Type: corev1.NodeInternalIP, Address: "10.0.0.4"},
		corev1.NodeAddress{Type: corev1.NodeExternalIP, Address: "34.12.5.9"},
	)
	// No addresses at all: a missing InternalIP is bad, a missing ExternalIP muted.
	broken := nodeWithAddresses("broken")
	c := fake.NewClientset(public, broken)
	var buf bytes.Buffer
	if err := NodeIPs(context.Background(), c, kube.Flags{Color: true}, nil, &buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{
		"\x1b[32m10.0.0.4\x1b[0m",  // internal present → green
		"\x1b[33m34.12.5.9\x1b[0m", // external present → warn
		"\x1b[31m<none>\x1b[0m",    // internal missing → bad
		"\x1b[90m<none>\x1b[0m",    // external missing → muted
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing colored cell %q:\n%s", want, out)
		}
	}
}

func TestNodeIPsSort(t *testing.T) {
	a := nodeWithAddresses("a", corev1.NodeAddress{Type: corev1.NodeInternalIP, Address: "10.0.0.9"})
	b := nodeWithAddresses("b", corev1.NodeAddress{Type: corev1.NodeInternalIP, Address: "10.0.0.1"})
	var buf bytes.Buffer
	if err := NodeIPs(context.Background(), fake.NewClientset(a, b), kube.Flags{Sort: "internal-ip"}, nil, &buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if strings.Index(out, "10.0.0.1") > strings.Index(out, "10.0.0.9") {
		t.Fatalf("rows not sorted by internal-ip:\n%s", out)
	}
}
