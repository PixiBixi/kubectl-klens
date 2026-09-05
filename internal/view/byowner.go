package view

import (
	"context"
	"strconv"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/PixiBixi/kubectl-klens/internal/kube"
)

// rolloutGVR is declared in rollouts.go and shared with this file - both read
// the same CRD.

// podsForView is the pod source every --by-owner-capable view (reqlim,
// no-limits, no-requests, images, probes, qos - see Command.ByOwner) reads
// through: the running pods normally, or one synthetic pod per workload when
// the flag is set.
//
// A synthetic pod is not a real one: Namespace/Name come from the controller,
// Spec is its pod template, Status is left zero, and a reserved annotation
// carries the replica count (see replicasOf). It works unmodified through
// every one of these views because none of them reads pod.Status directly -
// qosClass has an explicit spec-only fallback for exactly this case, and
// everything else (podContainers, a container's Readiness/Liveness/
// StartupProbe) reads the container spec. A view that reads runtime state must
// not be given ByOwner in the registry - TestByOwnerFlags is the guard.
//
// Reading four small controller lists instead of every pod is the whole point:
// on a 7209-pod cluster the controllers are 164 objects and ~700 kB against
// ~77 MB of pods (see openwiki/performance.md).
func podsForView(ctx context.Context, c kube.Clients, f kube.Flags) ([]corev1.Pod, error) {
	if !f.ByOwner {
		return kube.ListPods(ctx, c, f.Scope(), metav1.ListOptions{})
	}
	return workloadPods(ctx, c, f)
}

// replicasAnnotation is where a synthetic pod's replica count lives. It exists
// only on pods podsForView synthesizes - never on a real one, and never sent to
// an apiserver - and is read back by replicasOf within the same process.
const replicasAnnotation = "klens.io/replicas"

// workloadPods lists Deployments, StatefulSets, DaemonSets and, when the CRD is
// installed, Argo Rollouts, and turns each into one synthetic pod.
func workloadPods(ctx context.Context, c kube.Clients, f kube.Flags) ([]corev1.Pod, error) {
	var (
		deploys  []appsv1.Deployment
		stateful []appsv1.StatefulSet
		daemons  []appsv1.DaemonSet
		argo     []unstructured.Unstructured
	)
	scope := f.Scope()
	err := allLists(
		func() (err error) {
			deploys, err = kube.ListDeployments(ctx, c, scope, metav1.ListOptions{})
			return err
		},
		func() (err error) {
			stateful, err = kube.ListStatefulSets(ctx, c, scope, metav1.ListOptions{})
			return err
		},
		func() (err error) {
			daemons, err = kube.ListDaemonSets(ctx, c, scope, metav1.ListOptions{})
			return err
		},
		func() error {
			list, err := kube.ListCustom(ctx, c.Dynamic, rolloutGVR, scope, metav1.ListOptions{})
			// No Argo Rollouts CRD, or no access to it, is the normal case
			// rather than a failure: the other three kinds still make a useful
			// table, so the Rollout rows are simply absent.
			if meta.IsNoMatchError(err) || apierrors.IsNotFound(err) || apierrors.IsForbidden(err) {
				return nil
			}
			argo = list
			return err
		},
	)
	if err != nil {
		return nil, err
	}

	pods := make([]corev1.Pod, 0, len(deploys)+len(stateful)+len(daemons)+len(argo))
	for i := range deploys {
		d := &deploys[i]
		pods = append(pods, syntheticPod(d.Namespace, d.Name, replicasOrOne(d.Spec.Replicas), &d.Spec.Template.Spec))
	}
	for i := range stateful {
		s := &stateful[i]
		pods = append(pods, syntheticPod(s.Namespace, s.Name, replicasOrOne(s.Spec.Replicas), &s.Spec.Template.Spec))
	}
	for i := range daemons {
		d := &daemons[i]
		// A DaemonSet has no spec.replicas: its population is however many
		// nodes its selector and tolerations reach.
		pods = append(pods, syntheticPod(d.Namespace, d.Name, int(d.Status.DesiredNumberScheduled), &d.Spec.Template.Spec))
	}
	for i := range argo {
		u := &argo[i]
		spec, err := argoPodSpec(u)
		if err != nil {
			// One malformed Rollout must not take the rest of the table down
			// with it.
			continue
		}
		pods = append(pods, syntheticPod(u.GetNamespace(), u.GetName(), argoReplicas(u), spec))
	}
	return pods, nil
}

func syntheticPod(ns, name string, replicas int, spec *corev1.PodSpec) corev1.Pod {
	return corev1.Pod{
		Namespace:   ns,
		Name:        name,
		Annotations: map[string]string{replicasAnnotation: strconv.Itoa(replicas)},
		Spec:        *spec,
	}
}

// replicasOf reads back the replica count syntheticPod stashed. A pod with no
// such annotation (any real one) reads as 0, which is never rendered: it is
// only consulted from ownerCells, itself only called under --by-owner.
func replicasOf(p *corev1.Pod) int {
	n, _ := strconv.Atoi(p.Annotations[replicasAnnotation])
	return n
}

// replicaCell mutes a scaled-to-zero workload: its requests reserve nothing, so
// the numbers on the row are what it *would* cost, not what it costs.
func replicaCell(paint kube.Painter, n int) string {
	if n == 0 {
		return paint.Muted("0")
	}
	return strconv.Itoa(n)
}

// argoReplicas reads a Rollout's desired replica count. Argo defaults
// spec.replicas to 1 like the built-in controllers do, so an unset field means
// one replica, not zero.
func argoReplicas(u *unstructured.Unstructured) int {
	n, found, err := unstructured.NestedInt64(u.Object, "spec", "replicas")
	if err != nil || !found {
		return 1
	}
	return int(n)
}

// argoPodSpec converts a Rollout's unstructured spec.template.spec into a typed
// PodSpec, so it flows through podContainers exactly like the built-in kinds. A
// Rollout referencing a workloadRef instead of an inline template has no
// template at all, which is not an error: it yields an empty spec and so no
// container rows.
func argoPodSpec(u *unstructured.Unstructured) (*corev1.PodSpec, error) {
	raw, found, err := unstructured.NestedMap(u.Object, "spec", "template", "spec")
	if err != nil || !found {
		return &corev1.PodSpec{}, err
	}
	var spec corev1.PodSpec
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(raw, &spec); err != nil {
		return nil, err
	}
	return &spec, nil
}
