package view

import (
	"bytes"
	"context"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/PixiBixi/kubectl-klens/internal/kube"
)

func svcFor(name, ns string, typ corev1.ServiceType, selector map[string]string) *corev1.Service {
	return &corev1.Service{
		Name: name, Namespace: ns,
		Spec: corev1.ServiceSpec{Type: typ, Selector: selector},
	}
}

// sliceFor builds an EndpointSlice for a service; each entry of ready becomes
// one endpoint, backed by a distinct pod targetRef.
func sliceFor(svc, ns string, addrType discoveryv1.AddressType, ready ...bool) *discoveryv1.EndpointSlice {
	es := &discoveryv1.EndpointSlice{
		Name:        svc + "-" + string(addrType),
		Namespace:   ns,
		Labels:      map[string]string{discoveryv1.LabelServiceName: svc},
		AddressType: addrType,
	}
	for i := range ready {
		pod := svc + "-pod-" + string(rune('a'+i))
		es.Endpoints = append(es.Endpoints, discoveryv1.Endpoint{
			Addresses:  []string{"10.0.0." + string(rune('1'+i))},
			Conditions: discoveryv1.EndpointConditions{Ready: &ready[i]},
			TargetRef:  &corev1.ObjectReference{Kind: "Pod", Namespace: ns, Name: pod, UID: types.UID(pod)},
		})
	}
	return es
}

func TestSvcBackends(t *testing.T) {
	sel := map[string]string{"app": "web"}
	c := fake.NewClientset(
		svcFor("healthy", "app", corev1.ServiceTypeClusterIP, sel),
		sliceFor("healthy", "app", discoveryv1.AddressTypeIPv4, true, true),
		svcFor("rolling", "app", corev1.ServiceTypeClusterIP, sel),
		sliceFor("rolling", "app", discoveryv1.AddressTypeIPv4, true, false),
		svcFor("unready", "app", corev1.ServiceTypeClusterIP, sel),
		sliceFor("unready", "app", discoveryv1.AddressTypeIPv4, false),
		svcFor("typo", "app", corev1.ServiceTypeClusterIP, map[string]string{"app": "wbe"}),
		svcFor("alias", "app", corev1.ServiceTypeExternalName, nil),
		svcFor("noselector", "app", corev1.ServiceTypeClusterIP, nil),
	)
	var buf bytes.Buffer
	if err := SvcBackends(context.Background(), clients(c), kube.Flags{}, nil, &buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{
		"NS", "SERVICE", "TYPE", "SELECTOR", "READY", "NOTREADY", "VERDICT",
		"healthy", "OK", "1 label",
		"rolling", "DEGRADED",
		"unready", "NO-READY",
		"typo", "NO-PODS", "app=wbe",
		"alias", "EXTERNAL",
		"noselector", "UNWIRED", "<none>",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("output missing %q:\n%s", want, out)
		}
	}
}

// TestSvcBackendsSelectorOnlyWhenBroken pins the readability rule: a healthy
// row shows a label count (a Helm selector is 110 columns of nothing and wraps
// the table), a NO-PODS row shows the selector, which is where the typo is.
func TestSvcBackendsSelectorOnlyWhenBroken(t *testing.T) {
	c := fake.NewClientset(
		svcFor("healthy", "app", corev1.ServiceTypeClusterIP, map[string]string{
			"app.kubernetes.io/name": "web", "app.kubernetes.io/instance": "web",
		}),
		sliceFor("healthy", "app", discoveryv1.AddressTypeIPv4, true),
		svcFor("typo", "app", corev1.ServiceTypeClusterIP, map[string]string{"app.kubernetes.io/name": "wbe"}),
	)
	var buf bytes.Buffer
	if err := SvcBackends(context.Background(), clients(c), kube.Flags{}, nil, &buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "2 labels") {
		t.Fatalf("a healthy row must show the label count:\n%s", out)
	}
	if strings.Contains(out, "app.kubernetes.io/name=web") {
		t.Fatalf("a healthy row must not spell the selector out:\n%s", out)
	}
	if !strings.Contains(out, "app.kubernetes.io/name=wbe") {
		t.Fatalf("a NO-PODS row must spell the selector out:\n%s", out)
	}
}

// TestSvcBackendsDualStackCountsPodsOnce guards the dedup: a dual-stack service
// has one slice per address family listing the same pods, and counting slice
// entries would report twice the backends it has.
func TestSvcBackendsDualStackCountsPodsOnce(t *testing.T) {
	c := fake.NewClientset(
		svcFor("web", "app", corev1.ServiceTypeClusterIP, map[string]string{"app": "web"}),
		sliceFor("web", "app", discoveryv1.AddressTypeIPv4, true, true),
		sliceFor("web", "app", discoveryv1.AddressTypeIPv6, true, true),
	)
	var buf bytes.Buffer
	if err := SvcBackends(context.Background(), clients(c), kube.Flags{}, nil, &buf); err != nil {
		t.Fatal(err)
	}
	fields := strings.Fields(strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")[1])
	// The last three columns are READY, NOTREADY, VERDICT; counting from the end
	// keeps this from breaking on a SELECTOR cell that contains a space.
	if ready := fields[len(fields)-3]; ready != "2" {
		t.Fatalf("READY = %s, want 2 (one row per pod, not per slice):\n%s", ready, buf.String())
	}
}

// TestSvcBackendsManualEndpoints covers a selector-less service whose endpoints
// another controller fills in: unwired only when nothing answers.
func TestSvcBackendsManualEndpoints(t *testing.T) {
	es := &discoveryv1.EndpointSlice{
		Name: "external-db-1", Namespace: "app",
		Labels:      map[string]string{discoveryv1.LabelServiceName: "external-db"},
		AddressType: discoveryv1.AddressTypeIPv4,
		Endpoints:   []discoveryv1.Endpoint{{Addresses: []string{"192.168.1.10"}}},
	}
	c := fake.NewClientset(svcFor("external-db", "app", corev1.ServiceTypeClusterIP, nil), es)
	var buf bytes.Buffer
	if err := SvcBackends(context.Background(), clients(c), kube.Flags{}, nil, &buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "MANUAL") {
		t.Fatalf("want MANUAL for hand-managed endpoints:\n%s", out)
	}
	// A nil ready condition means ready by API convention.
	if !strings.Contains(out, "external-db") || strings.Contains(out, "NO-READY") {
		t.Fatalf("nil ready condition must count as ready:\n%s", out)
	}
}

func TestSvcBackendsColor(t *testing.T) {
	sel := map[string]string{"app": "web"}
	c := fake.NewClientset(
		svcFor("healthy", "app", corev1.ServiceTypeClusterIP, sel),
		sliceFor("healthy", "app", discoveryv1.AddressTypeIPv4, true),
		svcFor("rolling", "app", corev1.ServiceTypeClusterIP, sel),
		sliceFor("rolling", "app", discoveryv1.AddressTypeIPv4, true, false),
		svcFor("typo", "app", corev1.ServiceTypeClusterIP, map[string]string{"app": "wbe"}),
	)
	var buf bytes.Buffer
	if err := SvcBackends(context.Background(), clients(c), kube.Flags{Color: true}, nil, &buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{"\x1b[32mOK", "\x1b[33mDEGRADED", "\x1b[31mNO-PODS", "\x1b[31m0"} {
		if !strings.Contains(out, want) {
			t.Fatalf("output missing colored %q:\n%q", want, out)
		}
	}
}
