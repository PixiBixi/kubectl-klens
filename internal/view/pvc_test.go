package view

import (
	"bytes"
	"context"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/PixiBixi/kubectl-klens/internal/kube"
)

func TestPvc(t *testing.T) {
	withPVC := &corev1.Pod{
		Name: "db", Namespace: "data",
		Spec: corev1.PodSpec{
			NodeName: "n1",
			Volumes: []corev1.Volume{{
				Name:                  "store",
				PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: "db-data"},
			}},
		},
	}
	noPVC := &corev1.Pod{
		Name: "web", Namespace: "data",
		Spec: corev1.PodSpec{
			NodeName: "n1",
			Volumes:  []corev1.Volume{{Name: "tmp", EmptyDir: &corev1.EmptyDirVolumeSource{}}},
		},
	}
	fast := "fast-ssd"
	claim := &corev1.PersistentVolumeClaim{
		Name: "db-data", Namespace: "data",
		Spec: corev1.PersistentVolumeClaimSpec{StorageClassName: &fast},
		Status: corev1.PersistentVolumeClaimStatus{
			Capacity: corev1.ResourceList{corev1.ResourceStorage: resource.MustParse("20Gi")},
		},
	}
	c := fake.NewClientset(withPVC, noPVC, claim)
	var buf bytes.Buffer
	if err := Pvc(context.Background(), clients(c), kube.Flags{}, nil, &buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "db-data") || !strings.Contains(out, "n1") {
		t.Fatalf("missing pvc binding:\n%s", out)
	}
	if !strings.Contains(out, "CLASS") || !strings.Contains(out, "fast-ssd") {
		t.Fatalf("missing storage class:\n%s", out)
	}
	if !strings.Contains(out, "CAPACITY") || !strings.Contains(out, "20Gi") {
		t.Fatalf("missing capacity:\n%s", out)
	}
	if strings.Contains(out, "web") {
		t.Fatalf("pod without a PVC must not appear:\n%s", out)
	}
}

func TestPvcClassFallbacks(t *testing.T) {
	pod := &corev1.Pod{
		Name: "app", Namespace: "data",
		Spec: corev1.PodSpec{
			NodeName: "n1",
			Volumes: []corev1.Volume{
				{Name: "d", PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: "defaulted"}},
				{Name: "m", PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: "missing"}},
			},
		},
	}
	// No status capacity: not bound yet, so the requested size is all there is.
	defaulted := &corev1.PersistentVolumeClaim{
		Name: "defaulted", Namespace: "data",
		Spec: corev1.PersistentVolumeClaimSpec{
			Resources: corev1.VolumeResourceRequirements{
				Requests: corev1.ResourceList{corev1.ResourceStorage: resource.MustParse("5Gi")},
			},
		},
	}
	c := fake.NewClientset(pod, defaulted)
	var buf bytes.Buffer
	if err := Pvc(context.Background(), clients(c), kube.Flags{}, nil, &buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "<default>") {
		t.Fatalf("a claim with no storageClassName must read <default>:\n%s", out)
	}
	if !strings.Contains(out, "5Gi") {
		t.Fatalf("an unbound claim must fall back to its requested size:\n%s", out)
	}
	if !strings.Contains(out, "missing") || !strings.Contains(out, "-") {
		t.Fatalf("a claim that does not exist must still list with a dash:\n%s", out)
	}
}
