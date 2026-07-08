package view

import (
	"cmp"
	"context"
	"io"
	"slices"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/PixiBixi/kubectl-klens/internal/kube"
)

// Probes lists each long-running container's readiness, liveness, and startup
// probe handler types with a reliability verdict, so a missing readiness probe
// (which silently serves 5xx during rollouts) is as visible as a missing
// liveness probe. Batch (Job/CronJob) pods are excluded since they aren't
// servers. Rows default to VERDICT (risk) order, riskiest at the bottom.
func Probes(ctx context.Context, c kubernetes.Interface, f kube.Flags, args []string, out io.Writer) error {
	pods, err := c.CoreV1().Pods(f.NamespaceScope()).List(ctx, metav1.ListOptions{})
	if err != nil {
		return err
	}
	paint := kube.NewPainter(f)

	type entry struct {
		ns, pod, container           string
		readiness, liveness, startup string
		verdict, sev                 string
	}
	var list []entry
	for _, p := range pods.Items {
		if p.Namespace == "kube-system" {
			continue
		}
		if ref := metav1.GetControllerOf(&p); ref != nil && ref.Kind == "Job" {
			continue
		}
		for _, ctr := range p.Spec.Containers {
			hasR := ctr.ReadinessProbe != nil
			hasL := ctr.LivenessProbe != nil
			v, sev := probesVerdict(hasR, hasL)
			list = append(list, entry{
				ns:        p.Namespace,
				pod:       p.Name,
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

	t := kube.NewTable(out, paint, "NS", "POD", "CONTAINER", "READINESS", "LIVENESS", "STARTUP", "VERDICT")
	for _, e := range list {
		t.Row(
			e.ns, e.pod, e.container,
			probeCell(paint, e.readiness),
			probeCell(paint, e.liveness),
			probeCell(paint, e.startup),
			sevPaint(paint, e.sev)(e.verdict),
		)
	}
	t.SortRank("VERDICT", verdictRank("NO-PROBES", "NO-READINESS", "NO-LIVENESS", "OK"))
	t.SortBy(orDefault(f.Sort, "verdict"))
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
