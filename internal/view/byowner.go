package view

import (
	"context"
	"slices"
	"strconv"
	"strings"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/PixiBixi/kubectl-klens/internal/kube"
)

// rolloutGVR is declared in rollouts.go and shared with this file - both read
// the same CRD.

// strimziPodSetGVR is the Strimzi CRD that actually owns Kafka pods: brokers,
// controllers and MirrorMaker run under a StrimziPodSet, not a StatefulSet, so
// without it they are absent from every --by-owner view.
//
// v1 first: recent Strimzi serves both and warns on every v1beta2 list
// ("Version v1beta2 of the StrimziPodSet API is deprecated"), which a table
// command would print once per namespace it lists. Releases predating v1 serve
// only v1beta2, hence the fallback - a version that is merely old should not
// make the rows silently vanish.
var (
	strimziPodSetGVR       = schema.GroupVersionResource{Group: "core.strimzi.io", Version: "v1", Resource: "strimzipodsets"}
	strimziPodSetLegacyGVR = schema.GroupVersionResource{Group: "core.strimzi.io", Version: "v1beta2", Resource: "strimzipodsets"}
)

// cnpgClusterGVR is CloudNativePG, which owns its Postgres pods directly. It is
// the one kind here that carries no pod manifest at all: spec has instances,
// resources and an image reference, and the operator builds the rest. What it
// does carry is faithful - CNPG applies spec.resources to both the postgres
// container and the bootstrap-controller init container, so requests, limits
// and the QoS class computed from them are right. What it does not carry is
// marked unknown (see unknownForCNPG) rather than guessed.
var cnpgClusterGVR = schema.GroupVersionResource{Group: "postgresql.cnpg.io", Version: "v1", Resource: "clusters"}

// unknownForCNPG names the views a CNPG Cluster cannot answer. Its probes live
// in a CNPG-specific type, not a corev1.Probe, so a synthetic pod without them
// would make `probes` report NO-PROBES for a cluster that has them; and its
// image usually comes from an ImageCatalog the operator resolves, so `images`
// would print an empty repository and call its tag "latest". Both are worse
// than an absent row.
const unknownForCNPG = "probes,images"

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
// Reading a handful of small controller lists instead of every pod is the whole
// point: on a 7209-pod cluster the controllers are 164 objects and ~700 kB
// against ~77 MB of pods (see openwiki/performance.md).
//
// The kinds it knows - Deployment, StatefulSet, DaemonSet, Argo Rollout,
// Strimzi PodSet, CNPG Cluster - are a closed set, while the set of controllers
// that can own a pod is not, so a pod owned by any other custom resource has no
// row here. The unflagged view lists pods and therefore misses nothing.
//
// Adding a kind means finding where its pod spec lives, and they are not equal:
// a Deployment has a template, a StrimziPodSet embeds already-materialized pod
// manifests, and a CNPG Cluster has no pod anything - only instances, resources
// and an image reference. A kind that can answer part of the question marks the
// rest unknown (see unknownAnnotation) so the views that cannot be honest about
// it abstain, rather than printing an inference.
//
// A subtler limit has no marker because it is invisible from here: an operator
// can attach pods directly to a controller this does list, and those pods need
// not match its template. The Flink operator in native mode does exactly that -
// the FlinkDeployment's Deployment describes the JobManager (measured at 500m /
// 2Gi on one cluster) while the TaskManager pods it owns directly are 2 CPU /
// 8Gi and far more numerous. The row is present and describes only the
// template, so this mode understates such a workload. Detecting it would mean
// listing pods, which is the cost the flag exists to avoid; the unflagged view
// shows every pod at its real size.
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
		strimzi  []unstructured.Unstructured
		cnpg     []unstructured.Unstructured
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
			if absentCRD(err) {
				return nil
			}
			argo = list
			return err
		},
		func() (err error) {
			strimzi, err = listStrimziPodSets(ctx, c, scope)
			return err
		},
		func() error {
			list, err := kube.ListCustom(ctx, c.Dynamic, cnpgClusterGVR, scope, metav1.ListOptions{})
			if absentCRD(err) {
				return nil
			}
			cnpg = list
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
	for i := range strimzi {
		u := &strimzi[i]
		spec, replicas, err := strimziPodSpec(u)
		if err != nil || spec == nil {
			continue
		}
		pods = append(pods, syntheticPod(u.GetNamespace(), u.GetName(), replicas, spec))
	}
	for i := range cnpg {
		u := &cnpg[i]
		spec, err := cnpgPodSpec(u)
		if err != nil {
			continue
		}
		p := syntheticPod(u.GetNamespace(), u.GetName(), cnpgInstances(u), spec)
		p.Annotations[unknownAnnotation] = unknownForCNPG
		pods = append(pods, p)
	}
	return pods, nil
}

