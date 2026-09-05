package view

import (
	"bytes"
	"context"
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/PixiBixi/kubectl-klens/internal/kube"
)

// metaObj is the ObjectMeta every workload fixture in this file shares.
func metaObj(ns, name string) metav1.ObjectMeta {
	return metav1.ObjectMeta{Namespace: ns, Name: name}
}

func specDeploy(ns, name string, replicas int32, ctrs ...corev1.Container) *appsv1.Deployment {
	return &appsv1.Deployment{
		ObjectMeta: metaObj(ns, name),
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{Containers: ctrs}},
		},
	}
}

func TestReqlimByOwnerReadsWorkloadSpecs(t *testing.T) {
	c := fake.NewClientset(specDeploy("prod", "api", 5, container("main", "100m", "512Mi")))

	var plain bytes.Buffer
	if err := Reqlim(context.Background(), clients(c), kube.Flags{Namespace: "prod"}, nil, &plain); err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(plain.String(), "\n") - 1; got != 0 {
		t.Fatalf("want no rows without --by-owner - a Deployment is not a pod:\n%s", plain.String())
	}

	var owned bytes.Buffer
	f := kube.Flags{Namespace: "prod", ByOwner: true}
	if err := Reqlim(context.Background(), clients(c), f, nil, &owned); err != nil {
		t.Fatal(err)
	}
	out := owned.String()
	if got := strings.Count(out, "\n") - 1; got != 1 {
		t.Fatalf("want 1 row with --by-owner, got %d:\n%s", got, out)
	}
	for _, want := range []string{"WORKLOAD", "REPLICAS", "api", "5", "100m", "512Mi"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q:\n%s", want, out)
		}
	}
	header, _, _ := strings.Cut(out, "\n")
	if strings.Contains(header, "POD ") || strings.HasSuffix(strings.TrimSpace(strings.Fields(header)[1]), "POD") {
		t.Fatalf("the POD column must be gone under --by-owner:\n%s", out)
	}
}

// TestReqlimByOwnerScaledToZero: a scaled-to-zero Deployment's requests reserve
// nothing, so its replica count is muted rather than read as a live cost.
func TestReqlimByOwnerScaledToZero(t *testing.T) {
	c := fake.NewClientset(specDeploy("prod", "idle", 0, container("main", "8", "")))
	var buf bytes.Buffer
	f := kube.Flags{Namespace: "prod", ByOwner: true, Color: true}
	if err := Reqlim(context.Background(), clients(c), f, nil, &buf); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "\x1b[") {
		t.Fatalf("want the zero replica count muted:\n%q", buf.String())
	}
}

func statefulSet(ns, name string, replicas int32, ctrs ...corev1.Container) *appsv1.StatefulSet {
	return &appsv1.StatefulSet{
		ObjectMeta: metaObj(ns, name),
		Spec: appsv1.StatefulSetSpec{
			Replicas: &replicas,
			Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{Containers: ctrs}},
		},
	}
}

func daemonSet(ns, name string, desired int32, ctrs ...corev1.Container) *appsv1.DaemonSet {
	return &appsv1.DaemonSet{
		ObjectMeta: metaObj(ns, name),
		Spec:       appsv1.DaemonSetSpec{Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{Containers: ctrs}}},
		Status:     appsv1.DaemonSetStatus{DesiredNumberScheduled: desired},
	}
}

// TestReqlimByOwnerCoversAllFourKinds: a DaemonSet has no spec.replicas, so its
// REPLICAS has to come from status instead - the one place the four kinds are
// not interchangeable.
func TestReqlimByOwnerCoversAllFourKinds(t *testing.T) {
	c := fake.NewClientset(
		specDeploy("prod", "api", 3, container("main", "100m", "")),
		statefulSet("prod", "db", 2, container("postgres", "1", "")),
		daemonSet("prod", "agent", 250, container("agent", "10m", "")),
	)
	var buf bytes.Buffer
	f := kube.Flags{Namespace: "prod", ByOwner: true}
	if err := Reqlim(context.Background(), clients(c), f, nil, &buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{"api", "db", "agent", "250"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q:\n%s", want, out)
		}
	}
	if got := strings.Count(out, "\n") - 1; got != 3 {
		t.Fatalf("want 3 rows, got %d:\n%s", got, out)
	}
}

