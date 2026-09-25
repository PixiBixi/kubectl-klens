package view

import (
	"bytes"
	"context"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/PixiBixi/kubectl-klens/internal/kube"
)

func npPod(ns, name string, lbl map[string]string) *corev1.Pod {
	return &corev1.Pod{
		Namespace: ns, Name: name, Labels: lbl,
		Status: corev1.PodStatus{Phase: corev1.PodRunning},
	}
}

func npPolicyObj(ns, name string, sel map[string]string, spec networkingv1.NetworkPolicySpec) *networkingv1.NetworkPolicy {
	spec.PodSelector = metav1.LabelSelector{MatchLabels: sel}
	return &networkingv1.NetworkPolicy{Namespace: ns, Name: name, Spec: spec}
}

var (
	npFromApp = []networkingv1.NetworkPolicyIngressRule{{From: []networkingv1.NetworkPolicyPeer{{
		PodSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "front"}},
	}}}}
	npBothTypes = []networkingv1.PolicyType{networkingv1.PolicyTypeIngress, networkingv1.PolicyTypeEgress}
)

// npRow returns the output line whose first field is key.
func npRow(t *testing.T, out, key string) []string {
	t.Helper()
	for line := range strings.SplitSeq(out, "\n") {
		if f := strings.Fields(line); len(f) > 0 && f[0] == key {
			return f
		}
	}
	t.Fatalf("no row %q in:\n%s", key, out)
	return nil
}

func TestPolicyTypesDefaulting(t *testing.T) {
	egressRule := []networkingv1.NetworkPolicyEgressRule{{}}
	cases := []struct {
		name        string
		spec        networkingv1.NetworkPolicySpec
		wantIn, wEg bool
	}{
		{"implicit ingress only", networkingv1.NetworkPolicySpec{}, true, false},
		{"implicit egress from rules", networkingv1.NetworkPolicySpec{Egress: egressRule}, true, true},
		{"explicit egress only", networkingv1.NetworkPolicySpec{PolicyTypes: []networkingv1.PolicyType{networkingv1.PolicyTypeEgress}}, false, true},
	}
	for _, tc := range cases {
		in, eg := policyTypes(tc.spec)
		if in != tc.wantIn || eg != tc.wEg {
			t.Errorf("%s: policyTypes = (%v,%v), want (%v,%v)", tc.name, in, eg, tc.wantIn, tc.wEg)
		}
	}
}

