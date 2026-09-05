package view

import (
	"cmp"
	"context"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"

	autoscalingv2 "k8s.io/api/autoscaling/v2"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/PixiBixi/kubectl-klens/internal/kube"
)

// Hpa lists HorizontalPodAutoscalers with a computed autoscaling verdict, so a
// maxed-out (no headroom) or metric-blind HPA is readable at a glance. Rows
// default to VERDICT (risk) order, riskiest at the bottom.
func Hpa(ctx context.Context, c kube.Clients, f kube.Flags, args []string, out io.Writer) error {
	hpas, err := kube.ListHorizontalPodAutoscalers(ctx, c, f.Scope(), metav1.ListOptions{})
	if err != nil {
		return err
	}
	paint := kube.NewPainter(f)

	type entry struct {
		hpa          *autoscalingv2.HorizontalPodAutoscaler
		verdict, sev string
	}
	list := make([]entry, 0, len(hpas))
	for i := range hpas {
		h := &hpas[i]
		v, sev := hpaVerdict(h.Spec, h.Status)
		list = append(list, entry{h, v, sev})
	}
	// Deterministic tiebreak for rows with equal sort keys; the VERDICT sort
	// applied at Flush is stable, so this order survives within each verdict.
	slices.SortStableFunc(list, func(a, b entry) int {
		return cmp.Or(
			cmp.Compare(a.hpa.Namespace, b.hpa.Namespace),
			cmp.Compare(a.hpa.Name, b.hpa.Name),
		)
	})

	t := kube.NewTable(out, paint, "NS", "NAME", "REF", "TARGETS", "MIN", "MAX", "CURRENT", "DESIRED", "VERDICT")
	for i := range list {
		e := &list[i]
		spec, st := e.hpa.Spec, e.hpa.Status
		cur := strconv.Itoa(int(st.CurrentReplicas))
		if st.CurrentReplicas >= spec.MaxReplicas {
			cur = paint.Bad(cur)
		}
		t.Row(
			e.hpa.Namespace,
			e.hpa.Name,
			spec.ScaleTargetRef.Kind+"/"+spec.ScaleTargetRef.Name,
			hpaTargets(paint, spec.Metrics, st.CurrentMetrics),
			strconv.Itoa(int(hpaMinReplicas(spec))),
			strconv.Itoa(int(spec.MaxReplicas)),
			cur,
			strconv.Itoa(int(st.DesiredReplicas)),
			sevPaint(paint, e.sev)(e.verdict),
		)
	}
	t.SortRank("VERDICT", verdictRank("NO-METRICS", "MAXED", "SCALING", "AT-MIN", "OK"))
	t.SortBy(orDefault(f.Sort, "verdict"))
	return t.Flush()
}

// hpaTargets renders the current/target value of every metric the HPA drives
// on, in `kubectl get hpa`'s TARGETS shape (`cpu: 67%/70%`), so the number the
// autoscaler actually reacts to is visible next to the verdict. A metric whose
// current value is missing reads `<unknown>` and is painted bad: that is the
// NO-METRICS case seen per metric.
func hpaTargets(paint kube.Painter, specs []autoscalingv2.MetricSpec, statuses []autoscalingv2.MetricStatus) string {
	if len(specs) == 0 {
		return paint.Muted("<none>")
	}
	cells := make([]string, 0, len(specs))
	for i, spec := range specs {
		var st *autoscalingv2.MetricStatus
		if i < len(statuses) {
			st = &statuses[i]
		}
		name, cur, target := hpaMetricTarget(spec, st)
		val := cur + "/" + target
		if cur == unknownMetric {
			val = paint.Bad(val)
		} else {
			val = paint.OK(val)
		}
		if name != "" {
			val = name + ": " + val
		}
		cells = append(cells, val)
	}
	return strings.Join(cells, ", ")
}

// unknownMetric is what a metric with no reported current value prints as, the
// same placeholder kubectl uses.
const unknownMetric = "<unknown>"

// hpaMetricTarget destructures one metric spec (plus its matching status entry,
// which may be nil) into the metric's name, its current value and its target.
// Utilization targets print as a percentage, value targets as a quantity.
func hpaMetricTarget(spec autoscalingv2.MetricSpec, st *autoscalingv2.MetricStatus) (name, cur, target string) {
	switch spec.Type {
	case autoscalingv2.ResourceMetricSourceType:
		if spec.Resource == nil {
			break
		}
		name = string(spec.Resource.Name)
		var current *autoscalingv2.MetricValueStatus
		if st != nil && st.Resource != nil {
			current = &st.Resource.Current
		}
		cur, target = hpaMetricValues(current, spec.Resource.Target)
	case autoscalingv2.ContainerResourceMetricSourceType:
		if spec.ContainerResource == nil {
			break
		}
		name = string(spec.ContainerResource.Name) + "(" + spec.ContainerResource.Container + ")"
		var current *autoscalingv2.MetricValueStatus
		if st != nil && st.ContainerResource != nil {
			current = &st.ContainerResource.Current
		}
		cur, target = hpaMetricValues(current, spec.ContainerResource.Target)
	case autoscalingv2.PodsMetricSourceType:
		if spec.Pods == nil {
			break
		}
		name = spec.Pods.Metric.Name
		var current *autoscalingv2.MetricValueStatus
		if st != nil && st.Pods != nil {
			current = &st.Pods.Current
		}
		cur, target = hpaMetricValues(current, spec.Pods.Target)
	case autoscalingv2.ObjectMetricSourceType:
		if spec.Object == nil {
			break
		}
		name = spec.Object.Metric.Name
		var current *autoscalingv2.MetricValueStatus
		if st != nil && st.Object != nil {
			current = &st.Object.Current
		}
		cur, target = hpaMetricValues(current, spec.Object.Target)
	case autoscalingv2.ExternalMetricSourceType:
		if spec.External == nil {
			break
		}
		name = spec.External.Metric.Name
		var current *autoscalingv2.MetricValueStatus
		if st != nil && st.External != nil {
			current = &st.External.Current
		}
		cur, target = hpaMetricValues(current, spec.External.Target)
	}
	if target == "" {
		return name, unknownMetric, "<auto>"
	}
	return name, cur, target
}

// hpaMetricValues picks the current/target pair matching the target's own type:
// a Utilization target compares percentages, an AverageValue or Value target
// compares quantities. An unset target returns an empty string, which the
// caller turns into `<auto>`.
func hpaMetricValues(current *autoscalingv2.MetricValueStatus, target autoscalingv2.MetricTarget) (cur, tgt string) {
	switch {
	case target.AverageUtilization != nil:
		cur = unknownMetric
		if current != nil && current.AverageUtilization != nil {
			cur = fmt.Sprintf("%d%%", *current.AverageUtilization)
		}
		return cur, fmt.Sprintf("%d%%", *target.AverageUtilization)
	case target.AverageValue != nil:
		cur = unknownMetric
		if current != nil && current.AverageValue != nil {
			cur = current.AverageValue.String()
		}
		return cur, target.AverageValue.String()
	case target.Value != nil:
		cur = unknownMetric
		if current != nil && current.Value != nil {
			cur = current.Value.String()
		}
		return cur, target.Value.String()
	}
	return unknownMetric, ""
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
