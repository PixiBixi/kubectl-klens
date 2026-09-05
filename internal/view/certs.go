package view

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"io"
	"math"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"golang.org/x/net/publicsuffix"

	"github.com/PixiBixi/kubectl-klens/internal/kube"
)

// tlsSecretSelector narrows the list to serving certificates server-side. It is
// not only cheaper (43 of 386 secrets on one production cluster) but safer: a
// command that sweeps secrets cluster-wide never pulls the passwords and tokens
// it has no business reading.
const tlsSecretSelector = "type=" + string(corev1.SecretTypeTLS)

// Certs reports when each TLS secret's certificate stops being valid, worst
// last. `kubectl get secret` shows that one exists; nothing shows that it
// expired last Tuesday, which is how a certificate takes a service down in
// silence.
//
// Naming one secret prints every name on its certificate instead of the
// summarized cell, the same way `node-ips <node>` narrows to one node: a
// sweep has to stay one line per object, and a certificate can carry 88 names.
func Certs(ctx context.Context, c kube.Clients, f kube.Flags, args []string, out io.Writer) error {
	secrets, err := kube.ListSecrets(ctx, c, f.Scope(), metav1.ListOptions{FieldSelector: tlsSecretSelector})
	if err != nil {
		return err
	}
	paint := kube.NewPainter(f)

	type entry struct {
		ns, name, names, issuer string
		expiry                  time.Time
		verdict, sev            string
	}
	list := make([]entry, 0, len(secrets))
	for i := range secrets {
		s := &secrets[i]
		if skipNamespace(f, s.Namespace) {
			continue
		}
		if len(args) > 0 && s.Name != args[0] {
			continue
		}
		chain, err := parseCertChain(s.Data[corev1.TLSCertKey])
		if err != nil || len(chain) == 0 {
			// A secret typed as TLS whose tls.crt is missing or unparseable is
			// itself worth seeing: something wrote it wrong.
			list = append(list, entry{
				ns: s.Namespace, name: s.Name,
				names:   paint.Muted("<unparseable>"),
				issuer:  paint.Muted("-"),
				verdict: "INVALID", sev: "bad",
			})
			continue
		}
		leaf := chain[0]
		expiry := earliestExpiry(chain)
		verdict, sev := certVerdict(expiry, time.Now())
		names := certNames(leaf)
		cell := summarizeNames(names)
		if len(args) > 0 {
			cell = strings.Join(names, ",")
		}
		if cell == "" {
			cell = paint.Muted("<none>")
		}
		list = append(list, entry{
			ns: s.Namespace, name: s.Name, names: cell,
			issuer: issuerOf(leaf), expiry: expiry,
			verdict: verdict, sev: sev,
		})
	}

	// Deterministic tiebreak for rows sharing a verdict; the VERDICT sort applied
	// at Flush is stable, so this order survives within each verdict.
	slices.SortStableFunc(list, func(a, b entry) int {
		if a.ns != b.ns {
			return strings.Compare(a.ns, b.ns)
		}
		return strings.Compare(a.name, b.name)
	})

	t := kube.NewTable(out, paint, "NS", "SECRET", "NAMES", "ISSUER", "NOT_AFTER", "IN", "VERDICT")
	for i := range list {
		e := &list[i]
		t.Row(
			e.ns, e.name, e.names, e.issuer,
			notAfterCell(paint, e.expiry),
			remainingCell(paint, e.expiry, e.sev),
			sevPaint(paint, e.sev)(e.verdict),
		)
	}
	t.SortRank("VERDICT", verdictRank("EXPIRED", "INVALID", "EXPIRING", "RENEW-DUE", "OK"))
	// "64d" does not parse as a number, so without this the column sorts
	// lexically and reads 1090d, 296d, 33d, 3438d. Plain ascending by days, so
	// --sort in puts the soonest first, as every non-verdict column does.
	t.SortRank("IN", daysRank)
	t.SortBy(orDefault(f.Sort, "verdict"))
	return t.Flush()
}

// Thresholds for the expiry verdict. cert-manager renews at a third of the
// lifetime by default, so a certificate inside RenewDue has already missed at
// least one renewal attempt and is worth looking at before it is urgent.
const (
	certExpiring = 7 * 24 * time.Hour
	certRenewDue = 14 * 24 * time.Hour
)

// certVerdict grades time left. The first matching rule wins and the rules are
// total.
func certVerdict(expiry, now time.Time) (verdict, sev string) {
	switch left := expiry.Sub(now); {
	case left <= 0:
		return "EXPIRED", "bad"
	case left < certExpiring:
		return "EXPIRING", "bad"
	case left < certRenewDue:
		return "RENEW-DUE", "warn"
	default:
		return "OK", "ok"
	}
}

// parseCertChain decodes every CERTIFICATE block in a PEM bundle. A secret
// normally holds the leaf followed by its intermediates.
func parseCertChain(raw []byte) ([]*x509.Certificate, error) {
	var chain []*x509.Certificate
	for rest := raw; len(rest) > 0; {
		var block *pem.Block
		block, rest = pem.Decode(rest)
		if block == nil {
			break
		}
		if block.Type != "CERTIFICATE" {
			continue
		}
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return nil, err
		}
		chain = append(chain, cert)
	}
	return chain, nil
}

