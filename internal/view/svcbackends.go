package view

import (
	"cmp"
	"context"
	"io"
	"slices"
	"strconv"

	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"

	"github.com/PixiBixi/kubectl-klens/internal/kube"
)

// SvcBackends lists services with the pods actually behind them, so a service
// pointing at nothing - the silent outage of a mistyped selector or a workload
// scaled to zero - is visible without cross-reading `get svc` and `get
// endpointslices`. Rows default to VERDICT (risk) order, riskiest at the bottom.
func SvcBackends(ctx context.Context, c kube.Clients, f kube.Flags, args []string, out io.Writer) error {
	svcs, epSlices, err := bothLists(
		func() ([]corev1.Service, error) {
			return kube.ListServices(ctx, c, f.Scope(), metav1.ListOptions{})
		},
		func() ([]discoveryv1.EndpointSlice, error) {
			return kube.ListEndpointSlices(ctx, c, f.Scope(), metav1.ListOptions{})
		},
	)
	if err != nil {
		return err
	}
	counts := countEndpoints(epSlices)
	paint := kube.NewPainter(f)

	type entry struct {
		svc          *corev1.Service
		eps          endpointCount
		verdict, sev string
	}
	list := make([]entry, 0, len(svcs))
	for i := range svcs {
		s := &svcs[i]
		eps := counts[s.Namespace+"/"+s.Name]
		v, sev := svcVerdict(s, eps)
		list = append(list, entry{s, eps, v, sev})
	}
	// Deterministic tiebreak for rows sharing a verdict; the VERDICT sort applied
	// at Flush is stable, so this order survives within each verdict.
	slices.SortStableFunc(list, func(a, b entry) int {
		return cmp.Or(
			cmp.Compare(a.svc.Namespace, b.svc.Namespace),
			cmp.Compare(a.svc.Name, b.svc.Name),
		)
	})

	t := kube.NewTable(out, paint, "NS", "SERVICE", "TYPE", "SELECTOR", "READY", "NOTREADY", "VERDICT")
	for i := range list {
		e := &list[i]
		t.Row(
			e.svc.Namespace,
			e.svc.Name,
			string(e.svc.Spec.Type),
			selectorCell(paint, e.svc.Spec.Selector, e.verdict == "NO-PODS"),
			readyCell(paint, e.eps.ready),
			notReadyCell(paint, e.eps.notReady),
			sevPaint(paint, e.sev)(e.verdict),
		)
	}
	t.SortRank("VERDICT", verdictRank("UNWIRED", "NO-PODS", "NO-READY", "DEGRADED", "MANUAL", "EXTERNAL", "OK"))
	t.SortBy(orDefault(f.Sort, "verdict"))
	return t.Flush()
}

// endpointCount is a service's backing endpoints split by readiness.
type endpointCount struct{ ready, notReady int }

// svcVerdict classifies a service by whether it has backends and whether they
// can serve. The first matching rule wins and the rules are total.
func svcVerdict(s *corev1.Service, eps endpointCount) (verdict, sev string) {
	total := eps.ready + eps.notReady
	switch {
	// ExternalName is a DNS alias: it has no selector and no endpoints by
	// design, so an empty backend list is not a fault.
	case s.Spec.Type == corev1.ServiceTypeExternalName:
		return "EXTERNAL", "muted"
	case len(s.Spec.Selector) == 0 && total > 0:
		return "MANUAL", "muted" // endpoints managed by hand or by another controller
	case len(s.Spec.Selector) == 0:
		return "UNWIRED", "bad" // no selector and nobody filled the endpoints in: nothing can ever answer
	case total == 0:
		return "NO-PODS", "bad" // selector matches no pod: stale label or a workload at zero
	case eps.ready == 0:
		return "NO-READY", "bad" // pods exist but none passes its readiness probe
	case eps.notReady > 0:
		return "DEGRADED", "warn" // serving on fewer pods than it has; normal mid-rollout
	default:
		return "OK", "ok"
	}
}

// countEndpoints tallies ready and not-ready endpoints per service, keyed
// "ns/name" from the EndpointSlice's owning-service label.
//
// Endpoints are deduplicated by pod, not counted per slice entry: a dual-stack
// service gets one slice per address family, so the same pod appears twice and a
// naive count would report double the backends.
func countEndpoints(epSlices []discoveryv1.EndpointSlice) map[string]endpointCount {
	counts := map[string]endpointCount{}
	seen := map[string]bool{}
	for i := range epSlices {
		es := &epSlices[i]
		svc := es.Labels[discoveryv1.LabelServiceName]
		if svc == "" {
			continue // an orphan slice belongs to no service
		}
		key := es.Namespace + "/" + svc
		for j := range es.Endpoints {
			ep := &es.Endpoints[j]
			id := key + "|" + endpointID(ep)
			if seen[id] {
				continue
			}
			seen[id] = true
			c := counts[key]
			if endpointReady(ep) {
				c.ready++
			} else {
				c.notReady++
			}
			counts[key] = c
		}
	}
	return counts
}

// endpointID identifies the pod behind an endpoint: its targetRef when set (the
// case for every selector-backed service), else its first address for manually
// managed endpoints.
func endpointID(ep *discoveryv1.Endpoint) string {
	if ep.TargetRef != nil {
		return string(ep.TargetRef.UID) + ep.TargetRef.Namespace + "/" + ep.TargetRef.Name
	}
	if len(ep.Addresses) > 0 {
		return ep.Addresses[0]
	}
	return ""
}

// endpointReady reads the ready condition, whose nil means ready by API
// convention (a slice written without conditions is assumed serving).
func endpointReady(ep *discoveryv1.Endpoint) bool {
	return ep.Conditions.Ready == nil || *ep.Conditions.Ready
}

// selectorCell prints the full selector only where it is the thing to read: a
// NO-PODS row, where the answer is the typo in it. Everywhere else it is a
// label count, because the Helm convention (three app.kubernetes.io keys
// repeating the same value) spends 110 columns saying nothing and wraps the row,
// taking READY, NOTREADY and VERDICT down with it.
func selectorCell(paint kube.Painter, sel map[string]string, full bool) string {
	if len(sel) == 0 {
		return paint.Muted("<none>")
	}
	if full {
		return labels.Set(sel).String()
	}
	if len(sel) == 1 {
		return paint.Muted("1 label")
	}
	return paint.Muted(strconv.Itoa(len(sel)) + " labels")
}

// readyCell colors the serving count: none is bad, anything else is healthy.
func readyCell(paint kube.Painter, n int) string {
	s := strconv.Itoa(n)
	if n == 0 {
		return paint.Bad(s)
	}
	return paint.OK(s)
}

// notReadyCell colors leftover unready endpoints: zero is the wanted state, any
// other count is worth a look but routine during a rollout.
func notReadyCell(paint kube.Painter, n int) string {
	s := strconv.Itoa(n)
	if n == 0 {
		return paint.OK(s)
	}
	return paint.Warn(s)
}
