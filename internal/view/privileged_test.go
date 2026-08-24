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

func TestPrivileged(t *testing.T) {
	yes := true
	c := fake.NewClientset(
		&corev1.Pod{
			Name: "safe", Namespace: "default",
			Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "app"}}},
		},
		&corev1.Pod{
			Name: "priv", Namespace: "default",
			Spec: corev1.PodSpec{Containers: []corev1.Container{
				{Name: "app", SecurityContext: &corev1.SecurityContext{Privileged: &yes}},
			}},
		},
		&corev1.Pod{
			Name: "host", Namespace: "default",
			Spec: corev1.PodSpec{
				HostNetwork: true,
				Containers:  []corev1.Container{{Name: "app"}},
			},
		},
	)
	var buf bytes.Buffer
	if err := Privileged(context.Background(), clients(c), kube.Flags{}, nil, &buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if strings.Contains(out, "safe") {
		t.Fatalf("container with no security flags must be omitted:\n%s", out)
	}
	if !strings.Contains(out, "priv") || !strings.Contains(out, "privileged") {
		t.Fatalf("want 'priv' flagged privileged:\n%s", out)
	}
	if !strings.Contains(out, "host") || !strings.Contains(out, "hostNetwork") {
		t.Fatalf("want 'host' flagged hostNetwork:\n%s", out)
	}
}

// TestPrivilegedInitAndEphemeral guards the blind spot that used to hide a
// privileged init container: it escalates exactly as far as an app container.
func TestPrivilegedInitAndEphemeral(t *testing.T) {
	yes := true
	c := fake.NewClientset(&corev1.Pod{
		Name: "tuned", Namespace: "default",
		Spec: corev1.PodSpec{
			InitContainers: []corev1.Container{
				{Name: "sysctl-tuner", SecurityContext: &corev1.SecurityContext{Privileged: &yes}},
			},
			Containers: []corev1.Container{{Name: "app"}},
			EphemeralContainers: []corev1.EphemeralContainer{{
				Name:            "debugger",
				SecurityContext: &corev1.SecurityContext{Privileged: &yes},
			}},
		},
	})
	var buf bytes.Buffer
	if err := Privileged(context.Background(), clients(c), kube.Flags{}, nil, &buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{"sysctl-tuner", "init", "debugger", "eph"} {
		if !strings.Contains(out, want) {
			t.Fatalf("want %q in output:\n%s", want, out)
		}
	}
	// The unflagged app container must still not produce a row.
	if strings.Contains(out, "\napp ") {
		t.Fatalf("clean app container must be omitted:\n%s", out)
	}
}

// TestPrivilegedPrivEscDefaultEnrichesOnly locks in the chosen trade-off: an
// unset allowPrivilegeEscalation is reported as context on rows that already
// have a finding, but never creates a row on its own - otherwise the command
// would match nearly every container in a normal cluster.
func TestPrivilegedPrivEscDefaultEnrichesOnly(t *testing.T) {
	yes := true
	c := fake.NewClientset(
		&corev1.Pod{ // no securityContext at all: privesc defaults to true
			Name: "ordinary", Namespace: "default",
			Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "app"}}},
		},
		&corev1.Pod{ // has a real finding, so the default is worth naming
			Name: "priv", Namespace: "default",
			Spec: corev1.PodSpec{Containers: []corev1.Container{
				{Name: "app", SecurityContext: &corev1.SecurityContext{Privileged: &yes}},
			}},
		},
	)
	var buf bytes.Buffer
	if err := Privileged(context.Background(), clients(c), kube.Flags{}, nil, &buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if strings.Contains(out, "ordinary") {
		t.Fatalf("privesc-default alone must not create a row:\n%s", out)
	}
	if !strings.Contains(out, "privileged,"+privEscDefault) {
		t.Fatalf("want privesc-default noted on the flagged row:\n%s", out)
	}
}

func TestPrivilegedHostIPCAndCapabilities(t *testing.T) {
	c := fake.NewClientset(
		&corev1.Pod{
			Name: "ipc", Namespace: "default",
			Spec: corev1.PodSpec{
				HostIPC:    true,
				Containers: []corev1.Container{{Name: "app"}},
			},
		},
		&corev1.Pod{
			Name: "capped", Namespace: "default",
			Spec: corev1.PodSpec{Containers: []corev1.Container{{
				Name: "app",
				SecurityContext: &corev1.SecurityContext{Capabilities: &corev1.Capabilities{
					// Mixed spellings, plus a benign capability that must not match.
					Add: []corev1.Capability{"CAP_SYS_ADMIN", "net_admin", "NET_BIND_SERVICE"},
				}},
			}}},
		},
		&corev1.Pod{
			Name: "hostport", Namespace: "default",
			Spec: corev1.PodSpec{Containers: []corev1.Container{{
				Name:  "app",
				Ports: []corev1.ContainerPort{{ContainerPort: 8080, HostPort: 8080}},
			}}},
		},
	)
	var buf bytes.Buffer
	if err := Privileged(context.Background(), clients(c), kube.Flags{}, nil, &buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{"hostIPC", "caps=SYS_ADMIN+NET_ADMIN", "hostPort"} {
		if !strings.Contains(out, want) {
			t.Fatalf("want %q in output:\n%s", want, out)
		}
	}
	if strings.Contains(out, "NET_BIND_SERVICE") {
		t.Fatalf("benign capability must not be flagged:\n%s", out)
	}
}

func TestPrivilegedColor(t *testing.T) {
	yes := true
	c := fake.NewClientset(&corev1.Pod{
		Name: "priv", Namespace: "default",
		Spec: corev1.PodSpec{Containers: []corev1.Container{
			{Name: "app", SecurityContext: &corev1.SecurityContext{Privileged: &yes}},
		}},
	})
	var buf bytes.Buffer
	if err := Privileged(context.Background(), clients(c), kube.Flags{Color: true}, nil, &buf); err != nil {
		t.Fatal(err)
	}
	// Assert the FLAGS cell opens red without pinning the flag list, so adding a
	// finding to the row doesn't break the colour test.
	if !strings.Contains(buf.String(), "\x1b[31mprivileged") {
		t.Fatalf("flags not red:\n%s", buf.String())
	}
}