// TestByOwnerExcludesKubeSystemOnlyWhenWide mirrors the pod path: -A hides
// operator noise, an explicit -n kube-system does not.
func TestByOwnerExcludesKubeSystemOnlyWhenWide(t *testing.T) {
	c := fake.NewClientset(
		specDeploy("kube-system", "kube-dns", 2, container("dns", "100m", "")),
		specDeploy("prod", "api", 1, container("main", "100m", "")),
	)
	var wide bytes.Buffer
	if err := Reqlim(context.Background(), clients(c), kube.Flags{AllNamespaces: true, ByOwner: true}, nil, &wide); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(wide.String(), "kube-dns") {
		t.Fatalf("kube-system must be excluded from -A:\n%s", wide.String())
	}
	var scoped bytes.Buffer
	if err := Reqlim(context.Background(), clients(c), kube.Flags{Namespace: "kube-system", ByOwner: true}, nil, &scoped); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(scoped.String(), "kube-dns") {
		t.Fatalf("-n kube-system must show it:\n%s", scoped.String())
	}
}

// TestByOwnerSortAcceptsEitherName: which of pod/workload is the live header
// depends on a flag, so --sort must accept both in both modes rather than
// failing for a reason unrelated to the sort.
func TestByOwnerSortAcceptsEitherName(t *testing.T) {
	c := fake.NewClientset(
		specDeploy("prod", "zeta", 1, container("main", "100m", "")),
		specDeploy("prod", "alpha", 1, container("main", "200m", "")),
	)
	for _, tc := range []struct {
		name string
		f    kube.Flags
	}{
		{"pod under --by-owner", kube.Flags{Namespace: "prod", ByOwner: true, Sort: "pod"}},
		{"workload under --by-owner", kube.Flags{Namespace: "prod", ByOwner: true, Sort: "workload"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			if err := Reqlim(context.Background(), clients(c), tc.f, nil, &buf); err != nil {
				t.Fatal(err)
			}
			lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
			if len(lines) != 3 {
				t.Fatalf("want 2 rows, got:\n%s", buf.String())
			}
			if !strings.Contains(lines[1], "alpha") {
				t.Fatalf("want alpha sorted first, got:\n%s", buf.String())
			}
		})
	}
}

// TestQosByOwnerComputesClassFromSpec: a synthetic pod's status is zero, which
// is exactly what qosClass's from-spec fallback exists for.
func TestQosByOwnerComputesClassFromSpec(t *testing.T) {
	guaranteed := corev1.Container{
		Name: "main",
		Resources: corev1.ResourceRequirements{
			Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("1"), corev1.ResourceMemory: resource.MustParse("1Gi")},
			Limits:   corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("1"), corev1.ResourceMemory: resource.MustParse("1Gi")},
		},
	}
	c := fake.NewClientset(
		specDeploy("prod", "guaranteed", 1, guaranteed),
		specDeploy("prod", "besteffort", 1, corev1.Container{Name: "main"}),
	)
	var buf bytes.Buffer
	f := kube.Flags{Namespace: "prod", ByOwner: true}
	if err := Qos(context.Background(), clients(c), f, nil, &buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "Guaranteed") || !strings.Contains(out, "BestEffort") {
		t.Fatalf("want both QoS classes computed from spec:\n%s", out)
	}
}

// TestProbesByOwnerReadsTemplateProbes: probe presence is a spec property, so
// it reads straight off the workload template.
func TestProbesByOwnerReadsTemplateProbes(t *testing.T) {
	withProbe := container("main", "100m", "")
	withProbe.ReadinessProbe = &corev1.Probe{HTTPGet: &corev1.HTTPGetAction{Path: "/healthz"}}
	c := fake.NewClientset(specDeploy("prod", "api", 1, withProbe))
	var buf bytes.Buffer
	f := kube.Flags{Namespace: "prod", ByOwner: true}
	if err := Probes(context.Background(), clients(c), f, nil, &buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "http") || !strings.Contains(out, "NO-LIVENESS") {
		t.Fatalf("want the readiness probe read and liveness flagged missing:\n%s", out)
	}
}

// TestImagesByOwner covers the one --by-owner view whose column is PODNAME
// rather than POD.
func TestImagesByOwner(t *testing.T) {
	c := fake.NewClientset(specDeploy("prod", "api", 3, container("main", "100m", "")))
	var buf bytes.Buffer
	f := kube.Flags{Namespace: "prod", ByOwner: true}
	if err := Images(context.Background(), clients(c), f, nil, &buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "WORKLOAD") || strings.Contains(out, "PODNAME") {
		t.Fatalf("want PODNAME replaced by WORKLOAD:\n%s", out)
	}
	if got := strings.Count(out, "\n") - 1; got != 1 {
		t.Fatalf("want 1 row, got %d:\n%s", got, out)
	}
}

