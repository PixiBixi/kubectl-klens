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

func TestSpreadVerdict(t *testing.T) {
	cases := []struct {
		name                   string
		replicas, nodes, zones int
		wantVerdict, wantSev   string
	}{
		{"single", 1, 1, 0, "SINGLE", "muted"},
		{"spof-node", 3, 1, 1, "SPOF-NODE", "bad"},
		{"spof-zone", 3, 2, 1, "SPOF-ZONE", "warn"},
		{"spread", 2, 2, 2, "SPREAD", "ok"},
		{"multi-node", 2, 2, 0, "MULTI-NODE", "muted"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotV, gotS := spreadVerdict(tc.replicas, tc.nodes, tc.zones)
			if gotV != tc.wantVerdict || gotS != tc.wantSev {
				t.Fatalf("spreadVerdict(%d,%d,%d) = (%q,%q), want (%q,%q)", tc.replicas, tc.nodes, tc.zones, gotV, gotS, tc.wantVerdict, tc.wantSev)
			}
		})
	}
}

func zonedNode(name, zone string) *corev1.Node {
	return &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: name, Labels: map[string]string{"topology.kubernetes.io/zone": zone}}}
}

func ownedPod(name, node string, owner metav1.OwnerReference) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default", OwnerReferences: []metav1.OwnerReference{owner}},
		Spec:       corev1.PodSpec{NodeName: node},
	}
}

func TestSpread(t *testing.T) {
	ctrl := true
	rs := metav1.OwnerReference{Kind: "ReplicaSet", Name: "web-abc123", Controller: &ctrl}
	ss := metav1.OwnerReference{Kind: "StatefulSet", Name: "db", Controller: &ctrl}
	ds := metav1.OwnerReference{Kind: "DaemonSet", Name: "node-exp", Controller: &ctrl}

	c := fake.NewClientset(
		zonedNode("node-a", "a"), zonedNode("node-b", "b"), zonedNode("node-c", "a"),
		ownedPod("web-1", "node-a", rs), ownedPod("web-2", "node-b", rs), // 2 zones -> SPREAD
		ownedPod("db-0", "node-a", ss), ownedPod("db-1", "node-a", ss), // same node -> SPOF-NODE
		ownedPod("exp-1", "node-a", ds), // DaemonSet -> excluded
	)

	var buf bytes.Buffer
	if err := Spread(context.Background(), c, kube.Flags{Namespace: "default"}, nil, &buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{"Deployment/web", "SPREAD", "StatefulSet/db", "SPOF-NODE"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "node-exp") || strings.Contains(out, "DaemonSet") {
		t.Fatalf("DaemonSet workload must be excluded:\n%s", out)
	}
	// Default verdict sort: least-risky first, so SPREAD (web) ranks before
	// SPOF-NODE (db), which sinks toward the prompt.
	if strings.Index(out, "/db") < strings.Index(out, "/web") {
		t.Fatalf("expected verdict-sort default (web before db):\n%s", out)
	}
}

func TestSpreadColor(t *testing.T) {
	ctrl := true
	rs := metav1.OwnerReference{Kind: "ReplicaSet", Name: "web-abc123", Controller: &ctrl}
	ss := metav1.OwnerReference{Kind: "StatefulSet", Name: "db", Controller: &ctrl}

	c := fake.NewClientset(
		zonedNode("node-a", "a"), zonedNode("node-b", "b"),
		ownedPod("web-1", "node-a", rs), ownedPod("web-2", "node-b", rs),
		ownedPod("db-0", "node-a", ss), ownedPod("db-1", "node-a", ss),
	)

	var buf bytes.Buffer
	if err := Spread(context.Background(), c, kube.Flags{Namespace: "default", Color: true}, nil, &buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{"\x1b[31mSPOF-NODE\x1b[0m", "\x1b[32mSPREAD\x1b[0m"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing colored token %q:\n%s", want, out)
		}
	}
}
