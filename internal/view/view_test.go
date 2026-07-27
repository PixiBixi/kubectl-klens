package view

import (
	"testing"

	corev1 "k8s.io/api/core/v1"

	"github.com/PixiBixi/kubectl-klens/internal/kube"
)

func TestNodeStatus(t *testing.T) {
	node := func(conds ...corev1.NodeCondition) *corev1.Node {
		return &corev1.Node{Status: corev1.NodeStatus{Conditions: conds}}
	}
	ready := func(s corev1.ConditionStatus) corev1.NodeCondition {
		return corev1.NodeCondition{Type: corev1.NodeReady, Status: s}
	}
	tests := []struct {
		name string
		n    *corev1.Node
		want string
	}{
		{"ready", node(ready(corev1.ConditionTrue)), "Ready"},
		{"kubelet says not ready", node(ready(corev1.ConditionFalse)), "NotReady"},
		// The distinction that matters operationally: Unknown means the kubelet
		// stopped reporting, which is what starts the eviction clock.
		{"kubelet stopped reporting", node(ready(corev1.ConditionUnknown)), "Unknown"},
		{"no ready condition", node(), "Unknown"},
		{"ready among others", node(
			corev1.NodeCondition{Type: corev1.NodeMemoryPressure, Status: corev1.ConditionFalse},
			ready(corev1.ConditionTrue),
		), "Ready"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := nodeStatus(tc.n); got != tc.want {
				t.Fatalf("nodeStatus = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestPodContainersOrderAndKinds(t *testing.T) {
	p := &corev1.Pod{Spec: corev1.PodSpec{
		InitContainers: []corev1.Container{{Name: "migrate"}, {Name: "wait-db"}},
		Containers:     []corev1.Container{{Name: "api"}},
		EphemeralContainers: []corev1.EphemeralContainer{
			{EphemeralContainerCommon: corev1.EphemeralContainerCommon{Name: "debugger"}},
		},
	}}
	got := podContainers(p)
	want := []podContainer{
		{Kind: kindInit}, {Kind: kindInit}, {Kind: kindApp}, {Kind: kindEph},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d containers, want %d", len(got), len(want))
	}
	wantNames := []string{"migrate", "wait-db", "api", "debugger"}
	for i := range want {
		if got[i].Kind != want[i].Kind {
			t.Errorf("container %d kind = %q, want %q", i, got[i].Kind, want[i].Kind)
		}
		if got[i].Spec.Name != wantNames[i] {
			t.Errorf("container %d name = %q, want %q", i, got[i].Spec.Name, wantNames[i])
		}
	}
}

func TestPodContainerStatusesOrderAndKinds(t *testing.T) {
	p := &corev1.Pod{Status: corev1.PodStatus{
		InitContainerStatuses:      []corev1.ContainerStatus{{Name: "migrate"}},
		ContainerStatuses:          []corev1.ContainerStatus{{Name: "api"}},
		EphemeralContainerStatuses: []corev1.ContainerStatus{{Name: "debugger"}},
	}}
	got := podContainerStatuses(p)
	wantKinds := []string{kindInit, kindApp, kindEph}
	wantNames := []string{"migrate", "api", "debugger"}
	if len(got) != len(wantKinds) {
		t.Fatalf("got %d statuses, want %d", len(got), len(wantKinds))
	}
	for i := range wantKinds {
		if got[i].Kind != wantKinds[i] || got[i].Status.Name != wantNames[i] {
			t.Errorf("status %d = (%q, %q), want (%q, %q)",
				i, got[i].Status.Name, got[i].Kind, wantNames[i], wantKinds[i])
		}
	}
}

// TestSkipNamespace locks in that kube-system is only dropped from the
// cluster-wide view: an explicit -n kube-system must return its rows rather than
// silently print nothing.
func TestSkipNamespace(t *testing.T) {
	tests := []struct {
		name string
		f    kube.Flags
		ns   string
		want bool
	}{
		{"all namespaces skips kube-system", kube.Flags{AllNamespaces: true}, "kube-system", true},
		{"all namespaces keeps workloads", kube.Flags{AllNamespaces: true}, "team-a", false},
		{"explicit -n kube-system is honoured", kube.Flags{Namespace: "kube-system"}, "kube-system", false},
		{"scoped namespace keeps its rows", kube.Flags{Namespace: "team-a"}, "team-a", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := skipNamespace(tc.f, tc.ns); got != tc.want {
				t.Fatalf("skipNamespace(%+v, %q) = %v, want %v", tc.f, tc.ns, got, tc.want)
			}
		})
	}
}