func TestAllowsAnyPeer(t *testing.T) {
	world := []networkingv1.NetworkPolicyPeer{{IPBlock: &networkingv1.IPBlock{CIDR: "0.0.0.0/0"}}}
	worldExcept := []networkingv1.NetworkPolicyPeer{{IPBlock: &networkingv1.IPBlock{CIDR: "0.0.0.0/0", Except: []string{"10.0.0.0/8"}}}}
	cases := []struct {
		name  string
		peers []networkingv1.NetworkPolicyPeer
		ports int
		want  bool
	}{
		{"empty rule", nil, 0, true},
		{"empty peers but ports", nil, 1, false},
		{"world cidr", world, 0, true},
		{"world with except", worldExcept, 0, false},
		{"pod selector", npFromApp[0].From, 0, false},
	}
	for _, tc := range cases {
		if got := allowsAnyPeer(tc.peers, tc.ports); got != tc.want {
			t.Errorf("%s: allowsAnyPeer = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestNetpolNamespaces(t *testing.T) {
	c := newClientsetWithFieldSelectors(
		// default-deny both ways on every pod.
		npPod("locked", "a", nil),
		npPolicyObj("locked", "deny-all", nil, networkingv1.NetworkPolicySpec{PolicyTypes: npBothTypes}),
		// Only one of two pods covered.
		npPod("half", "api", map[string]string{"app": "api"}),
		npPod("half", "worker", map[string]string{"app": "worker"}),
		npPolicyObj("half", "api", map[string]string{"app": "api"}, networkingv1.NetworkPolicySpec{Ingress: npFromApp}),
		// Selects everything but allows everything: must not read as covered.
		npPod("fake", "a", nil),
		npPolicyObj("fake", "allow", nil, networkingv1.NetworkPolicySpec{Ingress: []networkingv1.NetworkPolicyIngressRule{{}}}),
		// No policy at all; the hostNetwork and finished pods are not counted.
		npPod("bare", "a", nil),
		&corev1.Pod{Namespace: "bare", Name: "host", Spec: corev1.PodSpec{HostNetwork: true}},
		&corev1.Pod{Namespace: "bare", Name: "done", Status: corev1.PodStatus{Phase: corev1.PodSucceeded}},
		// A policy with nothing to select.
		npPolicyObj("empty", "p", nil, networkingv1.NetworkPolicySpec{}),
	)
	var buf bytes.Buffer
	if err := Netpol(context.Background(), clients(c), kube.Flags{}, nil, &buf); err != nil {
		t.Fatal(err)
	}
	assertFieldSelector(t, c, "pods", npEligibleSelector)
	out := buf.String()
	want := map[string][]string{
		"locked": {"locked", "1", "1", "DEFAULT-DENY", "DEFAULT-DENY"},
		"half":   {"half", "1", "2", "PARTIAL", "1/2", "OPEN"},
		"fake":   {"fake", "1", "1", "ALLOW-ALL", "OPEN"},
		"bare":   {"bare", "0", "1", "OPEN", "OPEN"},
		"empty":  {"empty", "1", "0", "NO-PODS", "NO-PODS"},
	}
	for ns, fields := range want {
		if got := npRow(t, out, ns); strings.Join(got, " ") != strings.Join(fields, " ") {
			t.Errorf("row %s = %v, want %v", ns, got, fields)
		}
	}
	// Default ingress sort, worst-first at the bottom: OPEN is the last row.
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if last := strings.Fields(lines[len(lines)-1])[0]; last != "bare" {
		t.Fatalf("want bare (OPEN) last, got %s:\n%s", last, out)
	}
}

func TestNetpolPods(t *testing.T) {
	c := newClientsetWithFieldSelectors(
		npPod("shop", "api", map[string]string{"app": "api"}),
		npPod("shop", "cron", map[string]string{"app": "cron"}),
		&corev1.Pod{Namespace: "shop", Name: "agent", Spec: corev1.PodSpec{HostNetwork: true}},
		npPolicyObj("shop", "api-in", map[string]string{"app": "api"}, networkingv1.NetworkPolicySpec{Ingress: npFromApp}),
		npPolicyObj("shop", "egress-deny", nil, networkingv1.NetworkPolicySpec{PolicyTypes: []networkingv1.PolicyType{networkingv1.PolicyTypeEgress}}),
		&corev1.Pod{Namespace: "shop", Name: "finished", Status: corev1.PodStatus{Phase: corev1.PodFailed}},
	)
	var buf bytes.Buffer
	if err := Netpol(context.Background(), clients(c), kube.Flags{Namespace: "shop"}, nil, &buf); err != nil {
		t.Fatal(err)
	}
	// hostNetwork pods stay in the per-pod listing; only finished ones are dropped.
	assertFieldSelector(t, c, "pods", npLiveSelector)
	out := buf.String()
	if strings.Contains(out, "finished") {
		t.Fatalf("finished pod must not be listed:\n%s", out)
	}
	if !strings.HasPrefix(out, "NS") || !strings.Contains(strings.SplitN(out, "\n", 2)[0], "POD") {
		t.Fatalf("want the per-pod table for a single namespace:\n%s", out)
	}
	rows := map[string]string{}
	for line := range strings.SplitSeq(out, "\n") {
		if f := strings.Fields(line); len(f) == 5 {
			rows[f[1]] = strings.Join(f[2:], " ")
		}
	}
	want := map[string]string{
		"api":   "RESTRICTED DENY api-in,egress-deny",
		"cron":  "OPEN DENY egress-deny",
		"agent": "HOST-NETWORK HOST-NETWORK -",
	}
	for pod, w := range want {
		if rows[pod] != w {
			t.Errorf("pod %s = %q, want %q:\n%s", pod, rows[pod], w, out)
		}
	}
}

func TestNetpolColor(t *testing.T) {
	c := fake.NewClientset(
		npPod("app", "a", nil),
		npPod("kube-system", "dns", nil),
		npPod("edge", "lb", nil),
		npPolicyObj("edge", "public", nil, networkingv1.NetworkPolicySpec{Ingress: []networkingv1.NetworkPolicyIngressRule{{}}}),
	)
	var buf bytes.Buffer
	if err := Netpol(context.Background(), clients(c), kube.Flags{Color: true}, nil, &buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{
		"\x1b[31mOPEN\x1b[0m",      // red on a workload namespace
		"\x1b[90mOPEN\x1b[0m",      // muted system namespace
		"\x1b[33mALLOW-ALL\x1b[0m", // yellow: often a legit internet-facing pod
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing colored token %q:\n%q", want, out)
		}
	}
	if strings.Contains(out, "\x1b[33mOPEN") {
		t.Fatalf("OPEN must share one color across INGRESS and EGRESS:\n%q", out)
	}
}
