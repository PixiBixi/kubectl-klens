package view

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/PixiBixi/kubectl-klens/internal/kube"
)

// deletingPod builds a pod already carrying a deletionTimestamp, as the
// apiserver stamps it the moment a delete is accepted.
func deletingPod(name, ns, node string, ago time.Duration, grace *int64, finalizers ...string) *corev1.Pod {
	ts := metav1.NewTime(time.Now().Add(-ago))
	return &corev1.Pod{
		Name: name, Namespace: ns,
		DeletionTimestamp:          &ts,
		DeletionGracePeriodSeconds: grace,
		Finalizers:                 finalizers,
		Spec:                       corev1.PodSpec{NodeName: node},
	}
}

func readyNode(name string) *corev1.Node {
	return &corev1.Node{
		Name:   name,
		Status: corev1.NodeStatus{Conditions: []corev1.NodeCondition{{Type: corev1.NodeReady, Status: corev1.ConditionTrue}}},
	}
}

func lostNode(name string) *corev1.Node {
	return &corev1.Node{
		Name:   name,
		Status: corev1.NodeStatus{Conditions: []corev1.NodeCondition{{Type: corev1.NodeReady, Status: corev1.ConditionUnknown}}},
	}
}

func TestTerminating(t *testing.T) {
	grace := int64(30)
	c := fake.NewClientset(
		readyNode("n1"), lostNode("n2"),
		deletingPod("shutting-down", "app", "n1", 5*time.Second, &grace),
		deletingPod("wedged", "app", "n1", 30*time.Minute, &grace, "example.com/cleanup"),
		deletingPod("orphaned", "app", "n2", 20*time.Minute, &grace),
		&corev1.Pod{Name: "alive", Namespace: "app", Spec: corev1.PodSpec{NodeName: "n1"}},
	)
	var buf bytes.Buffer
	if err := Terminating(context.Background(), clients(c), kube.Flags{}, nil, &buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{
		"KIND", "NS", "NAME", "STUCK-FOR", "BLOCKER", "FINALIZERS", "VERDICT",
		"shutting-down", "GRACE",
		"wedged", "STUCK", "finalizer", "example.com/cleanup",
		"orphaned", "node n2 not ready",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("output missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "alive") {
		t.Fatalf("a pod with no deletionTimestamp must not be listed:\n%s", out)
	}
}

// TestTerminatingNamespace covers the stuck namespace, whose blocker the
// apiserver records as a condition that `get ns` never prints.
func TestTerminatingNamespace(t *testing.T) {
	ts := metav1.NewTime(time.Now().Add(-2 * time.Hour))
	ns := &corev1.Namespace{
		Name: "doomed", DeletionTimestamp: &ts,
		Spec: corev1.NamespaceSpec{Finalizers: []corev1.FinalizerName{corev1.FinalizerKubernetes}},
		Status: corev1.NamespaceStatus{
			Phase: corev1.NamespaceTerminating,
			Conditions: []corev1.NamespaceCondition{
				{Type: corev1.NamespaceDeletionDiscoveryFailure, Status: corev1.ConditionFalse},
				{Type: corev1.NamespaceContentRemaining, Status: corev1.ConditionTrue},
			},
		},
	}
	active := &corev1.Namespace{Name: "healthy", Status: corev1.NamespaceStatus{Phase: corev1.NamespaceActive}}
	var buf bytes.Buffer
	if err := Terminating(context.Background(), clients(fake.NewClientset(ns, active)), kube.Flags{}, nil, &buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{"Namespace", "doomed", "NamespaceContentRemaining", "kubernetes", "STUCK"} {
		if !strings.Contains(out, want) {
			t.Fatalf("output missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "healthy") {
		t.Fatalf("an Active namespace must not be listed:\n%s", out)
	}
}

// TestTerminatingNamespaceScope keeps -n from listing every other namespace's
// deletion: the user asked about one.
func TestTerminatingNamespaceScope(t *testing.T) {
	ts := metav1.NewTime(time.Now().Add(-time.Hour))
	mine := &corev1.Namespace{Name: "mine", DeletionTimestamp: &ts, Status: corev1.NamespaceStatus{Phase: corev1.NamespaceTerminating}}
	other := &corev1.Namespace{Name: "other", DeletionTimestamp: &ts, Status: corev1.NamespaceStatus{Phase: corev1.NamespaceTerminating}}
	var buf bytes.Buffer
	if err := Terminating(context.Background(), clients(fake.NewClientset(mine, other)), kube.Flags{Namespace: "mine"}, nil, &buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "mine") || strings.Contains(out, "other") {
		t.Fatalf("-n must scope the namespace rows too:\n%s", out)
	}
}

func TestTerminatingColor(t *testing.T) {
	grace := int64(30)
	c := fake.NewClientset(
		readyNode("n1"),
		deletingPod("fresh", "app", "n1", 2*time.Second, &grace),
		deletingPod("recent", "app", "n1", time.Minute, &grace),
		deletingPod("wedged", "app", "n1", time.Hour, &grace),
	)
	var buf bytes.Buffer
	if err := Terminating(context.Background(), clients(c), kube.Flags{Color: true}, nil, &buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{"\x1b[90mGRACE", "\x1b[33mDELETING", "\x1b[31mSTUCK"} {
		if !strings.Contains(out, want) {
			t.Fatalf("output missing colored %q:\n%q", want, out)
		}
	}
}