// earliestExpiry returns the first date at which the bundle stops working. An
// intermediate that expires before the leaf breaks the handshake just as
// thoroughly, and it is the one nobody is watching.
func earliestExpiry(chain []*x509.Certificate) time.Time {
	earliest := chain[0].NotAfter
	for _, c := range chain[1:] {
		if c.NotAfter.Before(earliest) {
			earliest = c.NotAfter
		}
	}
	return earliest
}

// certNames returns what the certificate is valid for: its subject alternative
// names, falling back to the common name for a certificate old or crude enough
// to carry no extensions at all (an ingress controller's default certificate,
// typically).
func certNames(leaf *x509.Certificate) []string {
	names := make([]string, 0, len(leaf.DNSNames)+len(leaf.IPAddresses))
	names = append(names, leaf.DNSNames...)
	for _, ip := range leaf.IPAddresses {
		names = append(names, ip.String())
	}
	if len(names) == 0 && leaf.Subject.CommonName != "" {
		names = append(names, leaf.Subject.CommonName)
	}
	sort.Strings(names)
	return names
}

// maxNamesInline bounds the verbatim form by *width*, not by count. Counting
// was the first attempt and it broke the table: two internal FQDNs are three
// names' worth of characters on their own, and a real cluster rendered rows 288
// columns wide. A certificate's names are context here - the row is already
// identified by its namespace and secret - so past this budget the cell says
// which domains are covered and `certs <secret>` prints the rest.
const maxNamesInline = 44

// maxNameGroups caps the summarized form, so a certificate spanning many
// registrable domains cannot widen the column without bound either.
const maxNameGroups = 2

// summarizeNames keeps a short list readable and turns a long one into the
// thing that actually identifies it: which domains it covers, and how many
// names in each. A delivery certificate with 88 hosts under one domain says far
// more as "smartadserver.com (88)" than as its alphabetically first host.
func summarizeNames(names []string) string {
	if inline := strings.Join(names, ","); len(inline) <= maxNamesInline {
		return inline
	}
	counts := map[string]int{}
	order := make([]string, 0, 4)
	for _, n := range names {
		d := registrableDomain(n)
		if _, seen := counts[d]; !seen {
			order = append(order, d)
		}
		counts[d]++
	}
	// Biggest group first: it is the one that identifies the certificate.
	sort.SliceStable(order, func(i, j int) bool { return counts[order[i]] > counts[order[j]] })

	parts := make([]string, 0, maxNameGroups+1)
	for _, d := range order[:min(len(order), maxNameGroups)] {
		parts = append(parts, fmt.Sprintf("%s (%d)", d, counts[d]))
	}
	if rest := len(order) - maxNameGroups; rest > 0 {
		parts = append(parts, fmt.Sprintf("+%d more", rest))
	}
	return strings.Join(parts, ", ")
}

// registrableDomain reduces a name to the domain someone registered, so hosts
// group the way a human would group them. It goes through the public suffix
// list rather than taking the last two labels, which would file every
// co.uk host under "co.uk".
func registrableDomain(name string) string {
	name = strings.TrimPrefix(name, "*.")
	// In-cluster service names are not public domains, and the suffix list
	// splits them into noise: api.ns.svc becomes "ns.svc" and its
	// cluster.local twin becomes "cluster.local", so one service lands in two
	// groups that both mean the same thing.
	if isClusterInternal(name) {
		return "cluster-internal"
	}
	d, err := publicsuffix.EffectiveTLDPlusOne(name)
	if err != nil {
		return name // an IP, a bare hostname, or something not a domain at all
	}
	return d
}

func isClusterInternal(name string) bool {
	return strings.HasSuffix(name, ".svc") ||
		strings.Contains(name, ".svc.") ||
		strings.HasSuffix(name, ".cluster.local")
}

// issuerOf names who signed the certificate, which is what says whether an
// expiry is urgent: a public certificate going down takes traffic with it,
// while an ingress controller's self-signed default is only ever served to a
// client that matched no other name.
func issuerOf(leaf *x509.Certificate) string {
	if leaf.Issuer.String() == leaf.Subject.String() {
		return "self-signed"
	}
	// Organization before common name: a public CA puts something readable
	// there and something cryptic in the CN. Let's Encrypt signs with
	// intermediates called YR1 and YR2, which say nothing to the person reading
	// a table of expiries, while its organization says everything.
	if len(leaf.Issuer.Organization) > 0 {
		return leaf.Issuer.Organization[0]
	}
	if cn := leaf.Issuer.CommonName; cn != "" {
		return cn
	}
	return "<unknown>"
}

// daysRank reads back the integer remainingCell wrote, so the column orders by
// time left rather than by the spelling of it. A cell with no number (an
// unparseable certificate has none) sorts as the far future: it is not urgent,
// it is unreadable, and VERDICT already says so.
func daysRank(cell string) int {
	n, err := strconv.Atoi(strings.TrimSuffix(cell, "d"))
	if err != nil {
		return math.MaxInt32
	}
	return n
}

func notAfterCell(paint kube.Painter, t time.Time) string {
	if t.IsZero() {
		return paint.Muted("-")
	}
	return t.Local().Format("2006-01-02 15:04")
}

// remainingCell prints whole days left, negative once past. Days rather than a
// duration because that is the unit a renewal window is discussed in.
func remainingCell(paint kube.Painter, t time.Time, sev string) string {
	if t.IsZero() {
		return paint.Muted("-")
	}
	days := int(time.Until(t).Hours() / 24)
	return sevPaint(paint, sev)(fmt.Sprintf("%dd", days))
}
