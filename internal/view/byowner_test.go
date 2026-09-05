package view

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"

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

// strimziPodSet mirrors the real shape: spec.pods is a list of complete pod
// manifests, one per replica, not a template.
func strimziPodSet(ns, name string, replicas int, ctrName, cpu string) *unstructured.Unstructured {
	pods := make([]any, 0, replicas)
	for i := range replicas {
		pods = append(pods, map[string]any{
			"apiVersion": "v1",
			"kind":       "Pod",
			"metadata":   map[string]any{"name": fmt.Sprintf("%s-%d", name, i)},
			"spec": map[string]any{
				"containers": []any{map[string]any{
					"name":      ctrName,
					"resources": map[string]any{"requests": map[string]any{"cpu": cpu}},
				}},
			},
		})
	}
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "core.strimzi.io/v1",
		"kind":       "StrimziPodSet",
		"metadata":   map[string]any{"name": name, "namespace": ns},
		"spec":       map[string]any{"pods": pods},
	}}
}

// TestByOwnerReadsStrimziPodSets: Kafka brokers, controllers and MirrorMaker run
// under a StrimziPodSet rather than a StatefulSet, so without this they were
// absent from every --by-owner view - including a MirrorMaker with no requests
// at all, which is exactly the row no-limits exists to surface.
func TestByOwnerReadsStrimziPodSets(t *testing.T) {
	c := argoClients(fake.NewClientset(), strimziPodSet("kafka", "bidder-edge-broker", 3, "kafka", "14"))
	var buf bytes.Buffer
	f := kube.Flags{Namespace: "kafka", ByOwner: true}
	if err := Reqlim(context.Background(), c, f, nil, &buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{"bidder-edge-broker", "kafka", "14"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q:\n%s", want, out)
		}
	}
	// One row for the set, and REPLICAS from the number of embedded pods.
	if got := strings.Count(out, "\n") - 1; got != 1 {
		t.Fatalf("want 1 row, got %d:\n%s", got, out)
	}
	if !strings.Contains(out, " 3 ") {
		t.Fatalf("want REPLICAS 3 from len(spec.pods):\n%s", out)
	}
}

// TestByOwnerStrimziMissingRequests is the finding this kind was added for.
func TestByOwnerStrimziMissingRequests(t *testing.T) {
	mm2 := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "core.strimzi.io/v1",
		"kind":       "StrimziPodSet",
		"metadata":   map[string]any{"name": "core-to-edge-mirroring-mirrormaker2", "namespace": "kafka"},
		"spec": map[string]any{"pods": []any{map[string]any{
			"spec": map[string]any{"containers": []any{map[string]any{"name": "mirrormaker2"}}},
		}}},
	}}
	c := argoClients(fake.NewClientset(), mm2)
	var buf bytes.Buffer
	f := kube.Flags{Namespace: "kafka", ByOwner: true}
	if err := NoRequests(context.Background(), c, f, nil, &buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "core-to-edge-mirroring-mirrormaker2") || !strings.Contains(out, "cpu,memory") {
		t.Fatalf("want the MirrorMaker flagged as missing both requests:\n%s", out)
	}
}

// TestByOwnerSurvivesEmptyStrimziPodSet: a set the operator has not populated
// yet has no pods to read, which is not an error.
func TestByOwnerSurvivesEmptyStrimziPodSet(t *testing.T) {
	empty := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "core.strimzi.io/v1",
		"kind":       "StrimziPodSet",
		"metadata":   map[string]any{"name": "not-ready", "namespace": "kafka"},
		"spec":       map[string]any{},
	}}
	c := argoClients(
		fake.NewClientset(specDeploy("kafka", "api", 1, container("main", "100m", ""))),
		empty,
	)
	var buf bytes.Buffer
	f := kube.Flags{Namespace: "kafka", ByOwner: true}
	if err := Reqlim(context.Background(), c, f, nil, &buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if strings.Contains(out, "not-ready") {
		t.Fatalf("an unpopulated StrimziPodSet has no containers to report:\n%s", out)
	}
	if !strings.Contains(out, "api") {
		t.Fatalf("the other workloads must still report:\n%s", out)
	}
}

// TestAbsentCRD covers the errors that make an optional custom resource simply
// absent rather than fatal. The dynamic fake cannot stand in for this: it panics
// on an unregistered LIST instead of returning the 404 a real apiserver sends.
func TestAbsentCRD(t *testing.T) {
	gr := schema.GroupResource{Group: "core.strimzi.io", Resource: "strimzipodsets"}
	for name, err := range map[string]error{
		"no CRD installed": apierrors.NewNotFound(gr, ""),
		"no RBAC on it":    apierrors.NewForbidden(gr, "", nil),
		"no REST mapping":  &meta.NoKindMatchError{},
	} {
		if !absentCRD(err) {
			t.Errorf("%s: want tolerated, got fatal (%v)", name, err)
		}
	}
	if absentCRD(apierrors.NewInternalError(errors.New("boom"))) {
		t.Error("a real server error must not be swallowed")
	}
	if absentCRD(nil) {
		t.Error("nil is not an absent CRD")
	}
}

