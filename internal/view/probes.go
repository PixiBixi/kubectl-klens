package view

import (
	"cmp"
	"context"
	"io"
	"slices"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/PixiBixi/kubectl-klens/internal/kube"
)

// Probes lists each long-running container's readiness, liveness, and startup
// probe handler types with a reliability verdict, so a missing readiness probe
// (which silently serves 5xx during rollouts) is as visible as a missing
// liveness probe. Batch (Job/CronJob) pods are excluded since they aren't
// servers - under --by-owner that exclusion is automatic, because Jobs are not
// one of the four kinds podsForView reads. Rows default to VERDICT (risk)
// order, riskiest at the bottom.
func Probes(ctx context.Context, c kube.Clients, f kube.Flags, args []string, out io.Writer) error {
	pods, err := podsForView(ctx, c, f)
	if err != nil {
		return err
	}
	paint := kube.NewPainter(f)

	type entry struct {
		ns, pod, container           string
		replicas                     *corev1.Pod
		readiness, liveness, startup string
		verdict, sev                 string
	}
	var list []entry
	for i := range pods {
		p := &pods[i]
		if skipNamespace(f, p.Namespace) {
			continue
		}
		if ref := metav1.GetControllerOf(p); ref != nil && ref.Kind == "Job" {
			continue
		}
		// A workload whose source carries no probe definitions cannot be
		// reported on: an empty spec here would read as NO-PROBES for a
		// workload that has them.
		if !specKnows(p, "probes") {
			continue
		}
		for i := range p.Spec.Containers {
			ctr := &p.Spec.Containers[i]
			hasR := ctr.ReadinessProbe != nil
			hasL := ctr.LivenessProbe != nil
			v, sev := probesVerdict(hasR, hasL)
			list = append(list, entry{
				ns:        p.Namespace,
				pod:       p.Name,
				replicas:  p,
				container: ctr.Name,
				readiness: probeHandler(ctr.ReadinessProbe),
				liveness:  probeHandler(ctr.LivenessProbe),
				startup:   probeHandler(ctr.StartupProbe),
				verdict:   v,
				sev:       sev,
			})
		}
	}
	// Deterministic tiebreak for rows with equal sort keys; the VERDICT sort
	// applied at Flush is stable, so this order survives within each verdict.
	slices.SortStableFunc(list, func(a, b entry) int {
		return cmp.Or(
			cmp.Compare(a.ns, b.ns),
			cmp.Compare(a.pod, b.pod),
			cmp.Compare(a.container, b.container),
		)
	})

	t := kube.NewTable(out, paint, slices.Concat(
		[]string{"NS", podColumn(f, "POD")}, ownerHeaders(f),
		[]string{"CONTAINER", "READINESS", "LIVENESS", "STARTUP", "VERDICT"},
	)...)
	// Reused row buffer; see Reqlim for why this is not a slices.Concat.
	row := make([]string, 0, 8)
	for i := range list {
		e := &list[i]
		row = append(row[:0], e.ns, e.pod)
		row = appendOwnerCells(row, paint, f, e.replicas)
		row = append(row,
			e.container,
			probeCell(paint, e.readiness),
			probeCell(paint, e.liveness),
			probeCell(paint, e.startup),
			sevPaint(paint, e.sev)(e.verdict),
		)
		t.Row(row...)
	}
	t.SortRank("VERDICT", verdictRank("NO-PROBES", "NO-READINESS", "NO-LIVENESS", "OK"))
	t.SortBy(podSort(f, orDefault(f.Sort, "verdict"), "POD"))
	return t.Flush()
}

// probesVerdict classifies a container's reliability posture from whether its
// readiness and liveness probes are set. The first matching rule wins; the rules
// are total. sev is one of ok/warn/bad.
func probesVerdict(hasReadiness, hasLiveness bool) (verdict, sev string) {
	switch {
	case !hasReadiness && !hasLiveness:
		return "NO-PROBES", "bad" // no traffic gating and no self-healing
	case !hasReadiness:
		return "NO-READINESS", "bad" // traffic routed before the app is ready: invisible 5xx during rollouts
	case !hasLiveness:
		return "NO-LIVENESS", "warn" // a hung container won't be restarted automatically
	default:
		return "OK", "ok"
	}
}

// probeHandler reports a probe's handler type (http/grpc/tcp/exec), an empty
// string when the probe is unset, or "?" for an unrecognized handler.
func probeHandler(p *corev1.Probe) string {
	if p == nil {
		return ""
	}
	switch {
	case p.HTTPGet != nil:
		return "http"
	case p.GRPC != nil:
		return "grpc"
	case p.TCPSocket != nil:
		return "tcp"
	case p.Exec != nil:
		return "exec"
	default:
		return "?"
	}
}

// probeCell colors a probe handler cell: a present handler reads as healthy
// (green), an absent one as a muted placeholder.
func probeCell(paint kube.Painter, handler string) string {
	if handler == "" {
		return paint.Muted("-")
	}
	return paint.OK(handler)
}
