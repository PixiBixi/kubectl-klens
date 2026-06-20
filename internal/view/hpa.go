package view

import (
	"context"
	"io"
	"sort"
	"strconv"

	autoscalingv2 "k8s.io/api/autoscaling/v2"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/PixiBixi/kubectl-klens/internal/kube"
)

// Hpa lists HorizontalPodAutoscalers with a computed autoscaling verdict, so a
// maxed-out (no headroom) or metric-blind HPA is readable at a glance. Rows
// default to risk-descending order.
func Hpa(ctx context.Context, c kubernetes.Interface, f kube.Flags, args []string, out io.Writer) error {
	hpas, err := c.AutoscalingV2().HorizontalPodAutoscalers(f.NamespaceScope()).List(ctx, metav1.ListOptions{})
	if err != nil {
		return err
	}
	paint := kube.NewPainter(f)

	type entry struct {
		hpa          autoscalingv2.HorizontalPodAutoscaler
		verdict, sev string
	}
	list := make([]entry, 0, len(hpas.Items))
	for _, h := range hpas.Items {
		v, sev := hpaVerdict(h.Spec, h.Status)
		list = append(list, entry{h, v, sev})
	}
	sort.SliceStable(list, func(i, j int) bool {
		if ri, rj := sevRank(list[i].sev), sevRank(list[j].sev); ri != rj {
			return ri > rj
		}
		if list[i].hpa.Namespace != list[j].hpa.Namespace {
			return list[i].hpa.Namespace < list[j].hpa.Namespace
		}
		return list[i].hpa.Name < list[j].hpa.Name
	})

	t := kube.NewTable(out, paint, "NS", "NAME", "REF", "MIN", "MAX", "CURRENT", "DESIRED", "VERDICT")
	for _, e := range list {
		spec, st := e.hpa.Spec, e.hpa.Status
		cur := strconv.Itoa(int(st.CurrentReplicas))
		if st.CurrentReplicas >= spec.MaxReplicas {
			cur = paint.Bad(cur)
		}
		t.Row(
			e.hpa.Namespace,
			e.hpa.Name,
			spec.ScaleTargetRef.Kind+"/"+spec.ScaleTargetRef.Name,
			strconv.Itoa(int(hpaMinReplicas(spec))),
			strconv.Itoa(int(spec.MaxReplicas)),
			cur,
			strconv.Itoa(int(st.DesiredReplicas)),
			sevPaint(paint, e.sev)(e.verdict),
		)
	}
	t.SortBy(f.Sort)
	return t.Flush()
}

// hpaVerdict classifies an HPA's autoscaling state. The first matching rule
// wins; the rules are total, so a verdict is always returned. sev is one of
// ok/warn/bad/muted.
func hpaVerdict(spec autoscalingv2.HorizontalPodAutoscalerSpec, st autoscalingv2.HorizontalPodAutoscalerStatus) (verdict, sev string) {
	switch {
	case hpaConditionFalse(st.Conditions, autoscalingv2.ScalingActive):
		return "NO-METRICS", "bad" // can't read metrics: flying blind
	case st.CurrentReplicas >= spec.MaxReplicas:
		return "MAXED", "bad" // pinned at the ceiling: no headroom up
	case st.CurrentReplicas != st.DesiredReplicas:
		return "SCALING", "warn" // converging toward desired
	case st.CurrentReplicas <= hpaMinReplicas(spec):
		return "AT-MIN", "muted" // idle at the floor
	default:
		return "OK", "ok"
	}
}

// hpaMinReplicas returns the effective minimum, defaulting to 1 when unset.
func hpaMinReplicas(spec autoscalingv2.HorizontalPodAutoscalerSpec) int32 {
	if spec.MinReplicas != nil {
		return *spec.MinReplicas
	}
	return 1
}

// hpaConditionFalse reports whether the named condition is present with a False
// status.
func hpaConditionFalse(conds []autoscalingv2.HorizontalPodAutoscalerCondition, t autoscalingv2.HorizontalPodAutoscalerConditionType) bool {
	for _, c := range conds {
		if c.Type == t {
			return c.Status == corev1.ConditionFalse
		}
	}
	return false
}
