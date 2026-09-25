package kube

import (
	"cmp"
	"context"
	"slices"
	"sync"

	appsv1 "k8s.io/api/apps/v1"
	autoscalingv2 "k8s.io/api/autoscaling/v2"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	networkingv1 "k8s.io/api/networking/v1"
	policyv1 "k8s.io/api/policy/v1"
	storagev1 "k8s.io/api/storage/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
)

// pageBounded issues one page request while holding a slot in inFlight. The slot
// is held for the request alone, not for a whole paged walk: paging is
// sequential per collection, so a walk holding its slot end to end would let a
// handful of large collections monopolize the bound.
func pageBounded[T any](page func(metav1.ListOptions) ([]T, metav1.ListMeta, error), opts metav1.ListOptions) ([]T, metav1.ListMeta, error) {
	inFlight <- struct{}{}
	defer func() { <-inFlight }()
	return page(opts)
}

// ChunkSize is how many objects each List request asks for. Its job is to stop
// the apiserver building an entire unbounded collection in one response, which
// on a cluster with tens of thousands of pods spikes memory on both ends.
//
// It is 2000, not the 500 kubectl uses, because paging is *sequential*: the next
// request needs the previous response's continue token, so the page count is
// round trips in series. Measured on a 7209-pod GKE cluster, `reqlim -A`:
//
//	500  -> 15 pages, ~4.8s     2000 -> 4 pages, ~2.6s
//
// kubectl's 500 buys a memory ceiling klens does not get anyway: it accumulates
// the whole collection before rendering, so the page size barely moves peak RSS
// (373-410 MiB at 500 against 351-397 MiB at 2000, same runs). What 500 does buy
// here is 11 extra round trips.
//
// Guarded by TestChunkSizeBounded - the value must stay bounded, the bound is
// what protects the apiserver.
const ChunkSize = 2000

// MaxInFlight bounds how many List requests one klens invocation has open at
// once. Nothing else does: cfg.QPS is -1 on purpose (see client.go), and the two
// fan-out layers multiply - a view issuing 10 concurrent lists (unused-config)
// under a glob matching MaxNamespaceFanout namespaces puts 160 requests on the
// wire at the same instant. HTTP/2 multiplexes them onto one connection so the
// client survives it, but a shared apiserver answering that burst is exactly
// what API Priority and Fairness starts queuing.
//
// 32 is the knee. Measured on a 7209-pod GKE cluster with
// `unused-config -n 'be-m*'` (10 lists x 7 namespaces = 70 requests unbounded),
// medians over four runs:
//
//	16 -> 1.53s    32 -> 0.98s    48 -> 0.97s    64 -> 1.01s    unbounded -> 0.82s
//
// So 16 costs 60% and 48 buys nothing over 32. 32 keeps nearly all the speed
// while capping the worst-case burst at a fifth of what it was.
const MaxInFlight = 32

// inFlight is that bound, as a counting semaphore. It is package-level because
// the limit is per process, not per call: every List in the binary goes through
// listAll, which is the one place that can see them all.
var inFlight = make(chan struct{}, MaxInFlight)

// listAll drains a paginated collection, following the server's continue token
// until it stops handing one out. Callers get the full slice, so paging stays an
// implementation detail of this package.
//
// A collection that fits in one page is handed back as the server's own slice,
// with no copy - that is the common case (most namespaces hold far fewer than
// ChunkSize objects) and these objects are fat: a corev1.Pod is ~1.2 kB.
//
// A continue token can expire mid-walk on a very slow client (the apiserver
// returns 410 Gone); that surfaces as an error rather than a silent short read,
// which is the right outcome for a one-shot CLI that finishes in seconds.
func listAll[T any](opts metav1.ListOptions, page func(metav1.ListOptions) ([]T, metav1.ListMeta, error)) ([]T, error) {
	opts.Limit = ChunkSize
	items, meta, err := pageBounded(page, opts)
	if err != nil {
		return nil, err
	}
	if meta.Continue == "" {
		return items, nil
	}
	all := items
	// A paginated response reports how many objects are still to come, so the
	// accumulator can be sized once instead of doubling its way there.
	if n := meta.RemainingItemCount; n != nil && *n > 0 {
		all = make([]T, 0, len(items)+int(*n))
		all = append(all, items...)
	}
	for meta.Continue != "" {
		opts.Continue = meta.Continue
		var next []T
		next, meta, err = pageBounded(page, opts)
		if err != nil {
			return nil, err
		}
		all = append(all, next...)
	}
	return all, nil
}

