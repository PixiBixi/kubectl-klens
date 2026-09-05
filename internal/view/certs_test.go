package view

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"slices"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/PixiBixi/kubectl-klens/internal/kube"
)

// certOpts describes a certificate to mint for a test. Zero values give a
// self-signed leaf valid for a year with no names at all.
type certOpts struct {
	cn       string
	dns      []string
	ips      []string
	notAfter time.Time
	issuerCN string
	issuerO  string
}

func mintCert(t *testing.T, o certOpts) []byte {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	if o.notAfter.IsZero() {
		o.notAfter = time.Now().Add(365 * 24 * time.Hour)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: o.cn},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     o.notAfter,
		DNSNames:     o.dns,
	}
	for _, ip := range o.ips {
		tmpl.IPAddresses = append(tmpl.IPAddresses, net.ParseIP(ip))
	}
	// x509 takes the issuer from the *parent's subject*, not from a template
	// field, so a distinct issuer needs a distinct parent. Passing tmpl as its
	// own parent is what makes a certificate self-signed.
	parent := tmpl
	if o.issuerCN != "" || o.issuerO != "" {
		parent = &x509.Certificate{
			SerialNumber: big.NewInt(2),
			Subject:      pkix.Name{CommonName: o.issuerCN, Organization: nonEmpty(o.issuerO)},
			NotBefore:    tmpl.NotBefore,
			NotAfter:     tmpl.NotAfter,
		}
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, parent, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}

func nonEmpty(s string) []string {
	if s == "" {
		return nil
	}
	return []string{s}
}

func tlsSecret(ns, name string, pemBytes []byte) *corev1.Secret {
	return &corev1.Secret{
		Name: name, Namespace: ns,
		Type: corev1.SecretTypeTLS,
		Data: map[string][]byte{corev1.TLSCertKey: pemBytes},
	}
}

func TestCertVerdict(t *testing.T) {
	now := time.Now()
	for _, tc := range []struct {
		name         string
		in           time.Duration
		verdict, sev string
	}{
		{"already past", -time.Hour, "EXPIRED", "bad"},
		{"expiring today", 12 * time.Hour, "EXPIRING", "bad"},
		{"just under a week", 6 * 24 * time.Hour, "EXPIRING", "bad"},
		{"renewal overdue", 10 * 24 * time.Hour, "RENEW-DUE", "warn"},
		{"just under a fortnight", 13 * 24 * time.Hour, "RENEW-DUE", "warn"},
		{"comfortable", 60 * 24 * time.Hour, "OK", "ok"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v, sev := certVerdict(now.Add(tc.in), now)
			if v != tc.verdict || sev != tc.sev {
				t.Fatalf("got %s/%s, want %s/%s", v, sev, tc.verdict, tc.sev)
			}
		})
	}
}

// TestEarliestExpiryAcrossChain: an intermediate that expires before the leaf
// breaks the handshake just as thoroughly, and it is the one nobody watches.
func TestEarliestExpiryAcrossChain(t *testing.T) {
	soon := time.Now().Add(3 * 24 * time.Hour)
	late := time.Now().Add(300 * 24 * time.Hour)
	leaf := mintCert(t, certOpts{cn: "leaf", notAfter: late})
	inter := mintCert(t, certOpts{cn: "intermediate", notAfter: soon})

	chain, err := parseCertChain(append(leaf, inter...))
	if err != nil {
		t.Fatal(err)
	}
	if len(chain) != 2 {
		t.Fatalf("want both certs parsed, got %d", len(chain))
	}
	if got := earliestExpiry(chain); !got.Equal(chain[1].NotAfter) {
		t.Fatalf("want the intermediate's expiry, got the leaf's")
	}
}

func TestCertNames(t *testing.T) {
	withSANs := mintCert(t, certOpts{cn: "ignored", dns: []string{"b.example.com", "a.example.com"}, ips: []string{"10.0.0.1"}})
	chain, _ := parseCertChain(withSANs)
	got := certNames(chain[0])
	want := []string{"10.0.0.1", "a.example.com", "b.example.com"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("want sorted SANs and IPs %v, got %v", want, got)
	}

	// A certificate with no extensions at all - an ingress controller's default
	// certificate, typically - still has to identify itself.
	bare := mintCert(t, certOpts{cn: "haproxy-public.pl-core-haproxy-public"})
	chain, _ = parseCertChain(bare)
	if got := certNames(chain[0]); len(got) != 1 || got[0] != "haproxy-public.pl-core-haproxy-public" {
		t.Fatalf("want the common name as a fallback, got %v", got)
	}
}