// TestNoLimitsNoRequestsByOwner covers the two views sharing reportMissing.
func TestNoLimitsNoRequestsByOwner(t *testing.T) {
	c := fake.NewClientset(
		specDeploy("prod", "unbounded", 2, corev1.Container{Name: "main"}),
	)
	f := kube.Flags{Namespace: "prod", ByOwner: true}
	var limits, requests bytes.Buffer
	if err := NoLimits(context.Background(), clients(c), f, nil, &limits); err != nil {
		t.Fatal(err)
	}
	if err := NoRequests(context.Background(), clients(c), f, nil, &requests); err != nil {
		t.Fatal(err)
	}
	for _, out := range []string{limits.String(), requests.String()} {
		if !strings.Contains(out, "unbounded") || !strings.Contains(out, "REPLICAS") {
			t.Fatalf("want the workload flagged with a REPLICAS column:\n%s", out)
		}
	}
}

func templatedRollout(ns, name string, replicas int64, ctrName, cpu string) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "argoproj.io/v1alpha1",
		"kind":       "Rollout",
		"metadata":   map[string]any{"name": name, "namespace": ns},
		"spec": map[string]any{
			"replicas": replicas,
			"template": map[string]any{"spec": map[string]any{
				"containers": []any{map[string]any{
					"name":      ctrName,
					"resources": map[string]any{"requests": map[string]any{"cpu": cpu}},
				}},
			}},
		},
	}}
}

// TestByOwnerReadsArgoRollouts covers the one kind that goes through the
// dynamic client, including the unstructured -> PodSpec conversion.
func TestByOwnerReadsArgoRollouts(t *testing.T) {
	c := argoClients(fake.NewClientset(), templatedRollout("prod", "canary", 4, "web", "250m"))
	var buf bytes.Buffer
	f := kube.Flags{Namespace: "prod", ByOwner: true}
	if err := Reqlim(context.Background(), c, f, nil, &buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{"canary", "web", "250m", "4"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q:\n%s", want, out)
		}
	}
}

// TestByOwnerSurvivesRolloutWithoutTemplate: a Rollout pointing at a
// workloadRef has no inline template. That is valid, not an error, and must not
// take the rest of the table down.
func TestByOwnerSurvivesRolloutWithoutTemplate(t *testing.T) {
	ref := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "argoproj.io/v1alpha1",
		"kind":       "Rollout",
		"metadata":   map[string]any{"name": "refd", "namespace": "prod"},
		"spec":       map[string]any{"workloadRef": map[string]any{"kind": "Deployment", "name": "api"}},
	}}
	c := argoClients(fake.NewClientset(specDeploy("prod", "api", 1, container("main", "100m", ""))), ref)
	var buf bytes.Buffer
	f := kube.Flags{Namespace: "prod", ByOwner: true}
	if err := Reqlim(context.Background(), c, f, nil, &buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if strings.Contains(out, "refd") {
		t.Fatalf("a Rollout with no inline template has no containers to report:\n%s", out)
	}
	if !strings.Contains(out, "api") {
		t.Fatalf("the other workloads must still report:\n%s", out)
	}
}

// TestByOwnerWithoutDynamicClient: no dynamic client (a test, or a caller that
// never built one) means no Rollout rows, not a failure.
func TestByOwnerWithoutDynamicClient(t *testing.T) {
	c := kube.Clients{Interface: fake.NewClientset(specDeploy("prod", "api", 1, container("main", "100m", "")))}
	var buf bytes.Buffer
	f := kube.Flags{Namespace: "prod", ByOwner: true}
	if err := Reqlim(context.Background(), c, f, nil, &buf); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "api") {
		t.Fatalf("want the typed workloads:\n%s", buf.String())
	}
}

func container(name, cpu, mem string) corev1.Container {
	c := corev1.Container{Name: name}
	if cpu != "" {
		c.Resources.Requests = corev1.ResourceList{corev1.ResourceCPU: resource.MustParse(cpu)}
	}
	if mem != "" {
		c.Resources.Limits = corev1.ResourceList{corev1.ResourceMemory: resource.MustParse(mem)}
	}
	return c
}