// MaxNamespaceFanout caps how many per-namespace Lists listScoped issues before
// falling back to a single cluster-wide one it filters locally. It bounds both
// the concurrent requests and the pathological case: `-n '*'` on a 400-namespace
// cluster would otherwise be 400 requests to rebuild a list the apiserver serves
// in one.
//
// 16 is where the two stop being comparable. Measured on the 6500-pod / 40-
// namespace bench shape (see internal/view/bench_test.go), against the 11.4ms
// and 42.9MiB of the cluster-wide list: 16 namespaces fan out in 6.4ms / 20.4MiB,
// 32 in 12.9ms / 40.8MiB, all 40 in 16.5ms / 51.0MiB. So the fan-out wins while
// it covers well under half the cluster and loses once it covers most of it -
// and a matched set of 16 is under half of any cluster big enough for this to
// matter.
//
// Those numbers understate the fan-out twice over, which is why the cap is not
// lower: the fake clientset transfers nothing, while a real targeted List also
// skips the bytes and the protobuf decode of every namespace it did not match;
// and paging makes the cluster-wide list *sequential* (13 requests for 6500
// pods, per openwiki/performance.md) where the fan-out's are concurrent.
const MaxNamespaceFanout = 16

// listScoped runs page once per namespace in the scope and concatenates the
// results, so a glob-expanded -n fetches only the namespaces it matched.
//
// The two degenerate scopes take the original single-List path with no extra
// work: a cluster-wide scope pages with ns "" exactly as before, and a single
// namespace pages with that name. Only a multi-namespace scope fans out.
func listScoped[T any, PT interface {
	*T
	metav1.Object
}](s Scope, opts metav1.ListOptions, page func(ns string, o metav1.ListOptions) ([]T, metav1.ListMeta, error)) ([]T, error) {
	names := s.Names()
	if len(names) <= 1 {
		ns := ""
		if len(names) == 1 {
			ns = names[0]
		}
		return listAll(opts, inNamespace(page, ns))
	}
	if len(names) > MaxNamespaceFanout {
		return listWideFiltered[T, PT](s, opts, page)
	}
	per := make([][]T, len(names))
	fns := make([]func() error, len(names))
	for i, ns := range names {
		fns[i] = func() (err error) {
			per[i], err = listAll(opts, inNamespace(page, ns))
			return err
		}
	}
	if err := Concurrent(fns...); err != nil {
		return nil, err
	}
	return slices.Concat(per...), nil
}

// inNamespace binds a namespaced page func to one namespace.
func inNamespace[T any](page func(ns string, o metav1.ListOptions) ([]T, metav1.ListMeta, error), ns string) func(metav1.ListOptions) ([]T, metav1.ListMeta, error) {
	return func(o metav1.ListOptions) ([]T, metav1.ListMeta, error) { return page(ns, o) }
}

// Concurrent runs independent calls in parallel and returns the first non-nil
// error in argument order. Each fn writes its own result into a variable the
// caller captured.
func Concurrent(fns ...func() error) error {
	errs := make([]error, len(fns))
	var wg sync.WaitGroup
	wg.Add(len(fns))
	for i, fn := range fns {
		go func() { defer wg.Done(); errs[i] = fn() }()
	}
	wg.Wait()
	return cmp.Or(errs...)
}

