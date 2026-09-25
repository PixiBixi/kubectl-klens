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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"

	"github.com/PixiBixi/kubectl-klens/internal/kube"
)

// Coverage verdicts. Pod-level ones classify one pod in one direction; the
// namespace rollup adds DEFAULT-DENY, PARTIAL and NO-PODS on top.
const (
	npOpen        = "OPEN"         // no policy selects the pod for this direction
	npAllowAll    = "ALLOW-ALL"    // selected, but a rule lets every peer through
	npRestricted  = "RESTRICTED"   // selected, with rules naming peers or ports
	npDeny        = "DENY"         // selected only by policies with no rules
	npDefaultDeny = "DEFAULT-DENY" // every pod in the namespace is DENY
	npPartial     = "PARTIAL"      // some pods are OPEN, the others covered
	npNoPods      = "NO-PODS"      // policies but no pod they could apply to
	npHostNetwork = "HOST-NETWORK" // NetworkPolicy does not apply to hostNetwork pods
)

// netpolRank orders both direction columns worst-first. PARTIAL cells carry an
// "N/M" suffix, so the rank keys on the first word.
var netpolRank = func() func(string) int {
	rank := verdictRank(npOpen, npAllowAll, npPartial, npRestricted, npDeny, npDefaultDeny, npHostNetwork, npNoPods)
	return func(cell string) int {
		word, _, _ := strings.Cut(cell, " ")
		return rank(word)
	}
}()

// Pod list pushdowns. The namespace rollup never counts hostNetwork pods, and
// on node-agent-heavy clusters they are most of the payload (-68% measured on a
// 553-pod AKS cluster). The per-pod view still shows them as HOST-NETWORK rows.
const (
	npLiveSelector     = "status.phase!=Failed,status.phase!=Succeeded"
	npEligibleSelector = "spec.hostNetwork=false," + npLiveSelector
)

// Netpol shows how NetworkPolicies actually cover pods, per direction, instead
// of the raw policy list where an empty podSelector reads as "<none>" while it
// selects every pod. Across namespaces it prints one row per namespace; scoped
// to a single namespace it prints one row per pod with the policies selecting it.
func Netpol(ctx context.Context, c kube.Clients, f kube.Flags, _ []string, out io.Writer) error {
	_, perPod := f.Scope().One()
	sel := npLiveSelector
	if !perPod {
		sel = npEligibleSelector
	}
	pods, pols, err := bothLists(
		func() ([]corev1.Pod, error) {
			return kube.ListPods(ctx, c, f.Scope(), metav1.ListOptions{FieldSelector: sel})
		},
		func() ([]networkingv1.NetworkPolicy, error) {
			return kube.ListNetworkPolicies(ctx, c, f.Scope(), metav1.ListOptions{})
		},
	)
	if err != nil {
		return err
	}
	byNs := map[string][]npPolicy{}
	for i := range pols {
		p, err := compilePolicy(&pols[i])
		if err != nil {
			return err
		}
		byNs[p.ns] = append(byNs[p.ns], p)
	}
	paint := kube.NewPainter(f)
	if perPod {
		return netpolPods(pods, byNs, paint, f.Sort, out)
	}
	return netpolNamespaces(pods, byNs, paint, f.Sort, out)
}

// npPolicy is a NetworkPolicy reduced to what coverage needs: its selector and,
// per direction, whether it applies at all and how open its rules are.
type npPolicy struct {
	ns, name        string
	sel             labels.Selector
	ingress, egress *npRules // nil when the policy does not govern that direction
}

type npRules struct {
	deny     bool // governs the direction with zero rules: blocks everything
	allowAll bool // at least one rule admits any peer on any port
}

