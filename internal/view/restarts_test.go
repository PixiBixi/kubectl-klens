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

func podRestarts(name, container string, restarts int32, waitingReason string) *corev1.Pod {
	cs := corev1.ContainerStatus{Name: container, RestartCount: restarts}
	if waitingReason != "" {
		cs.State.Waiting = &corev1.ContainerStateWaiting{Reason: waitingReason}
	}
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
		Status:     corev1.PodStatus{ContainerStatuses: []corev1.ContainerStatus{cs}},
	}
}

func TestRestarts(t *testing.T) {
	c := fake.NewClientset(
		podRestarts("calm", "app", 0, ""),
		podRestarts("flaky", "app", 3, "CrashLoopBackOff"),
		podRestarts("worst", "app", 9, "CrashLoopBackOff"),
	)
	var buf bytes.Buffer
	if err := Restarts(context.Background(), c, kube.Flags{}, nil, &buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if strings.Contains(out, "calm") {
		t.Fatalf("zero-restart container must be omitted:\n%s", out)
	}
	if !strings.Contains(out, "CrashLoopBackOff") {
		t.Fatalf("want crash reason:\n%s", out)
	}
	// Sorted by restarts desc: worst (9) before flaky (3).
	if strings.Index(out, "worst") > strings.Index(out, "flaky") {
		t.Fatalf("want worst (9) before flaky (3):\n%s", out)
	}
}

func TestRestartsColor(t *testing.T) {
	c := fake.NewClientset(podRestarts("flaky", "app", 5, "CrashLoopBackOff"))
	var buf bytes.Buffer
	if err := Restarts(context.Background(), c, kube.Flags{Color: true}, nil, &buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "\x1b[33m5\x1b[0m") {
		t.Fatalf("restart count not yellow:\n%s", out)
	}
	if !strings.Contains(out, "\x1b[31mCrashLoopBackOff\x1b[0m") {
		t.Fatalf("crash reason not red:\n%s", out)
	}
}