// listWideFiltered serves a scope too wide to fan out: one cluster-wide List,
// then drop what the pattern did not match. It transfers more bytes than the
// targeted Lists would, and still wins past MaxNamespaceFanout because it is one
// round trip instead of dozens.
func listWideFiltered[T any, PT interface {
	*T
	metav1.Object
}](s Scope, opts metav1.ListOptions, page func(ns string, o metav1.ListOptions) ([]T, metav1.ListMeta, error)) ([]T, error) {
	all, err := listAll(opts, inNamespace(page, ""))
	if err != nil {
		return nil, err
	}
	names := s.Names()
	want := make(map[string]struct{}, len(names))
	for _, ns := range names {
		want[ns] = struct{}{}
	}
	// Compacting in place, and only moving an element once something ahead of it
	// was dropped: these objects are fat (a corev1.Pod is ~1.2 kB), so an
	// unconditional append would copy the whole list to keep most of it.
	n := 0
	for i := range all {
		if _, ok := want[PT(&all[i]).GetNamespace()]; !ok {
			continue
		}
		if n != i {
			all[n] = all[i]
		}
		n++
	}
	return all[:n], nil
}

// pageMeta is the paging metadata every List response exposes: the typed lists
// through their embedded ListMeta, an UnstructuredList through accessors over
// its object map.
type pageMeta interface {
	GetContinue() string
	GetRemainingItemCount() *int64
}

// lister is one resource's typed or dynamic client, e.g. c.CoreV1().Pods(ns).
type lister[L any] interface {
	List(ctx context.Context, opts metav1.ListOptions) (L, error)
}

// fetch issues one List page and splits the response into items and the
// metadata listAll follows.
func fetch[T any, L pageMeta, I lister[L]](ctx context.Context, l I, items func(L) []T, o metav1.ListOptions) ([]T, metav1.ListMeta, error) {
	list, err := l.List(ctx, o)
	if err != nil {
		return nil, metav1.ListMeta{}, err
	}
	return items(list), metav1.ListMeta{Continue: list.GetContinue(), RemainingItemCount: list.GetRemainingItemCount()}, nil
}

// scoped lists a namespaced resource across s; at binds its client to one
// namespace (c.CoreV1().Pods, d.Resource(gvr).Namespace).
func scoped[T any, PT interface {
	*T
	metav1.Object
}, L pageMeta, I lister[L]](ctx context.Context, s Scope, opts metav1.ListOptions, at func(string) I, items func(L) []T) ([]T, error) {
	return listScoped[T, PT](s, opts, func(ns string, o metav1.ListOptions) ([]T, metav1.ListMeta, error) {
		return fetch(ctx, at(ns), items, o)
	})
}

// cluster lists a cluster-scoped resource.
func cluster[T any, L pageMeta, I lister[L]](ctx context.Context, l I, opts metav1.ListOptions, items func(L) []T) ([]T, error) {
	return listAll(opts, func(o metav1.ListOptions) ([]T, metav1.ListMeta, error) {
		return fetch(ctx, l, items, o)
	})
}

// ListPods returns every pod in scope matching opts.
func ListPods(ctx context.Context, c kubernetes.Interface, s Scope, opts metav1.ListOptions) ([]corev1.Pod, error) {
	return scoped(ctx, s, opts, c.CoreV1().Pods, func(l *corev1.PodList) []corev1.Pod { return l.Items })
}

// ListNodes returns every node matching opts.
func ListNodes(ctx context.Context, c kubernetes.Interface, opts metav1.ListOptions) ([]corev1.Node, error) {
	return cluster(ctx, c.CoreV1().Nodes(), opts, func(l *corev1.NodeList) []corev1.Node { return l.Items })
}

// ListSecrets returns every secret in scope matching opts.
func ListSecrets(ctx context.Context, c kubernetes.Interface, s Scope, opts metav1.ListOptions) ([]corev1.Secret, error) {
	return scoped(ctx, s, opts, c.CoreV1().Secrets, func(l *corev1.SecretList) []corev1.Secret { return l.Items })
}

