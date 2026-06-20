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

func TestProbesVerdict(t *testing.T) {
	cases := []struct {
		name             string
		hasR, hasL       bool
		wantVerdict, sev string
	}{
		{"neither", false, false, "NO-PROBES", "bad"},
		{"no readiness", false, true, "NO-READINESS", "bad"},
		{"no liveness", true, false, "NO-LIVENESS", "warn"},
		{"both", true, true, "OK", "ok"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotV, gotS := probesVerdict(tc.hasR, tc.hasL)
			if gotV != tc.wantVerdict || gotS != tc.sev {
				t.Fatalf("probesVerdict(%v,%v) = (%q,%q), want (%q,%q)", tc.hasR, tc.hasL, gotV, gotS, tc.wantVerdict, tc.sev)
			}
		})
	}
}

func httpProbe() *corev1.Probe {
	return &corev1.Probe{ProbeHandler: corev1.ProbeHandler{HTTPGet: &corev1.HTTPGetAction{Path: "/healthz"}}}
}

func probePod(name, ns string, owner *metav1.OwnerReference, ctr corev1.Container) *corev1.Pod {
	p := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
		Spec:       corev1.PodSpec{Containers: []corev1.Container{ctr}},
	}
	if owner != nil {
		p.OwnerReferences = []metav1.OwnerReference{*owner}
	}
	return p
}

func jobOwner() *metav1.OwnerReference {
	ctrl := true
	return &metav1.OwnerReference{APIVersion: "batch/v1", Kind: "Job", Name: "backup", Controller: &ctrl}
}

func TestProbes(t *testing.T) {
	healthy := probePod("api", "default", nil, corev1.Container{
		Name:           "app",
		ReadinessProbe: httpProbe(),
		LivenessProbe:  httpProbe(),
	})
	noReadiness := probePod("worker", "default", nil, corev1.Container{
		Name:          "app",
		LivenessProbe: httpProbe(),
	})
	noProbes := probePod("cache", "default", nil, corev1.Container{Name: "redis"})
	batch := probePod("backup-xyz", "default", jobOwner(), corev1.Container{Name: "dump"})
	system := probePod("kube-dns", "kube-system", nil, corev1.Container{Name: "dns"})

	c := fake.NewClientset(healthy, noReadiness, noProbes, batch, system)

	var buf bytes.Buffer
	if err := Probes(context.Background(), c, kube.Flags{}, nil, &buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()

	for _, want := range []string{"OK", "NO-READINESS", "NO-PROBES", "http"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q:\n%s", want, out)
		}
	}
	// Job-owned and kube-system pods are excluded.
	for _, excluded := range []string{"backup-xyz", "kube-dns"} {
		if strings.Contains(out, excluded) {
			t.Fatalf("expected %q excluded:\n%s", excluded, out)
		}
	}
	// Default verdict sort: least-risky first, so OK ranks before the bad
	// verdicts (NO-READINESS, NO-PROBES) which sink toward the prompt.
	if strings.Index(out, "OK") > strings.Index(out, "NO-READINESS") {
		t.Fatalf("expected verdict-sort default (OK before bad):\n%s", out)
	}
}

func TestProbesSortVerdict(t *testing.T) {
	healthy := probePod("api", "default", nil, corev1.Container{Name: "app", ReadinessProbe: httpProbe(), LivenessProbe: httpProbe()})
	noReadiness := probePod("worker", "default", nil, corev1.Container{Name: "app", LivenessProbe: httpProbe()})
	noLiveness := probePod("front", "default", nil, corev1.Container{Name: "nginx", ReadinessProbe: httpProbe()})
	noProbes := probePod("cache", "default", nil, corev1.Container{Name: "redis"})
	c := fake.NewClientset(healthy, noReadiness, noLiveness, noProbes)

	var buf bytes.Buffer
	if err := Probes(context.Background(), c, kube.Flags{Sort: "verdict"}, nil, &buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	// --sort verdict orders by explicit risk, least-risky first, so the riskiest
	// rows land at the bottom (nearest the prompt). NO-PROBES (no probes at all)
	// outranks NO-READINESS, so it sits dead last.
	order := []string{"OK", "NO-LIVENESS", "NO-READINESS", "NO-PROBES"}
	for i := 0; i+1 < len(order); i++ {
		if strings.Index(out, order[i]) > strings.Index(out, order[i+1]) {
			t.Fatalf("want %q before %q under --sort verdict:\n%s", order[i], order[i+1], out)
		}
	}
}

func TestProbesColor(t *testing.T) {
	healthy := probePod("api", "default", nil, corev1.Container{
		Name:           "app",
		ReadinessProbe: httpProbe(),
		LivenessProbe:  httpProbe(),
	})
	noReadiness := probePod("worker", "default", nil, corev1.Container{
		Name:          "app",
		LivenessProbe: httpProbe(),
	})
	noLiveness := probePod("front", "default", nil, corev1.Container{
		Name:           "nginx",
		ReadinessProbe: httpProbe(),
	})
	c := fake.NewClientset(healthy, noReadiness, noLiveness)

	var buf bytes.Buffer
	if err := Probes(context.Background(), c, kube.Flags{Color: true}, nil, &buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{
		"\x1b[31mNO-READINESS\x1b[0m", // red
		"\x1b[33mNO-LIVENESS\x1b[0m",  // yellow
		"\x1b[32mOK\x1b[0m",           // green
		"\x1b[32mhttp\x1b[0m",         // green present handler
		"\x1b[90m-\x1b[0m",            // muted absent handler
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing colored token %q:\n%s", want, out)
		}
	}
}
