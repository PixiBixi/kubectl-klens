package view

import (
	"bytes"
	"context"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/PixiBixi/kubectl-klens/internal/kube"
)

// qosPod builds a single-container pod whose requests and limits are given as
// quantity strings; an empty string leaves that one unset.
func qosPod(name, ns, class, reqCPU, limCPU, reqMem, limMem string) *corev1.Pod {
	return &corev1.Pod{
		Name: name, Namespace: ns,
		Spec: corev1.PodSpec{Containers: []corev1.Container{{
			Name:      "app",
			Resources: resourcesFor(reqCPU, limCPU, reqMem, limMem),
		}}},
		Status: corev1.PodStatus{QOSClass: corev1.PodQOSClass(class)},
	}
}

func resourcesFor(reqCPU, limCPU, reqMem, limMem string) corev1.ResourceRequirements {
	r := corev1.ResourceRequirements{Requests: corev1.ResourceList{}, Limits: corev1.ResourceList{}}
	for _, s := range []struct {
		list corev1.ResourceList
		name corev1.ResourceName
		val  string
	}{
		{r.Requests, corev1.ResourceCPU, reqCPU},
		{r.Limits, corev1.ResourceCPU, limCPU},
		{r.Requests, corev1.ResourceMemory, reqMem},
		{r.Limits, corev1.ResourceMemory, limMem},
	} {
		if s.val != "" {
			s.list[s.name] = resource.MustParse(s.val)
		}
	}
	return r
}

func TestQos(t *testing.T) {
	c := fake.NewClientset(
		qosPod("guaranteed", "app", "Guaranteed", "500m", "500m", "1Gi", "1Gi"),
		qosPod("burstable", "app", "Burstable", "100m", "1", "256Mi", "1Gi"),
		qosPod("cpu-only", "app", "Burstable", "100m", "1", "", ""),
		qosPod("besteffort", "app", "BestEffort", "", "", "", ""),
	)
	var buf bytes.Buffer
	if err := Qos(context.Background(), clients(c), kube.Flags{}, nil, &buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{
		"NS", "POD", "QOS", "REQ_CPU", "LIM_MEM", "VERDICT",
		"guaranteed", "GUARANTEED", "1Gi",
		"burstable", "BURSTABLE", "256Mi",
		"cpu-only", "NO-MEM-FLOOR",
		"besteffort", "EVICT-FIRST", "none",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("output missing %q:\n%s", want, out)
		}
	}
	// Riskiest last: the default sort is the verdict's risk order, so the
	// BestEffort pod sits nearest the prompt.
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if last := lines[len(lines)-1]; !strings.Contains(last, "EVICT-FIRST") {
		t.Fatalf("EVICT-FIRST must sort last, got %q", last)
	}
}

// TestQosSumsContainersAndSidecars checks the totals charge app containers plus
// native sidecars and leave plain init containers out: a startup-only spike is
// not what the pod holds once it is running.
func TestQosSumsContainersAndSidecars(t *testing.T) {
	always := corev1.ContainerRestartPolicyAlways
	pod := &corev1.Pod{
		Name: "multi", Namespace: "app",
		Spec: corev1.PodSpec{
			InitContainers: []corev1.Container{
				{Name: "migrate", Resources: resourcesFor("2", "2", "4Gi", "4Gi")},
				{Name: "proxy", RestartPolicy: &always, Resources: resourcesFor("100m", "100m", "128Mi", "128Mi")},
			},
			Containers: []corev1.Container{
				{Name: "app", Resources: resourcesFor("400m", "400m", "384Mi", "384Mi")},
			},
		},
		Status: corev1.PodStatus{QOSClass: corev1.PodQOSGuaranteed},
	}
	var buf bytes.Buffer
	if err := Qos(context.Background(), clients(fake.NewClientset(pod)), kube.Flags{}, nil, &buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "500m") || !strings.Contains(out, "512Mi") {
		t.Fatalf("want app+sidecar totals 500m/512Mi, got:\n%s", out)
	}
	if strings.Contains(out, "4Gi") {
		t.Fatalf("plain init container must not be charged:\n%s", out)
	}
}

// TestQosDerivesClassWithoutStatus covers objects that never went through
// admission, where status.qosClass is empty and the column would read blank.
func TestQosDerivesClassWithoutStatus(t *testing.T) {
	tests := []struct {
		name string
		pod  *corev1.Pod
		want string
	}{
		{"guaranteed", qosPod("g", "app", "", "1", "1", "1Gi", "1Gi"), "Guaranteed"},
		{"burstable", qosPod("b", "app", "", "1", "2", "1Gi", "2Gi"), "Burstable"},
		{"besteffort", qosPod("e", "app", "", "", "", "", ""), "BestEffort"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			if err := Qos(context.Background(), clients(fake.NewClientset(tc.pod)), kube.Flags{}, nil, &buf); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(buf.String(), tc.want) {
				t.Fatalf("want derived class %q, got:\n%s", tc.want, buf.String())
			}
		})
	}
}

func TestQosExcludesKubeSystemClusterWide(t *testing.T) {
	c := fake.NewClientset(
		qosPod("coredns", "kube-system", "BestEffort", "", "", "", ""),
		qosPod("web", "app", "BestEffort", "", "", "", ""),
	)
	var buf bytes.Buffer
	if err := Qos(context.Background(), clients(c), kube.Flags{}, nil, &buf); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(buf.String(), "coredns") {
		t.Fatalf("kube-system must be excluded cluster-wide:\n%s", buf.String())
	}
	if !strings.Contains(buf.String(), "web") {
		t.Fatalf("workload pod missing:\n%s", buf.String())
	}
}

func TestQosColor(t *testing.T) {
	c := fake.NewClientset(
		qosPod("guaranteed", "app", "Guaranteed", "1", "1", "1Gi", "1Gi"),
		qosPod("burstable", "app", "Burstable", "100m", "1", "256Mi", "1Gi"),
		qosPod("besteffort", "app", "BestEffort", "", "", "", ""),
	)
	var buf bytes.Buffer
	if err := Qos(context.Background(), clients(c), kube.Flags{Color: true}, nil, &buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{"\x1b[32mGUARANTEED", "\x1b[33mBURSTABLE", "\x1b[31mEVICT-FIRST"} {
		if !strings.Contains(out, want) {
			t.Fatalf("output missing colored %q:\n%q", want, out)
		}
	}
}