// TestByOwnerFallsBackToLegacyStrimziVersion: a Strimzi predating the v1 API
// serves only v1beta2, and the rows must not vanish just because the cluster is
// a version behind. Driven with a reactor because the dynamic fake panics on an
// unregistered LIST instead of returning the 404 a real apiserver sends.
func TestByOwnerFallsBackToLegacyStrimziVersion(t *testing.T) {
	legacy := strimziPodSet("kafka", "old-broker", 2, "kafka", "4")
	legacy.Object["apiVersion"] = "core.strimzi.io/v1beta2"
	c := argoClients(fake.NewClientset(), legacy)

	dyn, ok := c.Dynamic.(*dynamicfake.FakeDynamicClient)
	if !ok {
		t.Fatalf("want a dynamic fake to attach the reactor to, got %T", c.Dynamic)
	}
	dyn.PrependReactor("list", "strimzipodsets", func(a k8stesting.Action) (bool, runtime.Object, error) {
		if a.GetResource().Version == "v1" {
			return true, nil, apierrors.NewNotFound(schema.GroupResource{
				Group: "core.strimzi.io", Resource: "strimzipodsets",
			}, "")
		}
		return false, nil, nil
	})

	var buf bytes.Buffer
	f := kube.Flags{Namespace: "kafka", ByOwner: true}
	if err := Reqlim(context.Background(), c, f, nil, &buf); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "old-broker") {
		t.Fatalf("want the v1beta2 pod set read through the fallback:\n%s", buf.String())
	}
}

func cnpgCluster(ns, name string, instances int64, cpu, mem string) *unstructured.Unstructured {
	res := map[string]any{}
	if cpu != "" || mem != "" {
		req := map[string]any{}
		if cpu != "" {
			req["cpu"] = cpu
		}
		if mem != "" {
			req["memory"] = mem
		}
		res["requests"] = req
	}
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "postgresql.cnpg.io/v1",
		"kind":       "Cluster",
		"metadata":   map[string]any{"name": name, "namespace": ns},
		"spec":       map[string]any{"instances": instances, "resources": res},
	}}
}

// TestByOwnerReadsCNPGClusters: CNPG owns its Postgres pods directly, so
// without this a production database is absent from every --by-owner view.
func TestByOwnerReadsCNPGClusters(t *testing.T) {
	c := argoClients(fake.NewClientset(), cnpgCluster("db", "postgresql-bidder-cluster", 3, "2", "8Gi"))
	var buf bytes.Buffer
	f := kube.Flags{Namespace: "db", ByOwner: true}
	if err := Reqlim(context.Background(), c, f, nil, &buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{"postgresql-bidder-cluster", "postgres", "bootstrap-controller", "2", "8Gi"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q:\n%s", want, out)
		}
	}
	// CNPG applies spec.resources to the init container too, so both rows carry
	// it - that is what makes the QoS class derived from them correct.
	if got := strings.Count(out, "8Gi"); got != 2 {
		t.Fatalf("want the resources on both containers, got %d:\n%s", got, out)
	}
}

// TestByOwnerCNPGWithoutResources is the finding the kind was added for: a
// three-instance production Postgres with nothing set is BestEffort, first out
// under memory pressure.
func TestByOwnerCNPGWithoutResources(t *testing.T) {
	c := argoClients(fake.NewClientset(), cnpgCluster("db", "postgresql-bidder-cluster", 3, "", ""))
	f := kube.Flags{Namespace: "db", ByOwner: true}

	var missing bytes.Buffer
	if err := NoRequests(context.Background(), c, f, nil, &missing); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(missing.String(), "postgresql-bidder-cluster") || !strings.Contains(missing.String(), "cpu,memory") {
		t.Fatalf("want the cluster flagged as missing both requests:\n%s", missing.String())
	}

	var quality bytes.Buffer
	if err := Qos(context.Background(), c, f, nil, &quality); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(quality.String(), "BestEffort") {
		t.Fatalf("want BestEffort computed from the empty resources:\n%s", quality.String())
	}
}

// TestByOwnerCNPGAbstainsOnProbesAndImages: a CNPG Cluster carries neither
// corev1 probes nor a resolved image, so reporting on them would invent
// findings - NO-PROBES for a cluster that has probes, and a "latest" tag on an
// empty repository. The row is skipped in those two views only.
func TestByOwnerCNPGAbstainsOnProbesAndImages(t *testing.T) {
	c := argoClients(
		fake.NewClientset(specDeploy("db", "sidecar", 1, container("main", "100m", ""))),
		cnpgCluster("db", "postgresql-bidder-cluster", 3, "2", "8Gi"),
	)
	f := kube.Flags{Namespace: "db", ByOwner: true}

	for name, run := range map[string]func() (string, error){
		"probes": func() (string, error) {
			var b bytes.Buffer
			err := Probes(context.Background(), c, f, nil, &b)
			return b.String(), err
		},
		"images": func() (string, error) {
			var b bytes.Buffer
			err := Images(context.Background(), c, f, nil, &b)
			return b.String(), err
		},
	} {
		out, err := run()
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if strings.Contains(out, "postgresql-bidder-cluster") {
			t.Errorf("%s must abstain on a CNPG cluster:\n%s", name, out)
		}
		if !strings.Contains(out, "sidecar") {
			t.Errorf("%s must still report the workloads it can answer for:\n%s", name, out)
		}
	}
}

// TestSpecKnows: only a row explicitly marked unknown abstains.
func TestSpecKnows(t *testing.T) {
	plain := &corev1.Pod{}
	if !specKnows(plain, "probes") || !specKnows(plain, "images") {
		t.Error("a pod with no marker answers for everything")
	}
	partial := &corev1.Pod{}
	partial.Annotations = map[string]string{unknownAnnotation: "probes,images"}
	if specKnows(partial, "probes") || specKnows(partial, "images") {
		t.Error("a marked aspect must abstain")
	}
	if !specKnows(partial, "resources") {
		t.Error("an unmarked aspect still answers")
	}
}
