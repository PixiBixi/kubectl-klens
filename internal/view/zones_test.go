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

func TestZones(t *testing.T) {
	node := &corev1.Node{
		Name: "n1",
		Labels: map[string]string{
			"topology.kubernetes.io/region": "us-west1",
			"topology.kubernetes.io/zone":   "us-west1-a",
		},
	}
	c := fake.NewClientset(node)
	var buf bytes.Buffer
	if err := Zones(context.Background(), clients(c), kube.Flags{}, nil, &buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{"REGION", "ZONE", "us-west1", "us-west1-a"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q:\n%s", want, out)
		}
	}
}