// cnpgInstances reads a Cluster's replica count. Unlike the built-in kinds
// there is no default to fall back on: spec.instances is required.
func cnpgInstances(u *unstructured.Unstructured) int {
	n, _, _ := unstructured.NestedInt64(u.Object, "spec", "instances")
	return int(n)
}

// cnpgPodSpec builds the two containers CNPG runs, both carrying the cluster's
// resources. This is a reconstruction rather than a read, and it is only safe
// for the resource columns: CNPG copies spec.resources onto the postgres
// container and the bootstrap-controller init container alike, so requests,
// limits and the QoS class derived from them match the running pod. Images and
// probes are deliberately left unset and the row is marked unknown for them.
func cnpgPodSpec(u *unstructured.Unstructured) (*corev1.PodSpec, error) {
	var res corev1.ResourceRequirements
	raw, found, err := unstructured.NestedMap(u.Object, "spec", "resources")
	if err != nil {
		return nil, err
	}
	if found {
		if err := runtime.DefaultUnstructuredConverter.FromUnstructured(raw, &res); err != nil {
			return nil, err
		}
	}
	return &corev1.PodSpec{
		InitContainers: []corev1.Container{{Name: "bootstrap-controller", Resources: res}},
		Containers:     []corev1.Container{{Name: "postgres", Resources: res}},
	}, nil
}

// listStrimziPodSets reads the current API version, falling back to the
// deprecated one for a Strimzi too old to serve it. Both absent means Strimzi is
// not installed here, which is not an error.
func listStrimziPodSets(ctx context.Context, c kube.Clients, scope kube.Scope) ([]unstructured.Unstructured, error) {
	list, err := kube.ListCustom(ctx, c.Dynamic, strimziPodSetGVR, scope, metav1.ListOptions{})
	if !absentCRD(err) {
		return list, err
	}
	list, err = kube.ListCustom(ctx, c.Dynamic, strimziPodSetLegacyGVR, scope, metav1.ListOptions{})
	if absentCRD(err) {
		return nil, nil
	}
	return list, err
}

// absentCRD reports the errors that mean "this custom resource is not something
// we can read here", which for an optional CRD is the normal case rather than a
// failure: the built-in kinds still make a useful table, so those rows are
// simply absent.
func absentCRD(err error) bool {
	return meta.IsNoMatchError(err) || apierrors.IsNotFound(err) || apierrors.IsForbidden(err)
}

// strimziPodSpec reads a StrimziPodSet. Unlike every other kind here it carries
// no pod *template*: spec.pods is a list of complete, already-materialized pod
// manifests, one per replica, differing in name and volumes. The first one is
// the representative spec and the list length is the replica count.
//
// Taking the first is the same approximation the other kinds make implicitly -
// they have one template for all replicas. Strimzi updates its pods one at a
// time, so mid-upgrade the first pod may carry the old spec while later ones
// carry the new; that is the usual --by-owner caveat, not a Strimzi-specific
// one.
func strimziPodSpec(u *unstructured.Unstructured) (*corev1.PodSpec, int, error) {
	items, found, err := unstructured.NestedSlice(u.Object, "spec", "pods")
	if err != nil || !found || len(items) == 0 {
		return nil, 0, err
	}
	first, ok := items[0].(map[string]any)
	if !ok {
		return nil, 0, nil
	}
	raw, found, err := unstructured.NestedMap(first, "spec")
	if err != nil || !found {
		return nil, 0, err
	}
	var spec corev1.PodSpec
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(raw, &spec); err != nil {
		return nil, 0, err
	}
	return &spec, len(items), nil
}

func syntheticPod(ns, name string, replicas int, spec *corev1.PodSpec) corev1.Pod {
	return corev1.Pod{
		Namespace:   ns,
		Name:        name,
		Annotations: map[string]string{replicasAnnotation: strconv.Itoa(replicas)},
		Spec:        *spec,
	}
}

// unknownAnnotation lists the aspects a synthetic pod cannot speak for, comma
// separated, when its source resource carries no pod manifest. Views consult it
// through specKnows and skip the row rather than report a value they inferred.
const unknownAnnotation = "klens.io/unknown"

// specKnows reports whether a pod can answer for the given aspect. Every real
// pod, and every synthetic one built from a genuine pod template, answers for
// all of them.
func specKnows(p *corev1.Pod, aspect string) bool {
	unknown := p.Annotations[unknownAnnotation]
	if unknown == "" {
		return true
	}
	return !slices.Contains(strings.Split(unknown, ","), aspect)
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
