package view

import (
	"bytes"
	"context"
	"strings"
	"testing"

	policyv1 "k8s.io/api/policy/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/PixiBixi/kubectl-klens/internal/kube"
)

func TestPdbVerdict(t *testing.T) {
	cases := []struct {
		name        string
		status      policyv1.PodDisruptionBudgetStatus
		wantVerdict string
		wantSev     string
	}{
		{"orphan", policyv1.PodDisruptionBudgetStatus{ExpectedPods: 0}, "ORPHAN", "muted"},
		{"no-guard", policyv1.PodDisruptionBudgetStatus{ExpectedPods: 3, DesiredHealthy: 0, CurrentHealthy: 3, DisruptionsAllowed: 3}, "NO-GUARD", "bad"},
		{"single replica zero floor stays ok", policyv1.PodDisruptionBudgetStatus{ExpectedPods: 1, DesiredHealthy: 0, CurrentHealthy: 1, DisruptionsAllowed: 1}, "OK", "ok"},
		{"permablock equal", policyv1.PodDisruptionBudgetStatus{ExpectedPods: 3, DesiredHealthy: 3, CurrentHealthy: 3}, "PERMABLOCK", "bad"},
		{"permablock greater", policyv1.PodDisruptionBudgetStatus{ExpectedPods: 3, DesiredHealthy: 4}, "PERMABLOCK", "bad"},
		{"blocked", policyv1.PodDisruptionBudgetStatus{ExpectedPods: 5, DesiredHealthy: 3, CurrentHealthy: 2, DisruptionsAllowed: 0}, "BLOCKED", "bad"},
		{"at-floor equal", policyv1.PodDisruptionBudgetStatus{ExpectedPods: 5, DesiredHealthy: 3, CurrentHealthy: 3, DisruptionsAllowed: 0}, "AT-FLOOR", "warn"},
		{"at-floor above", policyv1.PodDisruptionBudgetStatus{ExpectedPods: 5, DesiredHealthy: 3, CurrentHealthy: 4, DisruptionsAllowed: 0}, "AT-FLOOR", "warn"},
		{"ok one", policyv1.PodDisruptionBudgetStatus{ExpectedPods: 5, DesiredHealthy: 3, CurrentHealthy: 4, DisruptionsAllowed: 1}, "OK", "ok"},
		{"ok many", policyv1.PodDisruptionBudgetStatus{ExpectedPods: 5, DesiredHealthy: 1, CurrentHealthy: 5, DisruptionsAllowed: 4}, "OK", "ok"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotV, gotS := pdbVerdict(tc.status)
			if gotV != tc.wantVerdict || gotS != tc.wantSev {
				t.Fatalf("pdbVerdict(%+v) = (%q,%q), want (%q,%q)", tc.status, gotV, gotS, tc.wantVerdict, tc.wantSev)
			}
		})
	}
}

func pdbFixture(name string, min *intstr.IntOrString, st policyv1.PodDisruptionBudgetStatus) *policyv1.PodDisruptionBudget {
	return &policyv1.PodDisruptionBudget{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
		Spec:       policyv1.PodDisruptionBudgetSpec{MinAvailable: min},
		Status:     st,
	}
}

func TestPdb(t *testing.T) {
	five := intstr.FromInt32(5)
	pct := intstr.FromString("50%")
	permablock := pdbFixture("kafka", &five, policyv1.PodDisruptionBudgetStatus{ExpectedPods: 5, DesiredHealthy: 5, CurrentHealthy: 4, DisruptionsAllowed: 0})
	ok := pdbFixture("api", &pct, policyv1.PodDisruptionBudgetStatus{ExpectedPods: 4, DesiredHealthy: 2, CurrentHealthy: 4, DisruptionsAllowed: 2})
	c := fake.NewClientset(permablock, ok)

	var buf bytes.Buffer
	if err := Pdb(context.Background(), c, kube.Flags{Namespace: "default"}, nil, &buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{"PERMABLOCK", "OK", "min=5", "min=50%"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q:\n%s", want, out)
		}
	}
	// Default verdict sort: least-risky first, so OK (api) ranks before
	// PERMABLOCK (kafka), which sinks toward the prompt.
	if strings.Index(out, "kafka") < strings.Index(out, "api") {
		t.Fatalf("expected verdict-sort default (api before kafka):\n%s", out)
	}
}

func TestPdbColor(t *testing.T) {
	five := intstr.FromInt32(5)
	blocked := pdbFixture("kafka", &five, policyv1.PodDisruptionBudgetStatus{ExpectedPods: 5, DesiredHealthy: 5, CurrentHealthy: 4, DisruptionsAllowed: 0})
	orphan := pdbFixture("stale", &five, policyv1.PodDisruptionBudgetStatus{ExpectedPods: 0})
	ok := pdbFixture("api", &five, policyv1.PodDisruptionBudgetStatus{ExpectedPods: 4, DesiredHealthy: 2, CurrentHealthy: 4, DisruptionsAllowed: 2})
	c := fake.NewClientset(blocked, orphan, ok)

	var buf bytes.Buffer
	if err := Pdb(context.Background(), c, kube.Flags{Namespace: "default", Color: true}, nil, &buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{
		"\x1b[31mPERMABLOCK\x1b[0m", // red
		"\x1b[90mORPHAN\x1b[0m",     // gray
		"\x1b[32mOK\x1b[0m",         // green
		"\x1b[32m2\x1b[0m",          // green ALLOWED on the OK pdb
		"\x1b[31m0\x1b[0m",          // red ALLOWED on the blocked pdb
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing colored token %q:\n%s", want, out)
		}
	}
}