func compilePolicy(p *networkingv1.NetworkPolicy) (npPolicy, error) {
	sel, err := metav1.LabelSelectorAsSelector(&p.Spec.PodSelector)
	if err != nil {
		return npPolicy{}, err
	}
	out := npPolicy{ns: p.Namespace, name: p.Name, sel: sel}
	ingress, egress := policyTypes(p.Spec)
	if ingress {
		out.ingress = &npRules{deny: len(p.Spec.Ingress) == 0}
		for _, r := range p.Spec.Ingress {
			out.ingress.allowAll = out.ingress.allowAll || allowsAnyPeer(r.From, len(r.Ports))
		}
	}
	if egress {
		out.egress = &npRules{deny: len(p.Spec.Egress) == 0}
		for _, r := range p.Spec.Egress {
			out.egress.allowAll = out.egress.allowAll || allowsAnyPeer(r.To, len(r.Ports))
		}
	}
	return out, nil
}

// policyTypes applies the apiserver's defaulting: without an explicit
// policyTypes, a policy always governs Ingress and governs Egress only if it
// carries egress rules.
func policyTypes(s networkingv1.NetworkPolicySpec) (ingress, egress bool) {
	if len(s.PolicyTypes) == 0 {
		return true, len(s.Egress) > 0
	}
	for _, t := range s.PolicyTypes {
		switch t {
		case networkingv1.PolicyTypeIngress:
			ingress = true
		case networkingv1.PolicyTypeEgress:
			egress = true
		}
	}
	return ingress, egress
}

// allowsAnyPeer reports whether a rule admits every peer on every port: an
// empty rule (`- {}`), or one whose peers include an unrestricted 0.0.0.0/0 or
// ::/0 block. Rules are OR-ed, so one such rule opens the whole direction.
func allowsAnyPeer(peers []networkingv1.NetworkPolicyPeer, ports int) bool {
	if ports > 0 {
		return false
	}
	if len(peers) == 0 {
		return true
	}
	for _, p := range peers {
		if b := p.IPBlock; b != nil && len(b.Except) == 0 && (b.CIDR == "0.0.0.0/0" || b.CIDR == "::/0") {
			return true
		}
	}
	return false
}

// podCoverage classifies one pod in one direction and names the policies
// selecting it for that direction.
func podCoverage(pols []npPolicy, podLabels labels.Set, rules func(*npPolicy) *npRules) (verdict string, names []string) {
	allDeny, allowAll := true, false
	for i := range pols {
		p := &pols[i]
		r := rules(p)
		if r == nil || !p.sel.Matches(podLabels) {
			continue
		}
		names = append(names, p.name)
		allDeny = allDeny && r.deny
		allowAll = allowAll || r.allowAll
	}
	switch {
	case len(names) == 0:
		return npOpen, nil
	case allowAll:
		return npAllowAll, names
	case allDeny:
		return npDeny, names
	default:
		return npRestricted, names
	}
}

func npIngress(p *npPolicy) *npRules { return p.ingress }
func npEgress(p *npPolicy) *npRules  { return p.egress }

// npEligible reports whether NetworkPolicy can apply to the pod: finished pods
// hold no traffic and hostNetwork pods share the node's network namespace.
func npEligible(p *corev1.Pod) bool {
	return !p.Spec.HostNetwork && p.Status.Phase != corev1.PodSucceeded && p.Status.Phase != corev1.PodFailed
}

// npSystemNamespace flags platform namespaces whose rows are muted rather than
// hidden: they are almost always OPEN and would otherwise bury workload rows.
func npSystemNamespace(ns string) bool {
	return strings.HasPrefix(ns, "kube-") || strings.HasPrefix(ns, "gke-") || strings.HasPrefix(ns, "gmp-")
}

// npCell colors a verdict the same way in both direction columns: one word, one
// color, or the table reads as two different verdicts. ALLOW-ALL is a warning:
// an internet-facing pod behind a client-IP-preserving LB can only allow 0.0.0.0/0.
func npCell(paint kube.Painter, verdict string, system bool) string {
	if system {
		return paint.Muted(verdict)
	}
	word, _, _ := strings.Cut(verdict, " ")
	switch word {
	case npOpen:
		return paint.Bad(verdict)
	case npAllowAll, npPartial:
		return paint.Warn(verdict)
	case npNoPods, npHostNetwork:
		return paint.Muted(verdict)
	default:
		return paint.OK(verdict)
	}
}

type npTally struct{ pods, open, allowAll, deny int }

