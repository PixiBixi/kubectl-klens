package view

import (
	"bytes"
	"context"
	"strings"
	"testing"

	autoscalingv2 "k8s.io/api/autoscaling/v2"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/PixiBixi/kubectl-klens/internal/kube"
)

func ptr32(n int32) *int32 { return new(n) }

func TestHpaVerdict(t *testing.T) {
	blind := []autoscalingv2.HorizontalPodAutoscalerCondition{{Type: autoscalingv2.ScalingActive, Status: corev1.ConditionFalse}}
	cases := []struct {
		name        string
		spec        autoscalingv2.HorizontalPodAutoscalerSpec
		status      autoscalingv2.HorizontalPodAutoscalerStatus
		wantVerdict string
		wantSev     string
	}{
		{"no-metrics", autoscalingv2.HorizontalPodAutoscalerSpec{MinReplicas: ptr32(1), MaxReplicas: 10}, autoscalingv2.HorizontalPodAutoscalerStatus{CurrentReplicas: 3, DesiredReplicas: 3, Conditions: blind}, "NO-METRICS", "bad"},
		{"maxed", autoscalingv2.HorizontalPodAutoscalerSpec{MinReplicas: ptr32(1), MaxReplicas: 5}, autoscalingv2.HorizontalPodAutoscalerStatus{CurrentReplicas: 5, DesiredReplicas: 5}, "MAXED", "bad"},
		{"scaling", autoscalingv2.HorizontalPodAutoscalerSpec{MinReplicas: ptr32(1), MaxReplicas: 10}, autoscalingv2.HorizontalPodAutoscalerStatus{CurrentReplicas: 2, DesiredReplicas: 4}, "SCALING", "warn"},
		{"at-min default", autoscalingv2.HorizontalPodAutoscalerSpec{MaxReplicas: 10}, autoscalingv2.HorizontalPodAutoscalerStatus{CurrentReplicas: 1, DesiredReplicas: 1}, "AT-MIN", "muted"},
		{"at-min explicit", autoscalingv2.HorizontalPodAutoscalerSpec{MinReplicas: ptr32(2), MaxReplicas: 10}, autoscalingv2.HorizontalPodAutoscalerStatus{CurrentReplicas: 2, DesiredReplicas: 2}, "AT-MIN", "muted"},
		{"ok", autoscalingv2.HorizontalPodAutoscalerSpec{MinReplicas: ptr32(1), MaxReplicas: 10}, autoscalingv2.HorizontalPodAutoscalerStatus{CurrentReplicas: 3, DesiredReplicas: 3}, "OK", "ok"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotV, gotS := hpaVerdict(tc.spec, tc.status)
			if gotV != tc.wantVerdict || gotS != tc.wantSev {
				t.Fatalf("hpaVerdict = (%q,%q), want (%q,%q)", gotV, gotS, tc.wantVerdict, tc.wantSev)
			}
		})
	}
}

func hpaFixture(name string, min *int32, max, cur, des int32, conds []autoscalingv2.HorizontalPodAutoscalerCondition) *autoscalingv2.HorizontalPodAutoscaler {
	return &autoscalingv2.HorizontalPodAutoscaler{
		Name: name, Namespace: "default",
		Spec: autoscalingv2.HorizontalPodAutoscalerSpec{
			ScaleTargetRef: autoscalingv2.CrossVersionObjectReference{Kind: "Deployment", Name: name},
			MinReplicas:    min,
			MaxReplicas:    max,
		},
		Status: autoscalingv2.HorizontalPodAutoscalerStatus{CurrentReplicas: cur, DesiredReplicas: des, Conditions: conds},
	}
}

func TestHpa(t *testing.T) {
	maxed := hpaFixture("kafka", ptr32(1), 5, 5, 5, nil)
	ok := hpaFixture("api", ptr32(1), 10, 3, 3, nil)
	c := fake.NewClientset(maxed, ok)

	var buf bytes.Buffer
	if err := Hpa(context.Background(), clients(c), kube.Flags{Namespace: "default"}, nil, &buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{"MAXED", "OK", "Deployment/kafka", "Deployment/api"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q:\n%s", want, out)
		}
	}
	// Default verdict sort: least-risky first, so OK (api) ranks before
	// MAXED (kafka), which sinks toward the prompt.
	if strings.Index(out, "kafka") < strings.Index(out, "api") {
		t.Fatalf("expected verdict-sort default (api before kafka):\n%s", out)
	}
}

func TestHpaColor(t *testing.T) {
	maxed := hpaFixture("kafka", ptr32(1), 5, 5, 5, nil)
	ok := hpaFixture("api", ptr32(1), 10, 3, 3, nil)
	c := fake.NewClientset(maxed, ok)

	var buf bytes.Buffer
	if err := Hpa(context.Background(), clients(c), kube.Flags{Namespace: "default", Color: true}, nil, &buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{"\x1b[31mMAXED\x1b[0m", "\x1b[32mOK\x1b[0m"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing colored token %q:\n%s", want, out)
		}
	}
}