// ListServices returns every service in scope matching opts.
func ListServices(ctx context.Context, c kubernetes.Interface, s Scope, opts metav1.ListOptions) ([]corev1.Service, error) {
	return scoped(ctx, s, opts, c.CoreV1().Services, func(l *corev1.ServiceList) []corev1.Service { return l.Items })
}

// ListPodDisruptionBudgets returns every PDB in scope matching opts.
func ListPodDisruptionBudgets(ctx context.Context, c kubernetes.Interface, s Scope, opts metav1.ListOptions) ([]policyv1.PodDisruptionBudget, error) {
	return scoped(ctx, s, opts, c.PolicyV1().PodDisruptionBudgets, func(l *policyv1.PodDisruptionBudgetList) []policyv1.PodDisruptionBudget { return l.Items })
}

// ListHorizontalPodAutoscalers returns every HPA in scope matching opts.
func ListHorizontalPodAutoscalers(ctx context.Context, c kubernetes.Interface, s Scope, opts metav1.ListOptions) ([]autoscalingv2.HorizontalPodAutoscaler, error) {
	return scoped(ctx, s, opts, c.AutoscalingV2().HorizontalPodAutoscalers, func(l *autoscalingv2.HorizontalPodAutoscalerList) []autoscalingv2.HorizontalPodAutoscaler {
		return l.Items
	})
}

// ListEndpointSlices returns every EndpointSlice in scope matching opts.
// EndpointSlices, not the legacy Endpoints object: they carry the per-endpoint
// ready/terminating conditions this needs, and Endpoints is deprecated.
func ListEndpointSlices(ctx context.Context, c kubernetes.Interface, s Scope, opts metav1.ListOptions) ([]discoveryv1.EndpointSlice, error) {
	return scoped(ctx, s, opts, c.DiscoveryV1().EndpointSlices, func(l *discoveryv1.EndpointSliceList) []discoveryv1.EndpointSlice { return l.Items })
}

// ListDeployments returns every Deployment in scope matching opts.
func ListDeployments(ctx context.Context, c kubernetes.Interface, s Scope, opts metav1.ListOptions) ([]appsv1.Deployment, error) {
	return scoped(ctx, s, opts, c.AppsV1().Deployments, func(l *appsv1.DeploymentList) []appsv1.Deployment { return l.Items })
}

// ListStatefulSets returns every StatefulSet in scope matching opts.
func ListStatefulSets(ctx context.Context, c kubernetes.Interface, s Scope, opts metav1.ListOptions) ([]appsv1.StatefulSet, error) {
	return scoped(ctx, s, opts, c.AppsV1().StatefulSets, func(l *appsv1.StatefulSetList) []appsv1.StatefulSet { return l.Items })
}

// ListDaemonSets returns every DaemonSet in scope matching opts.
func ListDaemonSets(ctx context.Context, c kubernetes.Interface, s Scope, opts metav1.ListOptions) ([]appsv1.DaemonSet, error) {
	return scoped(ctx, s, opts, c.AppsV1().DaemonSets, func(l *appsv1.DaemonSetList) []appsv1.DaemonSet { return l.Items })
}

// ListCustom returns every object of a custom resource in scope through the
// dynamic client, paginated like the typed lists.
//
// A nil client yields no objects and no error: that is how a view reading a CRD
// stays runnable when the bundle carries no dynamic client (a test, or a caller
// that never built one). Whether an absent CRD is an error is the caller's call
// - see view.Rollouts.
func ListCustom(ctx context.Context, d dynamic.Interface, gvr schema.GroupVersionResource, s Scope, opts metav1.ListOptions) ([]unstructured.Unstructured, error) {
	if d == nil {
		return nil, nil
	}
	return scoped(ctx, s, opts, d.Resource(gvr).Namespace, func(l *unstructured.UnstructuredList) []unstructured.Unstructured { return l.Items })
}

