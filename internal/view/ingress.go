package view

import (
	"cmp"
	"context"
	"io"
	"slices"
	"strconv"
	"strings"

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/PixiBixi/kubectl-klens/internal/kube"
)

// Ingress flattens ingress rules to one row per host+path and checks each one
// against the cluster: that the backend service exists and exposes the port the
// rule names, and that the host is covered by a TLS block whose secret is
// actually there. A rule pointing at a service that was renamed, or a TLS block
// naming a secret cert-manager never issued, returns 503s while `get ing` shows
// nothing wrong. Rows default to VERDICT (risk) order, riskiest at the bottom.
func Ingress(ctx context.Context, c kube.Clients, f kube.Flags, args []string, out io.Writer) error {
	var (
		ings    []networkingv1.Ingress
		svcs    []corev1.Service
		secrets []corev1.Secret
		// Reading secrets is a privilege many users are not granted, and the
		// backend checks are worth having without it, so a refusal downgrades
		// the TLS column instead of failing the command.
		secretsKnown = true
	)
	ns := f.NamespaceScope()
	err := allLists(
		func() (err error) {
			ings, err = kube.ListIngresses(ctx, c, ns, metav1.ListOptions{})
			return err
		},
		func() (err error) {
			svcs, err = kube.ListServices(ctx, c, ns, metav1.ListOptions{})
			return err
		},
		func() error {
			list, err := kube.ListSecrets(ctx, c, ns, metav1.ListOptions{})
			if apierrors.IsForbidden(err) {
				secretsKnown = false
				return nil
			}
			secrets = list
			return err
		},
	)
	if err != nil {
		return err
	}
	ports := servicePorts(svcs)
	haveSecret := make(map[string]bool, len(secrets))
	for i := range secrets {
		haveSecret[secrets[i].Namespace+"/"+secrets[i].Name] = true
	}
	paint := kube.NewPainter(f)

	type row struct {
		ns, name, class, host, path, backend string
		tls                                  string
		verdict, sev                         string
	}
	var rows []row
	for i := range ings {
		ing := &ings[i]
		for _, r := range ingressRules(ing) {
			secret, covered := tlsSecretFor(ing, r.host)
			v, sev := ingressVerdict(ing.Namespace, r.backend, ports, secret, covered, secretsKnown, haveSecret)
			rows = append(rows, row{
				ns: ing.Namespace, name: ing.Name, class: ingressClass(paint, ing),
				host: r.host, path: r.path, backend: backendCell(paint, r.backend),
				tls:     tlsCell(paint, secret, covered, secretsKnown),
				verdict: v, sev: sev,
			})
		}
	}
	// Deterministic tiebreak for rows sharing a verdict; the VERDICT sort applied
	// at Flush is stable, so this order survives within each verdict.
	slices.SortStableFunc(rows, func(a, b row) int {
		return cmp.Or(
			cmp.Compare(a.ns, b.ns),
			cmp.Compare(a.name, b.name),
			cmp.Compare(a.host, b.host),
			cmp.Compare(a.path, b.path),
		)
	})

	t := kube.NewTable(out, paint, "NS", "INGRESS", "CLASS", "HOST", "PATH", "BACKEND", "TLS", "VERDICT")
	for i := range rows {
		r := &rows[i]
		t.Row(r.ns, r.name, r.class, r.host, r.path, r.backend, r.tls, sevPaint(paint, r.sev)(r.verdict))
	}
	t.SortRank("VERDICT", verdictRank("NO-SERVICE", "NO-PORT", "NO-SECRET", "NO-TLS", "RESOURCE", "OK"))
	t.SortBy(orDefault(f.Sort, "verdict"))
	return t.Flush()
}

// ingressRule is one flattened routing rule: where traffic arrives and what it
// is sent to.
type ingressRule struct {
	host, path string
	backend    *networkingv1.IngressBackend
}

// ingressRules flattens an Ingress to its host/path rules, plus its
// defaultBackend as a catch-all row so a rule-less Ingress is not invisible.
func ingressRules(ing *networkingv1.Ingress) []ingressRule {
	var out []ingressRule
	for i := range ing.Spec.Rules {
		r := &ing.Spec.Rules[i]
		host := r.Host
		if host == "" {
			host = "*" // no host: the rule answers for any name reaching the controller
		}
		if r.HTTP == nil {
			out = append(out, ingressRule{host, "-", nil})
			continue
		}
		for j := range r.HTTP.Paths {
			p := &r.HTTP.Paths[j]
			out = append(out, ingressRule{host, pathOrRoot(p.Path), &p.Backend})
		}
	}
	if ing.Spec.DefaultBackend != nil {
		out = append(out, ingressRule{"*", "(default)", ing.Spec.DefaultBackend})
	}
	return out
}

