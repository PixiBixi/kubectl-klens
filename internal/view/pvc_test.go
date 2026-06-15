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

func TestPvc(t *testing.T) {
	withPVC := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "db", Namespace: "data"},
		Spec: corev1.PodSpec{
			NodeName: "n1",
			Volumes: []corev1.Volume{{
				Name: "store",
				VolumeSource: corev1.VolumeSource{
					PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: "db-data"},
				},
			}},
		},
	}
	noPVC := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "data"},
		Spec: corev1.PodSpec{
			NodeName: "n1",
			Volumes:  []corev1.Volume{{Name: "tmp", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}}},
		},
	}
	c := fake.NewClientset(withPVC, noPVC)
	var buf bytes.Buffer
	if err := Pvc(context.Background(), c, kube.Flags{}, nil, &buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "db-data") || !strings.Contains(out, "n1") {
		t.Fatalf("missing pvc binding:\n%s", out)
	}
	if strings.Contains(out, "web") {
		t.Fatalf("pod without a PVC must not appear:\n%s", out)
	}
}
