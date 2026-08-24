package view

import (
	"bytes"
	"context"
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/PixiBixi/kubectl-klens/internal/kube"
)

func pvcFor(name, ns, class, size string, phase corev1.PersistentVolumeClaimPhase) *corev1.PersistentVolumeClaim {
	return &corev1.PersistentVolumeClaim{
		Name: name, Namespace: ns,
		Spec: corev1.PersistentVolumeClaimSpec{
			StorageClassName: &class,
			VolumeName:       "pv-" + name,
		},
		Status: corev1.PersistentVolumeClaimStatus{
			Phase:    phase,
			Capacity: corev1.ResourceList{corev1.ResourceStorage: resource.MustParse(size)},
		},
	}
}

func podMounting(name, ns, claim string) *corev1.Pod {
	return &corev1.Pod{
		Name: name, Namespace: ns,
		Spec: corev1.PodSpec{Volumes: []corev1.Volume{{
			Name: "data",
			PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
				ClaimName: claim,
			},
		}}},
	}
}

// stsWithClaim builds a StatefulSet whose volumeClaimTemplate generates
// data-<name>-<ordinal> claims, the naming contract the controller relies on.
func stsWithClaim(name, ns string, replicas int32) *appsv1.StatefulSet {
	return &appsv1.StatefulSet{
		Name: name, Namespace: ns,
		Spec: appsv1.StatefulSetSpec{
			Replicas: &replicas,
			VolumeClaimTemplates: []corev1.PersistentVolumeClaim{
				{Name: "data"},
			},
		},
	}
}

func TestPvcUnused(t *testing.T) {
	c := fake.NewClientset(
		pvcFor("in-use", "app", "standard", "10Gi", corev1.ClaimBound),
		podMounting("web", "app", "in-use"),
		pvcFor("forgotten", "app", "premium-rwo", "100Gi", corev1.ClaimBound),
		pvcFor("waiting", "app", "standard", "5Gi", corev1.ClaimPending),
		pvcFor("broken", "app", "standard", "1Gi", corev1.ClaimLost),
		stsWithClaim("db", "app", 2),
		pvcFor("data-db-0", "app", "premium-rwo", "50Gi", corev1.ClaimBound),
		pvcFor("data-db-5", "app", "premium-rwo", "50Gi", corev1.ClaimBound),
	)
	var buf bytes.Buffer
	if err := PvcUnused(context.Background(), clients(c), kube.Flags{}, nil, &buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if strings.Contains(out, "in-use") {
		t.Fatalf("a mounted PVC must not be listed:\n%s", out)
	}
	for _, want := range []string{
		"NS", "PVC", "STATUS", "CAPACITY", "CLASS", "VOLUME", "VERDICT",
		"forgotten", "ORPHAN", "100Gi", "premium-rwo", "pv-forgotten",
		"waiting", "UNBOUND",
		"broken", "LOST",
		"data-db-0", "STS-RESERVED",
		"data-db-5", "SCALED-DOWN",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("output missing %q:\n%s", want, out)
		}
	}
}

// TestPvcUnusedStatefulSetNamespaceIsolation guards the owner match: a
// StatefulSet must not vouch for an identically named claim in another
// namespace, which would hide a real orphan.
func TestPvcUnusedStatefulSetNamespaceIsolation(t *testing.T) {
	c := fake.NewClientset(
		stsWithClaim("db", "prod", 2),
		pvcFor("data-db-0", "staging", "standard", "10Gi", corev1.ClaimBound),
	)
	var buf bytes.Buffer
	if err := PvcUnused(context.Background(), clients(c), kube.Flags{}, nil, &buf); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "ORPHAN") {
		t.Fatalf("a StatefulSet in another namespace must not own the claim:\n%s", buf.String())
	}
}

// TestPvcUnusedCountsTerminatingPods keeps a claim mounted by a pod on its way
// out out of the list: its volume is still attached until the pod is gone.
func TestPvcUnusedCountsTerminatingPods(t *testing.T) {
	pod := podMounting("web", "app", "data")
	now := metav1.Now()
	pod.DeletionTimestamp = &now
	c := fake.NewClientset(pvcFor("data", "app", "standard", "10Gi", corev1.ClaimBound), pod)
	var buf bytes.Buffer
	if err := PvcUnused(context.Background(), clients(c), kube.Flags{}, nil, &buf); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(buf.String(), "data") {
		t.Fatalf("a claim held by a terminating pod is still in use:\n%s", buf.String())
	}
}

func TestPvcUnusedColor(t *testing.T) {
	c := fake.NewClientset(
		pvcFor("forgotten", "app", "standard", "100Gi", corev1.ClaimBound),
		stsWithClaim("db", "app", 1),
		pvcFor("data-db-0", "app", "standard", "10Gi", corev1.ClaimBound),
		pvcFor("waiting", "app", "standard", "5Gi", corev1.ClaimPending),
	)
	var buf bytes.Buffer
	if err := PvcUnused(context.Background(), clients(c), kube.Flags{Color: true}, nil, &buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{"\x1b[31mORPHAN", "\x1b[33mSTS-RESERVED", "\x1b[90mUNBOUND", "\x1b[32mBound"} {
		if !strings.Contains(out, want) {
			t.Fatalf("output missing colored %q:\n%q", want, out)
		}
	}
}
