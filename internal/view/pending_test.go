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

func TestSchedulerCause(t *testing.T) {
	cases := []struct {
		name string
		msg  string
		want string
	}{
		{
			"dominant count wins",
			"0/5 nodes are available: 3 Insufficient cpu, 2 node(s) had untolerated taint {dedicated: gpu}. preemption: 0/5 nodes are available: 5 No preemption victims found.",
			"Insufficient cpu (3 nodes)",
		},
		{
			"single clause no preemption tail",
			"0/3 nodes are available: 3 Insufficient memory.",
			"Insufficient memory (3 nodes)",
		},
		{
			"taint blob stripped",
			"0/2 nodes are available: 2 node(s) had untolerated taint {dedicated: gpu}.",
			"node(s) had untolerated taint (2 nodes)",
		},
		{
			"unparseable falls back to trimmed message",
			"some unexpected scheduler wording without the marker",
			"some unexpected scheduler wording without the marker",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := schedulerCause(tc.msg); got != tc.want {
				t.Fatalf("schedulerCause(%q) = %q, want %q", tc.msg, got, tc.want)
			}
		})
	}
}

func TestPending(t *testing.T) {
	unsched := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "api", Namespace: "default"},
		Status: corev1.PodStatus{
			Phase: corev1.PodPending,
			Conditions: []corev1.PodCondition{{
				Type:    corev1.PodScheduled,
				Status:  corev1.ConditionFalse,
				Reason:  "Unschedulable",
				Message: "0/5 nodes are available: 3 Insufficient cpu, 2 node(s) had untolerated taint {x: y}.",
			}},
		},
	}
	badimg := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "worker", Namespace: "default"},
		Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "c", Image: "myreg/app:bad"}}},
		Status: corev1.PodStatus{
			Phase: corev1.PodPending,
			ContainerStatuses: []corev1.ContainerStatus{{
				Name:  "c",
				State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "ImagePullBackOff"}},
			}},
		},
	}
	running := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "live", Namespace: "default"},
		Status:     corev1.PodStatus{Phase: corev1.PodRunning},
	}
	c := fake.NewClientset(unsched, badimg, running)

	var buf bytes.Buffer
	if err := Pending(context.Background(), c, kube.Flags{Namespace: "default"}, nil, &buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{"Unschedulable", "Insufficient cpu (3 nodes)", "ImagePullBackOff", "myreg/app:bad"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "live") {
		t.Fatalf("running pod must be excluded:\n%s", out)
	}
}

func TestPendingColor(t *testing.T) {
	unsched := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "api", Namespace: "default"},
		Status: corev1.PodStatus{
			Phase: corev1.PodPending,
			Conditions: []corev1.PodCondition{{
				Type: corev1.PodScheduled, Status: corev1.ConditionFalse, Reason: "Unschedulable",
				Message: "0/1 nodes are available: 1 Insufficient cpu.",
			}},
		},
	}
	badimg := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "worker", Namespace: "default"},
		Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "c", Image: "x:bad"}}},
		Status: corev1.PodStatus{
			Phase: corev1.PodPending,
			ContainerStatuses: []corev1.ContainerStatus{{
				Name: "c", State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "ImagePullBackOff"}},
			}},
		},
	}
	c := fake.NewClientset(unsched, badimg)

	var buf bytes.Buffer
	if err := Pending(context.Background(), c, kube.Flags{Namespace: "default", Color: true}, nil, &buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{"\x1b[31mUnschedulable\x1b[0m", "\x1b[31mImagePullBackOff\x1b[0m"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing colored token %q:\n%s", want, out)
		}
	}
}
