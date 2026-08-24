package view

import (
	"bytes"
	"context"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"

	"github.com/PixiBixi/kubectl-klens/internal/kube"
)

// ingressFor builds a single-rule Ingress routing host+path to service:port,
// optionally with a TLS block.
func ingressFor(name, ns, host, path, svc string, port int32, tlsHost, tlsSecret string) *networkingv1.Ingress {
	ing := &networkingv1.Ingress{
		Name: name, Namespace: ns,
		Spec: networkingv1.IngressSpec{
			Rules: []networkingv1.IngressRule{{
				Host: host,
				HTTP: &networkingv1.HTTPIngressRuleValue{
					Paths: []networkingv1.HTTPIngressPath{{
						Path: path,
						Backend: networkingv1.IngressBackend{Service: &networkingv1.IngressServiceBackend{
							Name: svc,
							Port: networkingv1.ServiceBackendPort{Number: port},
						}},
					}},
				},
			}},
		},
	}
	if tlsHost != "" {
		ing.Spec.TLS = []networkingv1.IngressTLS{{Hosts: []string{tlsHost}, SecretName: tlsSecret}}
	}
	return ing
}

func svcWithPort(name, ns string, port int32, portName string) *corev1.Service {
	return &corev1.Service{
		Name: name, Namespace: ns,
		Spec: corev1.ServiceSpec{Ports: []corev1.ServicePort{{Name: portName, Port: port}}},
	}
}

func TestIngress(t *testing.T) {
	c := fake.NewClientset(
		svcWithPort("web", "app", 80, "http"),
		&corev1.Secret{Name: "web-tls", Namespace: "app"},
		ingressFor("ok", "app", "app.example.com", "/", "web", 80, "app.example.com", "web-tls"),
		ingressFor("plaintext", "app", "plain.example.com", "/", "web", 80, "", ""),
		ingressFor("missing-cert", "app", "cert.example.com", "/", "web", 80, "cert.example.com", "nope-tls"),
		ingressFor("renamed", "app", "old.example.com", "/", "gone", 80, "old.example.com", "web-tls"),
		ingressFor("wrong-port", "app", "port.example.com", "/", "web", 8080, "port.example.com", "web-tls"),
	)
	var buf bytes.Buffer
	if err := Ingress(context.Background(), clients(c), kube.Flags{}, nil, &buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{
		"NS", "INGRESS", "CLASS", "HOST", "PATH", "BACKEND", "TLS", "VERDICT",
		"ok", "web:80", "web-tls", "OK",
		"plaintext", "NO-TLS", "none",
		"missing-cert", "NO-SECRET",
		"renamed", "NO-SERVICE",
		"wrong-port", "NO-PORT",
		"<default>",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("output missing %q:\n%s", want, out)
		}
	}
}

// TestIngressWildcardCertCoversHost guards the TLS host matching: a
// *.example.com certificate covers app.example.com, and reporting NO-TLS there
// would be a false alarm on the most common setup there is.
func TestIngressWildcardCertCoversHost(t *testing.T) {
	c := fake.NewClientset(
		svcWithPort("web", "app", 80, "http"),
		&corev1.Secret{Name: "wildcard-tls", Namespace: "app"},
		ingressFor("wild", "app", "app.example.com", "/", "web", 80, "*.example.com", "wildcard-tls"),
		ingressFor("deeper", "app", "a.b.example.com", "/", "web", 80, "*.example.com", "wildcard-tls"),
	)
	var buf bytes.Buffer
	if err := Ingress(context.Background(), clients(c), kube.Flags{}, nil, &buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	var wild, deeper string
	for _, l := range lines {
		if strings.Contains(l, "app.example.com") {
			wild = l
		}
		if strings.Contains(l, "a.b.example.com") {
			deeper = l
		}
	}
	if !strings.Contains(wild, "OK") {
		t.Fatalf("wildcard must cover one label: %q", wild)
	}
	// A wildcard covers exactly one label, so a deeper name is not covered.
	if !strings.Contains(deeper, "NO-TLS") {
		t.Fatalf("wildcard must not cover two labels: %q", deeper)
	}
}

// TestIngressPortByName covers a backend referring to its port by name, which
// checking numbers only would report as NO-PORT.
func TestIngressPortByName(t *testing.T) {
	ing := ingressFor("named", "app", "app.example.com", "/", "web", 0, "", "")
	ing.Spec.Rules[0].HTTP.Paths[0].Backend.Service.Port = networkingv1.ServiceBackendPort{Name: "http"}
	c := fake.NewClientset(svcWithPort("web", "app", 80, "http"), ing)
	var buf bytes.Buffer
	if err := Ingress(context.Background(), clients(c), kube.Flags{}, nil, &buf); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(buf.String(), "NO-PORT") {
		t.Fatalf("a named port must match by name:\n%s", buf.String())
	}
	if !strings.Contains(buf.String(), "web:http") {
		t.Fatalf("want the named port in BACKEND:\n%s", buf.String())
	}
}

// TestIngressWithoutSecretAccess covers the common RBAC case: no read on
// secrets. The backend checks must still run, and the TLS name must still print.
func TestIngressWithoutSecretAccess(t *testing.T) {
	c := fake.NewClientset(
		svcWithPort("web", "app", 80, "http"),
		ingressFor("ok", "app", "app.example.com", "/", "web", 80, "app.example.com", "web-tls"),
	)
	c.PrependReactor("list", "secrets", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, apiForbidden()
	})
	var buf bytes.Buffer
	if err := Ingress(context.Background(), clients(c), kube.Flags{}, nil, &buf); err != nil {
		t.Fatalf("a forbidden secret list must not fail the command: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "web-tls") || strings.Contains(out, "NO-SECRET") {
		t.Fatalf("want the unverified secret name, no verdict on it:\n%s", out)
	}
}

// TestIngressDefaultBackend keeps a rule-less ingress visible: it still routes
// everything reaching its controller.
func TestIngressDefaultBackend(t *testing.T) {
	ing := &networkingv1.Ingress{
		Name: "catchall", Namespace: "app",
		Spec: networkingv1.IngressSpec{DefaultBackend: &networkingv1.IngressBackend{
			Service: &networkingv1.IngressServiceBackend{Name: "web", Port: networkingv1.ServiceBackendPort{Number: 80}},
		}},
	}
	c := fake.NewClientset(svcWithPort("web", "app", 80, "http"), ing)
	var buf bytes.Buffer
	if err := Ingress(context.Background(), clients(c), kube.Flags{}, nil, &buf); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "(default)") {
		t.Fatalf("want the default backend row:\n%s", buf.String())
	}
}

func TestIngressColor(t *testing.T) {
	c := fake.NewClientset(
		svcWithPort("web", "app", 80, "http"),
		&corev1.Secret{Name: "web-tls", Namespace: "app"},
		ingressFor("ok", "app", "app.example.com", "/", "web", 80, "app.example.com", "web-tls"),
		ingressFor("plaintext", "app", "plain.example.com", "/", "web", 80, "", ""),
		ingressFor("renamed", "app", "old.example.com", "/", "gone", 80, "old.example.com", "web-tls"),
	)
	var buf bytes.Buffer
	if err := Ingress(context.Background(), clients(c), kube.Flags{Color: true}, nil, &buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{"\x1b[32mOK", "\x1b[33mNO-TLS", "\x1b[31mNO-SERVICE"} {
		if !strings.Contains(out, want) {
			t.Fatalf("output missing colored %q:\n%q", want, out)
		}
	}
}
