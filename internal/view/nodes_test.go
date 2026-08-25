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

func TestNodes(t *testing.T) {
	node := &corev1.Node{
		Name: "gke-pool-1-abc",
		Labels: map[string]string{
			"cloud.google.com/gke-nodepool":    "pool-1",
			"node.kubernetes.io/instance-type": "e2-standard-4",
		},
		Status: corev1.NodeStatus{
			Conditions: []corev1.NodeCondition{{Type: corev1.NodeReady, Status: corev1.ConditionTrue}},
		},
	}
	c := fake.NewClientset(node)
	var buf bytes.Buffer
	if err := Nodes(context.Background(), clients(c), kube.Flags{}, nil, &buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{"NAME", "gke-pool-1-abc", "Ready", "pool-1", "e2-standard-4"} {
		if !strings.Contains(out, want) {
			t.Fatalf("output missing %q:\n%s", want, out)
		}
	}
}

func TestNodesColor(t *testing.T) {
	ready := &corev1.Node{
		Name:   "ok",
		Status: corev1.NodeStatus{Conditions: []corev1.NodeCondition{{Type: corev1.NodeReady, Status: corev1.ConditionTrue}}},
	}
	down := &corev1.Node{
		Name:   "down",
		Status: corev1.NodeStatus{Conditions: []corev1.NodeCondition{{Type: corev1.NodeReady, Status: corev1.ConditionFalse}}},
	}
	c := fake.NewClientset(ready, down)
	var buf bytes.Buffer
	if err := Nodes(context.Background(), clients(c), kube.Flags{Color: true}, nil, &buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "\x1b[32mReady\x1b[0m") {
		t.Fatalf("Ready not green:\n%s", out)
	}
	if !strings.Contains(out, "\x1b[31mNotReady\x1b[0m") {
		t.Fatalf("NotReady not red:\n%s", out)
	}
}

func TestNodesColorUnknownAndPlaceholders(t *testing.T) {
	// Node with no Ready condition → status "Unknown"; no labels → muted <none>.
	c := fake.NewClientset(&corev1.Node{Name: "n"})
	var buf bytes.Buffer
	if err := Nodes(context.Background(), clients(c), kube.Flags{Color: true}, nil, &buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "\x1b[31mUnknown\x1b[0m") {
		t.Fatalf("Unknown status not red:\n%s", out)
	}
	if !strings.Contains(out, "\x1b[90m<none>\x1b[0m") {
		t.Fatalf("missing label not muted:\n%s", out)
	}
}

func TestNodesNoCloudLabelsProvisioningIsNone(t *testing.T) {
	// Negative case: a node with zero cloud labels must report PROVISIONING
	// <none>, never a guessed "on-demand".
	node := &corev1.Node{Name: "bare", Labels: map[string]string{"kubernetes.io/hostname": "bare"}}
	c := fake.NewClientset(node)
	var buf bytes.Buffer
	if err := Nodes(context.Background(), clients(c), kube.Flags{}, nil, &buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "<none>") {
		t.Fatalf("provisioning should be <none> for a node with no cloud labels:\n%s", out)
	}
	if strings.Contains(out, "on-demand") {
		t.Fatalf("provisioning must not be guessed as on-demand:\n%s", out)
	}
}

func TestNodesCloudProvisioningAndClass(t *testing.T) {
	tests := []struct {
		name      string
		labels    map[string]string
		wantPool  string
		wantClass string
		wantCap   string
		unwantCap string // provisioning value that must NOT appear (guards against a wrong normalization)
	}{
		{
			name: "gke spot",
			labels: map[string]string{
				"cloud.google.com/gke-nodepool": "pool-1",
				"cloud.google.com/gke-spot":     "true",
			},
			wantPool: "pool-1",
			wantCap:  "spot",
		},
		{
			name: "gke compute-class",
			labels: map[string]string{
				"cloud.google.com/gke-nodepool":  "pool-1",
				"cloud.google.com/compute-class": "autopilot",
			},
			wantPool:  "pool-1",
			wantClass: "autopilot",
			wantCap:   "on-demand",
		},
		{
			name: "gke on-demand, no spot label",
			labels: map[string]string{
				"cloud.google.com/gke-nodepool": "pool-1",
			},
			wantPool: "pool-1",
			wantCap:  "on-demand",
		},
		{
			name: "eks managed node group spot",
			labels: map[string]string{
				"eks.amazonaws.com/nodegroup":    "ng-1",
				"eks.amazonaws.com/capacityType": "SPOT",
			},
			wantPool: "ng-1",
			wantCap:  "spot",
		},
		{
			name: "eks managed node group on-demand",
			labels: map[string]string{
				"eks.amazonaws.com/nodegroup":    "ng-1",
				"eks.amazonaws.com/capacityType": "ON_DEMAND",
			},
			wantPool:  "ng-1",
			wantCap:   "on-demand",
			unwantCap: "ON_DEMAND",
		},
		{
			name: "karpenter spot",
			labels: map[string]string{
				"karpenter.sh/nodepool":      "default",
				"karpenter.sh/capacity-type": "spot",
			},
			wantPool: "default",
			wantCap:  "spot",
		},
		{
			name: "aks spot",
			labels: map[string]string{
				"kubernetes.azure.com/agentpool": "nodepool1",
				"kubernetes.azure.com/priority":  "spot",
			},
			wantPool: "nodepool1",
			wantCap:  "spot",
		},
		{
			name: "aks regular via agentpool",
			labels: map[string]string{
				"kubernetes.azure.com/agentpool": "nodepool1",
			},
			wantPool: "nodepool1",
			wantCap:  "on-demand",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			node := &corev1.Node{Name: "n", Labels: tc.labels}
			c := fake.NewClientset(node)
			var buf bytes.Buffer
			if err := Nodes(context.Background(), clients(c), kube.Flags{}, nil, &buf); err != nil {
				t.Fatal(err)
			}
			out := buf.String()
			if !strings.Contains(out, tc.wantPool) {
				t.Fatalf("output missing nodepool %q:\n%s", tc.wantPool, out)
			}
			if tc.wantClass != "" && !strings.Contains(out, tc.wantClass) {
				t.Fatalf("output missing class %q:\n%s", tc.wantClass, out)
			}
			if !strings.Contains(out, tc.wantCap) {
				t.Fatalf("output missing provisioning %q:\n%s", tc.wantCap, out)
			}
			if tc.unwantCap != "" && strings.Contains(out, tc.unwantCap) {
				t.Fatalf("output must not contain raw value %q:\n%s", tc.unwantCap, out)
			}
		})
	}
}

func TestNodesProvisioningColor(t *testing.T) {
	onDemand := &corev1.Node{Name: "od", Labels: map[string]string{"cloud.google.com/gke-nodepool": "pool-1"}}
	spot := &corev1.Node{Name: "spot", Labels: map[string]string{"cloud.google.com/gke-spot": "true"}}
	c := fake.NewClientset(onDemand, spot)
	var buf bytes.Buffer
	if err := Nodes(context.Background(), clients(c), kube.Flags{Color: true}, nil, &buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "\x1b[32mon-demand\x1b[0m") {
		t.Fatalf("on-demand not green:\n%s", out)
	}
	if !strings.Contains(out, "\x1b[33mspot\x1b[0m") {
		t.Fatalf("spot not yellow:\n%s", out)
	}
}