func TestSummarizeNames(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   []string
		want string
	}{
		{"one short name is verbatim", []string{"api.eqtv.io"}, "api.eqtv.io"},
		{"a short pair is verbatim", []string{"a.eqtv.io", "b.eqtv.io"}, "a.eqtv.io,b.eqtv.io"},
		{
			// Two internal FQDNs are three names' worth of characters; counting
			// names rather than width is what rendered rows 288 columns wide.
			"a long pair groups",
			[]string{"castleblack.delivery-1.europe-west4.internal.eqtv.io", "prod-delivery-europe-west4-1.europe-west4-1.eqtv.io"},
			"eqtv.io (2)",
		},
		{
			"biggest domain leads",
			append(hosts("smartadserver.com", 8), hosts("eqtv.io", 2)...),
			"smartadserver.com (8), eqtv.io (2)",
		},
		{
			"in-cluster names group as one",
			[]string{"api.ns.svc", "api.ns.svc.cluster.local", "other.ns.svc.cluster.local", "more.ns.svc"},
			"cluster-internal (4)",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := summarizeNames(tc.in); got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

// TestSummarizeNamesCapsGroups: a certificate spanning many registrable domains
// must not widen the column without bound either.
func TestSummarizeNamesCapsGroups(t *testing.T) {
	var names []string
	for _, d := range []string{"a.com", "b.com", "c.com", "d.com"} {
		names = append(names, hosts(d, 3)...)
	}
	got := summarizeNames(names)
	if strings.Count(got, "(") != maxNameGroups || !strings.HasSuffix(got, "+2 more") {
		t.Fatalf("want %d groups then a remainder, got %q", maxNameGroups, got)
	}
}

func hosts(domain string, n int) []string {
	out := make([]string, 0, n)
	for i := range n {
		out = append(out, string(rune('a'+i))+"."+domain)
	}
	return out
}

// TestRegistrableDomain: the public suffix list, not the last two labels, which
// would file every British host under "co.uk".
func TestRegistrableDomain(t *testing.T) {
	for in, want := range map[string]string{
		"www.smartadserver.com":    "smartadserver.com",
		"shop.example.co.uk":       "example.co.uk",
		"*.eqtv.io":                "eqtv.io",
		"api.ns.svc":               "cluster-internal",
		"api.ns.svc.cluster.local": "cluster-internal",
	} {
		if got := registrableDomain(in); got != want {
			t.Errorf("%s: got %q, want %q", in, got, want)
		}
	}
}

// TestIssuerOf: Let's Encrypt signs with intermediates named YR1 and YR2, which
// say nothing in a table of expiries, while its organization says everything.
func TestIssuerOf(t *testing.T) {
	le := mintCert(t, certOpts{cn: "api.eqtv.io", issuerCN: "YR2", issuerO: "Let's Encrypt"})
	chain, _ := parseCertChain(le)
	if got := issuerOf(chain[0]); got != "Let's Encrypt" {
		t.Fatalf("want the organization, got %q", got)
	}

	// An internal CA often sets no organization at all.
	internal := mintCert(t, certOpts{cn: "svc", issuerCN: "kubernetes-ingress-ca"})
	chain, _ = parseCertChain(internal)
	if got := issuerOf(chain[0]); got != "kubernetes-ingress-ca" {
		t.Fatalf("want the common name as a fallback, got %q", got)
	}

	// Subject and issuer identical: nothing vouched for this but itself.
	selfSigned := mintCert(t, certOpts{cn: "webhook"})
	chain, _ = parseCertChain(selfSigned)
	if got := issuerOf(chain[0]); got != "self-signed" {
		t.Fatalf("want self-signed, got %q", got)
	}
}

func TestCerts(t *testing.T) {
	c := fake.NewClientset(
		tlsSecret("prod", "api-tls", mintCert(t, certOpts{
			cn: "api.eqtv.io", dns: []string{"api.eqtv.io"},
			notAfter: time.Now().Add(3 * 24 * time.Hour),
			issuerCN: "YR2", issuerO: "Let's Encrypt",
		})),
		tlsSecret("prod", "healthy-tls", mintCert(t, certOpts{
			cn: "ok.eqtv.io", dns: []string{"ok.eqtv.io"},
			notAfter: time.Now().Add(80 * 24 * time.Hour),
			issuerCN: "YR2", issuerO: "Let's Encrypt",
		})),
	)
	var buf bytes.Buffer
	if err := Certs(context.Background(), clients(c), kube.Flags{Namespace: "prod"}, nil, &buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{"api-tls", "EXPIRING", "healthy-tls", "OK", "Let's Encrypt", "api.eqtv.io"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q:\n%s", want, out)
		}
	}
	// Worst last, like every other verdict view.
	if strings.Index(out, "EXPIRING") < strings.Index(out, "healthy-tls") {
		t.Fatalf("want the expiring row below the healthy one:\n%s", out)
	}
}

// TestCertsPushesTypeSelectorDown: the narrowing is not only cheaper (43 of 386
// secrets on one production cluster) but safer - a command sweeping secrets
// cluster-wide must never pull the passwords it has no business reading.
func TestCertsPushesTypeSelectorDown(t *testing.T) {
	c := newClientsetWithFieldSelectors(tlsSecret("prod", "api-tls", mintCert(t, certOpts{cn: "api.eqtv.io"})))
	var buf bytes.Buffer
	if err := Certs(context.Background(), clients(c), kube.Flags{AllNamespaces: true}, nil, &buf); err != nil {
		t.Fatal(err)
	}
	assertFieldSelector(t, c, "secrets", "type=kubernetes.io/tls")
}

// TestCertsFlagsUnparseable: a secret typed as TLS whose tls.crt is missing or
// corrupt is itself the finding - something wrote it wrong.
func TestCertsFlagsUnparseable(t *testing.T) {
	broken := tlsSecret("prod", "broken-tls", []byte("not a certificate"))
	var buf bytes.Buffer
	if err := Certs(context.Background(), clients(fake.NewClientset(broken)), kube.Flags{Namespace: "prod"}, nil, &buf); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "INVALID") {
		t.Fatalf("want the broken secret flagged:\n%s", buf.String())
	}
}

// TestCertsNamedSecretPrintsEveryName: the sweep stays one line per object, so
// the detail lives behind a positional argument, like `node-ips <node>`.
func TestCertsNamedSecretPrintsEveryName(t *testing.T) {
	many := hosts("smartadserver.com", 8)
	c := fake.NewClientset(
		tlsSecret("prod", "big-tls", mintCert(t, certOpts{cn: "a.smartadserver.com", dns: many})),
		tlsSecret("prod", "other-tls", mintCert(t, certOpts{cn: "other.eqtv.io", dns: []string{"other.eqtv.io"}})),
	)
	var buf bytes.Buffer
	if err := Certs(context.Background(), clients(c), kube.Flags{Namespace: "prod"}, []string{"big-tls"}, &buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if strings.Contains(out, "other-tls") {
		t.Fatalf("naming a secret must narrow to it:\n%s", out)
	}
	for _, want := range many {
		if !strings.Contains(out, want) {
			t.Fatalf("want every name printed, missing %q:\n%s", want, out)
		}
	}
}

// TestCertsSortByDaysIsNumeric: "64d" does not parse as a number, so without an
// explicit rank the column sorts lexically and reads 1090d, 296d, 33d, 3438d.
func TestCertsSortByDaysIsNumeric(t *testing.T) {
	days := func(n int) *corev1.Secret {
		return tlsSecret("prod", "tls-"+strings.Repeat("x", n%7+1)+"-"+string(rune('a'+n%26)),
			mintCert(t, certOpts{cn: "a.eqtv.io", dns: []string{"a.eqtv.io"}, notAfter: time.Now().Add(time.Duration(n) * 24 * time.Hour)}))
	}
	c := fake.NewClientset(days(300), days(9), days(1090), days(45))

	var buf bytes.Buffer
	f := kube.Flags{Namespace: "prod", Sort: "in"}
	if err := Certs(context.Background(), clients(c), f, nil, &buf); err != nil {
		t.Fatal(err)
	}
	var got []int
	for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n")[1:] {
		fields := strings.Fields(line)
		got = append(got, daysRank(fields[len(fields)-2]))
	}
	if !slices.IsSorted(got) {
		t.Fatalf("want days ascending, got %v:\n%s", got, buf.String())
	}
}

func TestDaysRank(t *testing.T) {
	if daysRank("-6d") >= daysRank("2d") {
		t.Error("an expired certificate must sort before a live one")
	}
	if daysRank("33d") >= daysRank("1090d") {
		t.Error("days must order by value, not by spelling")
	}
	// An unparseable certificate has no day count; it is not urgent, it is
	// unreadable, and VERDICT already says so.
	if daysRank("-") <= daysRank("3438d") {
		t.Error("a cell with no number sorts as the far future")
	}
}