func resourceMetric(name corev1.ResourceName, target int32) autoscalingv2.MetricSpec {
	return autoscalingv2.MetricSpec{
		Type: autoscalingv2.ResourceMetricSourceType,
		Resource: &autoscalingv2.ResourceMetricSource{
			Name:   name,
			Target: autoscalingv2.MetricTarget{Type: autoscalingv2.UtilizationMetricType, AverageUtilization: ptr32(target)},
		},
	}
}

func resourceStatus(name corev1.ResourceName, current int32) autoscalingv2.MetricStatus {
	return autoscalingv2.MetricStatus{
		Type: autoscalingv2.ResourceMetricSourceType,
		Resource: &autoscalingv2.ResourceMetricStatus{
			Name:    name,
			Current: autoscalingv2.MetricValueStatus{AverageUtilization: ptr32(current)},
		},
	}
}

func TestHpaTargets(t *testing.T) {
	qty := resource.MustParse("500")
	cases := []struct {
		name     string
		specs    []autoscalingv2.MetricSpec
		statuses []autoscalingv2.MetricStatus
		want     string
	}{
		{"none", nil, nil, "<none>"},
		{
			"cpu utilization",
			[]autoscalingv2.MetricSpec{resourceMetric(corev1.ResourceCPU, 70)},
			[]autoscalingv2.MetricStatus{resourceStatus(corev1.ResourceCPU, 67)},
			"cpu: 67%/70%",
		},
		{
			"missing status",
			[]autoscalingv2.MetricSpec{resourceMetric(corev1.ResourceCPU, 70)},
			nil,
			"cpu: <unknown>/70%",
		},
		{
			"two metrics",
			[]autoscalingv2.MetricSpec{resourceMetric(corev1.ResourceCPU, 70), resourceMetric(corev1.ResourceMemory, 80)},
			[]autoscalingv2.MetricStatus{resourceStatus(corev1.ResourceCPU, 67), resourceStatus(corev1.ResourceMemory, 42)},
			"cpu: 67%/70%, memory: 42%/80%",
		},
		{
			"container resource",
			[]autoscalingv2.MetricSpec{{
				Type: autoscalingv2.ContainerResourceMetricSourceType,
				ContainerResource: &autoscalingv2.ContainerResourceMetricSource{
					Name: corev1.ResourceCPU, Container: "app",
					Target: autoscalingv2.MetricTarget{Type: autoscalingv2.UtilizationMetricType, AverageUtilization: ptr32(60)},
				},
			}},
			[]autoscalingv2.MetricStatus{{
				Type: autoscalingv2.ContainerResourceMetricSourceType,
				ContainerResource: &autoscalingv2.ContainerResourceMetricStatus{
					Name: corev1.ResourceCPU, Container: "app",
					Current: autoscalingv2.MetricValueStatus{AverageUtilization: ptr32(12)},
				},
			}},
			"cpu(app): 12%/60%",
		},
		{
			"external average value",
			[]autoscalingv2.MetricSpec{{
				Type: autoscalingv2.ExternalMetricSourceType,
				External: &autoscalingv2.ExternalMetricSource{
					Metric: autoscalingv2.MetricIdentifier{Name: "queue_depth"},
					Target: autoscalingv2.MetricTarget{Type: autoscalingv2.AverageValueMetricType, AverageValue: &qty},
				},
			}},
			[]autoscalingv2.MetricStatus{{
				Type: autoscalingv2.ExternalMetricSourceType,
				External: &autoscalingv2.ExternalMetricStatus{
					Metric:  autoscalingv2.MetricIdentifier{Name: "queue_depth"},
					Current: autoscalingv2.MetricValueStatus{AverageValue: &qty},
				},
			}},
			"queue_depth: 500/500",
		},
		{
			"pods target unset reads auto",
			[]autoscalingv2.MetricSpec{{
				Type: autoscalingv2.PodsMetricSourceType,
				Pods: &autoscalingv2.PodsMetricSource{Metric: autoscalingv2.MetricIdentifier{Name: "rps"}},
			}},
			nil,
			"rps: <unknown>/<auto>",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := hpaTargets(kube.NewPainter(kube.Flags{}), tc.specs, tc.statuses); got != tc.want {
				t.Fatalf("hpaTargets = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestHpaTargetsColumn(t *testing.T) {
	h := hpaFixture("api", ptr32(1), 10, 3, 3, nil)
	h.Spec.Metrics = []autoscalingv2.MetricSpec{resourceMetric(corev1.ResourceCPU, 70)}
	h.Status.CurrentMetrics = []autoscalingv2.MetricStatus{resourceStatus(corev1.ResourceCPU, 67)}
	c := fake.NewClientset(h)

	var buf bytes.Buffer
	if err := Hpa(context.Background(), clients(c), kube.Flags{Namespace: "default"}, nil, &buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{"TARGETS", "cpu: 67%/70%"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q:\n%s", want, out)
		}
	}
}
