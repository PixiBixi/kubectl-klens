package view

import (
	"bytes"
	"context"
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/PixiBixi/kubectl-klens/internal/kube"
)

func deploy(name, ns string, replicas, ready, updated, available int32, opts ...func(*appsv1.Deployment)) *appsv1.Deployment {
	d := &appsv1.Deployment{
		Name: name, Namespace: ns,
		Spec:   appsv1.DeploymentSpec{Replicas: &replicas},
		Status: appsv1.DeploymentStatus{ReadyReplicas: ready, UpdatedReplicas: updated, AvailableReplicas: available},
	}
	for _, o := range opts {
		o(d)
	}
	return d
}

func TestRollouts(t *testing.T) {
	stalled := deploy("stalled", "app", 3, 1, 1, 1, func(d *appsv1.Deployment) {
		d.Status.Conditions = []appsv1.DeploymentCondition{{
			Type: appsv1.DeploymentProgressing, Status: "False", Reason: "ProgressDeadlineExceeded",
		}}
	})
	paused := deploy("paused", "app", 2, 2, 2, 2, func(d *appsv1.Deployment) { d.Spec.Paused = true })
	unobserved := deploy("unobserved", "app", 2, 2, 2, 2, func(d *appsv1.Deployment) {
		d.Generation = 4
		d.Status.ObservedGeneration = 3
	})
	c := fake.NewClientset(
		deploy("healthy", "app", 2, 2, 2, 2),
		deploy("progressing", "app", 3, 2, 2, 2),
		deploy("down", "app", 2, 0, 2, 0),
		deploy("idle", "app", 0, 0, 0, 0),
		stalled, paused, unobserved,
		&appsv1.StatefulSet{
			Name: "db", Namespace: "app",
			Spec:   appsv1.StatefulSetSpec{Replicas: new(int32(2))},
			Status: appsv1.StatefulSetStatus{ReadyReplicas: 1, UpdatedReplicas: 1, AvailableReplicas: 1, CurrentRevision: "db-1", UpdateRevision: "db-2"},
		},
		&appsv1.DaemonSet{
			Name: "agent", Namespace: "app",
			Status: appsv1.DaemonSetStatus{DesiredNumberScheduled: 3, NumberReady: 3, UpdatedNumberScheduled: 3, NumberAvailable: 3, NumberMisscheduled: 1},
		},
	)
	var buf bytes.Buffer
	if err := Rollouts(context.Background(), clients(c), kube.Flags{}, nil, &buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{
		"NS", "KIND", "NAME", "DESIRED", "READY", "UPDATED", "AVAILABLE", "STATE", "VERDICT",
		"healthy", "OK",
		"progressing", "PROGRESSING",
		"down", "DOWN",
		"idle", "SCALED-ZERO",
		"stalled", "STALLED", "ProgressDeadlineExceeded",
		"paused", "PAUSED",
		"unobserved", "NOT-OBSERVED",
		"StatefulSet", "RevisionUpdating",
		"DaemonSet", "Misscheduled=1",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("output missing %q:\n%s", want, out)
		}
	}
}

// rolloutObj builds an Argo Rollout as the dynamic client sees it.
func rolloutObj(name, ns, phase string, replicas, ready int64, steps int, step int64) *unstructured.Unstructured {
	spec := map[string]any{"replicas": replicas}
	if steps > 0 {
		canary := make([]any, steps)
		for i := range canary {
			canary[i] = map[string]any{"setWeight": int64(10 * (i + 1))}
		}
		spec["strategy"] = map[string]any{"canary": map[string]any{"steps": canary}}
	}
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "argoproj.io/v1alpha1",
		"kind":       "Rollout",
		"metadata":   map[string]any{"name": name, "namespace": ns},
		"spec":       spec,
		"status": map[string]any{
			"phase":             phase,
			"readyReplicas":     ready,
			"updatedReplicas":   ready,
			"availableReplicas": ready,
			"currentStepIndex":  step,
		},
	}}
}

// argoClients bundles a typed fake with a dynamic fake serving the Rollout CRD.
func argoClients(typed *fake.Clientset, objs ...runtime.Object) kube.Clients {
	gvr := schema.GroupVersionResource{Group: "argoproj.io", Version: "v1alpha1", Resource: "rollouts"}
	d := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(
		runtime.NewScheme(),
		map[schema.GroupVersionResource]string{gvr: "RolloutList"},
		objs...,
	)
	return kube.Clients{Interface: typed, Dynamic: d}
}

func TestRolloutsReadsArgoRollouts(t *testing.T) {
	c := argoClients(
		fake.NewClientset(deploy("web", "app", 1, 1, 1, 1)),
		rolloutObj("canary", "app", "Paused", 4, 2, 5, 2),
		rolloutObj("degraded", "app", "Degraded", 2, 0, 0, 0),
		rolloutObj("healthy", "app", "Healthy", 2, 2, 0, 0),
	)
	var buf bytes.Buffer
	if err := Rollouts(context.Background(), c, kube.Flags{}, nil, &buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{
		"Rollout", "canary", "Paused 2/5", "PAUSED",
		"degraded", "STALLED",
		"healthy", "Healthy",
		"Deployment", "web",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("output missing %q:\n%s", want, out)
		}
	}
}

// TestRolloutsWithoutDynamicClient covers the cluster with no Argo CRD (and the
// bundle with no dynamic client at all): the built-in kinds must still print.
func TestRolloutsWithoutDynamicClient(t *testing.T) {
	var buf bytes.Buffer
	c := clients(fake.NewClientset(deploy("web", "app", 1, 1, 1, 1)))
	if err := Rollouts(context.Background(), c, kube.Flags{}, nil, &buf); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "web") {
		t.Fatalf("built-in workloads must still list:\n%s", buf.String())
	}
}

// TestRolloutsDefaultsReplicasToOne guards the unset spec.replicas case: read as
// zero it would report a single-replica workload as SCALED-ZERO.
func TestRolloutsDefaultsReplicasToOne(t *testing.T) {
	d := &appsv1.Deployment{
		Name: "noreplicas", Namespace: "app",
		Status: appsv1.DeploymentStatus{ReadyReplicas: 1, UpdatedReplicas: 1, AvailableReplicas: 1},
	}
	var buf bytes.Buffer
	if err := Rollouts(context.Background(), clients(fake.NewClientset(d)), kube.Flags{}, nil, &buf); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(buf.String(), "SCALED-ZERO") {
		t.Fatalf("unset spec.replicas must default to 1:\n%s", buf.String())
	}
}

func TestRolloutsColor(t *testing.T) {
	c := fake.NewClientset(
		deploy("healthy", "app", 2, 2, 2, 2),
		deploy("progressing", "app", 3, 2, 2, 2),
		deploy("down", "app", 2, 0, 2, 0),
	)
	var buf bytes.Buffer
	if err := Rollouts(context.Background(), clients(c), kube.Flags{Color: true}, nil, &buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{"\x1b[32mOK", "\x1b[33mPROGRESSING", "\x1b[31mDOWN", "\x1b[31m0"} {
		if !strings.Contains(out, want) {
			t.Fatalf("output missing colored %q:\n%q", want, out)
		}
	}
}