func pathOrRoot(p string) string {
	if p == "" {
		return "/"
	}
	return p
}

// ingressVerdict checks one rule against the cluster. The first matching rule
// wins and the rules are total.
func ingressVerdict(ns string, b *networkingv1.IngressBackend, ports map[string]servicePortSet, secret string, covered, secretsKnown bool, haveSecret map[string]bool) (verdict, sev string) {
	switch {
	case b == nil || (b.Service == nil && b.Resource != nil):
		// A resource backend (a storage bucket, an APIs object) is not something
		// this can check, and a rule with no backend at all is the controller's
		// business, not a service wiring fault.
		return "RESOURCE", "muted"
	case b.Service == nil:
		return "NO-SERVICE", "bad"
	}
	set, ok := ports[ns+"/"+b.Service.Name]
	switch {
	case !ok:
		return "NO-SERVICE", "bad" // the service the rule routes to does not exist
	case !set.has(b.Service.Port):
		return "NO-PORT", "bad" // the service exists but does not expose that port
	case covered && secret != "" && secretsKnown && !haveSecret[ns+"/"+secret]:
		return "NO-SECRET", "bad" // TLS declared, certificate secret missing: the controller serves its default cert
	case !covered:
		return "NO-TLS", "warn" // reachable over plaintext only
	default:
		return "OK", "ok"
	}
}

// servicePortSet is the set of ways a service's ports can be named in a backend.
type servicePortSet struct {
	numbers map[int32]bool
	names   map[string]bool
}

func (s servicePortSet) has(p networkingv1.ServiceBackendPort) bool {
	if p.Name != "" {
		return s.names[p.Name]
	}
	return s.numbers[p.Number]
}

// servicePorts indexes every service's ports by number and by name, keyed
// "ns/name", so a backend reference is checked without a second lookup.
func servicePorts(svcs []corev1.Service) map[string]servicePortSet {
	out := make(map[string]servicePortSet, len(svcs))
	for i := range svcs {
		s := &svcs[i]
		set := servicePortSet{numbers: map[int32]bool{}, names: map[string]bool{}}
		for _, p := range s.Spec.Ports {
			set.numbers[p.Port] = true
			if p.Name != "" {
				set.names[p.Name] = true
			}
		}
		out[s.Namespace+"/"+s.Name] = set
	}
	return out
}

// tlsSecretFor finds the TLS block covering host and returns its secret name.
// An empty host list in a tls block is the controller's catch-all, and a
// wildcard entry covers one label, matching how a certificate does.
func tlsSecretFor(ing *networkingv1.Ingress, host string) (secret string, covered bool) {
	for _, t := range ing.Spec.TLS {
		if len(t.Hosts) == 0 {
			return t.SecretName, true
		}
		for _, h := range t.Hosts {
			if hostMatches(h, host) {
				return t.SecretName, true
			}
		}
	}
	return "", false
}

// hostMatches compares a certificate host to a rule host, honoring a single
// leading wildcard label the way TLS name matching does.
func hostMatches(pattern, host string) bool {
	if pattern == host || host == "*" {
		return true
	}
	suffix, ok := strings.CutPrefix(pattern, "*.")
	if !ok {
		return false
	}
	label, remainder, found := strings.Cut(host, ".")
	return found && label != "" && remainder == suffix
}

func ingressClass(paint kube.Painter, ing *networkingv1.Ingress) string {
	if ing.Spec.IngressClassName != nil && *ing.Spec.IngressClassName != "" {
		return *ing.Spec.IngressClassName
	}
	// The pre-1.18 annotation is still what several controllers read.
	if v := ing.Annotations["kubernetes.io/ingress.class"]; v != "" {
		return v
	}
	return paint.Muted("<default>")
}

// backendCell renders the routing target as service:port, or names the resource
// backend it cannot check.
func backendCell(paint kube.Painter, b *networkingv1.IngressBackend) string {
	switch {
	case b == nil:
		return paint.Muted("<none>")
	case b.Service != nil:
		port := b.Service.Port.Name
		if port == "" {
			port = strconv.Itoa(int(b.Service.Port.Number))
		}
		return b.Service.Name + ":" + port
	case b.Resource != nil:
		return paint.Muted(b.Resource.Kind + "/" + b.Resource.Name)
	}
	return paint.Muted("<none>")
}

// tlsCell names the certificate secret serving the host, muting the placeholder
// for plaintext and for the case where secrets could not be read.
func tlsCell(paint kube.Painter, secret string, covered, secretsKnown bool) string {
	switch {
	case !covered:
		return paint.Muted("none")
	case secret == "":
		return paint.Muted("<default-cert>") // a tls block with no secretName: the controller's own certificate
	case !secretsKnown:
		return paint.Muted(secret) // named, existence unverified
	}
	return secret
}