func (t *npTally) add(v string) {
	t.pods++
	switch v {
	case npOpen:
		t.open++
	case npAllowAll:
		t.allowAll++
	case npDeny:
		t.deny++
	}
}

// verdict rolls a namespace up. ALLOW-ALL outranks PARTIAL: a policy that
// selects pods but admits everything is a false sense of safety.
func (t npTally) verdict() string {
	switch {
	case t.pods == 0:
		return npNoPods
	case t.open == t.pods:
		return npOpen
	case t.allowAll > 0:
		return npAllowAll
	case t.open > 0:
		return npPartial + " " + strconv.Itoa(t.pods-t.open) + "/" + strconv.Itoa(t.pods)
	case t.deny == t.pods:
		return npDefaultDeny
	default:
		return npRestricted
	}
}

func netpolNamespaces(pods []corev1.Pod, byNs map[string][]npPolicy, paint kube.Painter, sort string, out io.Writer) error {
	type row struct{ in, eg npTally }
	rows := map[string]*row{}
	for ns := range byNs {
		rows[ns] = &row{}
	}
	for i := range pods {
		p := &pods[i]
		r := rows[p.Namespace]
		if r == nil {
			r = &row{}
			rows[p.Namespace] = r
		}
		if !npEligible(p) {
			continue
		}
		set := labels.Set(p.Labels)
		in, _ := podCoverage(byNs[p.Namespace], set, npIngress)
		eg, _ := podCoverage(byNs[p.Namespace], set, npEgress)
		r.in.add(in)
		r.eg.add(eg)
	}
	// System namespaces first: the verdict sort is stable, so within a verdict
	// they stay above workload rows, which land nearest the prompt.
	names := make([]string, 0, len(rows))
	for ns := range rows {
		names = append(names, ns)
	}
	slices.SortFunc(names, func(a, b string) int {
		return cmp.Or(-cmp.Compare(boolInt(npSystemNamespace(a)), boolInt(npSystemNamespace(b))), cmp.Compare(a, b))
	})

	t := kube.NewTable(out, paint, "NS", "POLICIES", "PODS", "INGRESS", "EGRESS")
	for _, ns := range names {
		r, sys := rows[ns], npSystemNamespace(ns)
		t.Row(ns, strconv.Itoa(len(byNs[ns])), strconv.Itoa(r.in.pods),
			npCell(paint, r.in.verdict(), sys), npCell(paint, r.eg.verdict(), sys))
	}
	return flushNetpol(t, sort)
}

func netpolPods(pods []corev1.Pod, byNs map[string][]npPolicy, paint kube.Painter, sort string, out io.Writer) error {
	slices.SortFunc(pods, func(a, b corev1.Pod) int { return cmp.Compare(a.Name, b.Name) })
	t := kube.NewTable(out, paint, "NS", "POD", "INGRESS", "EGRESS", "POLICIES")
	for i := range pods {
		p := &pods[i]
		if p.Status.Phase == corev1.PodSucceeded || p.Status.Phase == corev1.PodFailed {
			continue
		}
		sys := npSystemNamespace(p.Namespace)
		if p.Spec.HostNetwork {
			t.Row(p.Namespace, p.Name, paint.Muted(npHostNetwork), paint.Muted(npHostNetwork), paint.Muted("-"))
			continue
		}
		set := labels.Set(p.Labels)
		in, inNames := podCoverage(byNs[p.Namespace], set, npIngress)
		eg, egNames := podCoverage(byNs[p.Namespace], set, npEgress)
		t.Row(p.Namespace, p.Name, npCell(paint, in, sys), npCell(paint, eg, sys),
			orMutedDash(paint, strings.Join(mergeNames(inNames, egNames), ",")))
	}
	return flushNetpol(t, sort)
}

func flushNetpol(t *kube.Table, sort string) error {
	t.SortRank("INGRESS", netpolRank)
	t.SortRank("EGRESS", netpolRank)
	t.SortBy(orDefault(sort, "ingress"))
	return t.Flush()
}

// mergeNames returns the sorted union of two policy name lists.
func mergeNames(a, b []string) []string {
	out := slices.Concat(a, b)
	slices.Sort(out)
	return slices.Compact(out)
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