// ListIngresses returns every Ingress in scope matching opts.
func ListIngresses(ctx context.Context, c kubernetes.Interface, s Scope, opts metav1.ListOptions) ([]networkingv1.Ingress, error) {
	return scoped(ctx, s, opts, c.NetworkingV1().Ingresses, func(l *networkingv1.IngressList) []networkingv1.Ingress { return l.Items })
}

// ListNamespaces returns every namespace matching opts.
func ListNamespaces(ctx context.Context, c kubernetes.Interface, opts metav1.ListOptions) ([]corev1.Namespace, error) {
	return cluster(ctx, c.CoreV1().Namespaces(), opts, func(l *corev1.NamespaceList) []corev1.Namespace { return l.Items })
}

// ListPersistentVolumeClaims returns every PVC in scope matching opts.
func ListPersistentVolumeClaims(ctx context.Context, c kubernetes.Interface, s Scope, opts metav1.ListOptions) ([]corev1.PersistentVolumeClaim, error) {
	return scoped(ctx, s, opts, c.CoreV1().PersistentVolumeClaims, func(l *corev1.PersistentVolumeClaimList) []corev1.PersistentVolumeClaim { return l.Items })
}

// ListConfigMaps returns every ConfigMap in scope matching opts.
func ListConfigMaps(ctx context.Context, c kubernetes.Interface, s Scope, opts metav1.ListOptions) ([]corev1.ConfigMap, error) {
	return scoped(ctx, s, opts, c.CoreV1().ConfigMaps, func(l *corev1.ConfigMapList) []corev1.ConfigMap { return l.Items })
}

// ListServiceAccounts returns every ServiceAccount in scope matching opts.
func ListServiceAccounts(ctx context.Context, c kubernetes.Interface, s Scope, opts metav1.ListOptions) ([]corev1.ServiceAccount, error) {
	return scoped(ctx, s, opts, c.CoreV1().ServiceAccounts, func(l *corev1.ServiceAccountList) []corev1.ServiceAccount { return l.Items })
}

// ListJobs returns every Job in scope matching opts.
func ListJobs(ctx context.Context, c kubernetes.Interface, s Scope, opts metav1.ListOptions) ([]batchv1.Job, error) {
	return scoped(ctx, s, opts, c.BatchV1().Jobs, func(l *batchv1.JobList) []batchv1.Job { return l.Items })
}

// ListCronJobs returns every CronJob in scope matching opts.
func ListCronJobs(ctx context.Context, c kubernetes.Interface, s Scope, opts metav1.ListOptions) ([]batchv1.CronJob, error) {
	return scoped(ctx, s, opts, c.BatchV1().CronJobs, func(l *batchv1.CronJobList) []batchv1.CronJob { return l.Items })
}

// ListPersistentVolumes returns every PersistentVolume matching opts.
// Cluster-scoped: callers holding only namespace rights must tolerate the error.
func ListPersistentVolumes(ctx context.Context, c kubernetes.Interface, opts metav1.ListOptions) ([]corev1.PersistentVolume, error) {
	return cluster(ctx, c.CoreV1().PersistentVolumes(), opts, func(l *corev1.PersistentVolumeList) []corev1.PersistentVolume { return l.Items })
}

// ListStorageClasses returns every StorageClass matching opts. Cluster-scoped:
// callers holding only namespace rights must tolerate the error.
func ListStorageClasses(ctx context.Context, c kubernetes.Interface, opts metav1.ListOptions) ([]storagev1.StorageClass, error) {
	return cluster(ctx, c.StorageV1().StorageClasses(), opts, func(l *storagev1.StorageClassList) []storagev1.StorageClass { return l.Items })
}

// ListNetworkPolicies returns every NetworkPolicy in scope matching opts.
func ListNetworkPolicies(ctx context.Context, c kubernetes.Interface, s Scope, opts metav1.ListOptions) ([]networkingv1.NetworkPolicy, error) {
	return scoped(ctx, s, opts, c.NetworkingV1().NetworkPolicies, func(l *networkingv1.NetworkPolicyList) []networkingv1.NetworkPolicy { return l.Items })
}
