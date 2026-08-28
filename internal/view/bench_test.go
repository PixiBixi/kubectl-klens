package view

import (
	"context"
	"fmt"
	"io"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/PixiBixi/kubectl-klens/internal/kube"
)

// End-to-end view benchmarks: a whole RunFunc, from the list calls to the
// rendered table, on the cluster shape the interval floor is justified against
// (6500 pods, see the Watch section of the README).
//
// What they can and cannot tell you: the fake clientset serves from memory, so
// there is no apiserver latency and no JSON decode - the two things that
// dominate a real run. It does DeepCopy every object it hands out, which a real
// client does not. So treat the absolute numbers as a *local processing* budget
// only, and read them as a ratchet: what matters is that a new view or a change
// to the shared Table does not move them. `make bench` prints them; keep the
// output of two runs and diff with benchstat.
const benchPods = 6500

// benchObjects builds a cluster of pods each mounting one PVC, with the claim
// sized so every one of them is mid-resize (the worst case for pvc-resize: no
// row gets filtered out).
func benchObjects(pods, pvcs int) []runtime.Object {
	objs := make([]runtime.Object, 0, pods+pvcs)
	for i := range pods {
		objs = append(objs, &corev1.Pod{
			Name:      fmt.Sprintf("deployment-abcdef-%d", i),
			Namespace: fmt.Sprintf("namespace-%d", i%40),
			Spec: corev1.PodSpec{
				NodeName: fmt.Sprintf("node-%d", i%250),
				Containers: []corev1.Container{{
					Name: "app",
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("100m")},
						Limits:   corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("500m")},
					},
				}},
				Volumes: []corev1.Volume{{
					Name: "data",
					PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
						ClaimName: fmt.Sprintf("data-%d", i),
					},
				}},
			},
			Status: corev1.PodStatus{
				Phase: corev1.PodRunning,
				ContainerStatuses: []corev1.ContainerStatus{{
					Name:         "app",
					RestartCount: int32(i % 5),
					State:        corev1.ContainerState{Running: &corev1.ContainerStateRunning{}},
				}},
			},
		})
	}
	class := "expandable"
	for i := range pvcs {
		objs = append(objs, &corev1.PersistentVolumeClaim{
			Name:      fmt.Sprintf("data-%d", i),
			Namespace: fmt.Sprintf("namespace-%d", i%40),
			Spec: corev1.PersistentVolumeClaimSpec{
				StorageClassName: &class,
				Resources: corev1.VolumeResourceRequirements{
					Requests: corev1.ResourceList{corev1.ResourceStorage: resource.MustParse("180Gi")},
				},
			},
			Status: corev1.PersistentVolumeClaimStatus{
				Phase:    corev1.ClaimBound,
				Capacity: corev1.ResourceList{corev1.ResourceStorage: resource.MustParse("150Gi")},
			},
		})
	}
	return objs
}

// benchSettledObjects is benchObjects with every claim already at its requested
// size, so pvc-resize filters all of them out.
func benchSettledObjects(pods, pvcs int) []runtime.Object {
	objs := benchObjects(pods, pvcs)
	for _, o := range objs {
		if pvc, ok := o.(*corev1.PersistentVolumeClaim); ok {
			pvc.Spec.Resources.Requests = pvc.Status.Capacity
		}
	}
	return objs
}

type benchRunFunc func(context.Context, kube.Clients, kube.Flags, []string, io.Writer) error

func benchView(b *testing.B, fn benchRunFunc, objs []runtime.Object) {
	b.Helper()
	cl := clients(fake.NewClientset(objs...))
	b.ReportAllocs()
	for b.Loop() {
		if err := fn(context.Background(), cl, kube.Flags{}, nil, io.Discard); err != nil {
			b.Fatal(err)
		}
	}
}

// PvcResize is the heaviest shape: two full lists joined on the pod side.
func BenchmarkPvcResize(b *testing.B) {
	benchView(b, PvcResize, benchObjects(benchPods, benchPods))
}

// PvcResizeSettled is the nominal case and the one that matters most: no claim
// is mid-resize, so the table is a header line. It must not cost a pod list.
func BenchmarkPvcResizeSettled(b *testing.B) {
	benchView(b, PvcResize, benchSettledObjects(benchPods, benchPods))
}

// PvcUnused walks three lists (PVCs, pods, StatefulSets).
func BenchmarkPvcUnused(b *testing.B) {
	benchView(b, PvcUnused, benchObjects(benchPods, benchPods))
}

// Restarts and Reqlim are the per-container fan-out: one row per container, so
// they stress Table.Row and the sort more than the join.
func BenchmarkRestarts(b *testing.B) { benchView(b, Restarts, benchObjects(benchPods, 0)) }
func BenchmarkReqlim(b *testing.B)   { benchView(b, Reqlim, benchObjects(benchPods, 0)) }

// Spread aggregates into a map keyed per owner, the other shared shape.
func BenchmarkSpread(b *testing.B) { benchView(b, Spread, benchObjects(benchPods, 0)) }
