package kube

import (
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

// ChunkSize is how many objects each List request asks for. A List with no Limit
// makes the apiserver build the entire collection in one response, which on a
// cluster with tens of thousands of pods spikes memory on both ends; kubectl
// chunks at 500 for the same reason.
const ChunkSize = 500

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
	items, meta, err := page(opts)
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
		next, meta, err = page(opts)
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
		return listAll(opts, func(o metav1.ListOptions) ([]T, metav1.ListMeta, error) { return page(ns, o) })
	}
	if len(names) > MaxNamespaceFanout {
		return listWideFiltered[T, PT](s, opts, page)
	}
	per := make([][]T, len(names))
	errs := make([]error, len(names))
	var wg sync.WaitGroup
	wg.Add(len(names))
	for i, ns := range names {
		go func() {
			defer wg.Done()
			per[i], errs[i] = listAll(opts, func(o metav1.ListOptions) ([]T, metav1.ListMeta, error) { return page(ns, o) })
		}()
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			return nil, err
		}
	}
	return slices.Concat(per...), nil
}

// listWideFiltered serves a scope too wide to fan out: one cluster-wide List,
// then drop what the pattern did not match. It transfers more bytes than the
// targeted Lists would, and still wins past MaxNamespaceFanout because it is one
// round trip instead of dozens.
func listWideFiltered[T any, PT interface {
	*T
	metav1.Object
}](s Scope, opts metav1.ListOptions, page func(ns string, o metav1.ListOptions) ([]T, metav1.ListMeta, error)) ([]T, error) {
	all, err := listAll(opts, func(o metav1.ListOptions) ([]T, metav1.ListMeta, error) { return page("", o) })
	if err != nil {
		return nil, err
	}
	want := make(map[string]struct{}, s.Len())
	for _, ns := range s.Names() {
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

// ListPods returns every pod in scope matching opts.
func ListPods(ctx context.Context, c kubernetes.Interface, s Scope, opts metav1.ListOptions) ([]corev1.Pod, error) {
	return listScoped(s, opts, func(ns string, o metav1.ListOptions) ([]corev1.Pod, metav1.ListMeta, error) {
		l, err := c.CoreV1().Pods(ns).List(ctx, o)
		if err != nil {
			return nil, metav1.ListMeta{}, err
		}
		return l.Items, l.ListMeta, nil
	})
}

// ListNodes returns every node matching opts.
func ListNodes(ctx context.Context, c kubernetes.Interface, opts metav1.ListOptions) ([]corev1.Node, error) {
	return listAll(opts, func(o metav1.ListOptions) ([]corev1.Node, metav1.ListMeta, error) {
		l, err := c.CoreV1().Nodes().List(ctx, o)
		if err != nil {
			return nil, metav1.ListMeta{}, err
		}
		return l.Items, l.ListMeta, nil
	})
}

// ListSecrets returns every secret in scope matching opts.
func ListSecrets(ctx context.Context, c kubernetes.Interface, s Scope, opts metav1.ListOptions) ([]corev1.Secret, error) {
	return listScoped(s, opts, func(ns string, o metav1.ListOptions) ([]corev1.Secret, metav1.ListMeta, error) {
		l, err := c.CoreV1().Secrets(ns).List(ctx, o)
		if err != nil {
			return nil, metav1.ListMeta{}, err
		}
		return l.Items, l.ListMeta, nil
	})
}

// ListServices returns every service in scope matching opts.
func ListServices(ctx context.Context, c kubernetes.Interface, s Scope, opts metav1.ListOptions) ([]corev1.Service, error) {
	return listScoped(s, opts, func(ns string, o metav1.ListOptions) ([]corev1.Service, metav1.ListMeta, error) {
		l, err := c.CoreV1().Services(ns).List(ctx, o)
		if err != nil {
			return nil, metav1.ListMeta{}, err
		}
		return l.Items, l.ListMeta, nil
	})
}

// ListPodDisruptionBudgets returns every PDB in scope matching opts.
func ListPodDisruptionBudgets(ctx context.Context, c kubernetes.Interface, s Scope, opts metav1.ListOptions) ([]policyv1.PodDisruptionBudget, error) {
	return listScoped(s, opts, func(ns string, o metav1.ListOptions) ([]policyv1.PodDisruptionBudget, metav1.ListMeta, error) {
		l, err := c.PolicyV1().PodDisruptionBudgets(ns).List(ctx, o)
		if err != nil {
			return nil, metav1.ListMeta{}, err
		}
		return l.Items, l.ListMeta, nil
	})
}

// ListHorizontalPodAutoscalers returns every HPA in scope matching opts.
func ListHorizontalPodAutoscalers(ctx context.Context, c kubernetes.Interface, s Scope, opts metav1.ListOptions) ([]autoscalingv2.HorizontalPodAutoscaler, error) {
	return listScoped(s, opts, func(ns string, o metav1.ListOptions) ([]autoscalingv2.HorizontalPodAutoscaler, metav1.ListMeta, error) {
		l, err := c.AutoscalingV2().HorizontalPodAutoscalers(ns).List(ctx, o)
		if err != nil {
			return nil, metav1.ListMeta{}, err
		}
		return l.Items, l.ListMeta, nil
	})
}

// ListEndpointSlices returns every EndpointSlice in scope matching opts.
// EndpointSlices, not the legacy Endpoints object: they carry the per-endpoint
// ready/terminating conditions this needs, and Endpoints is deprecated.
func ListEndpointSlices(ctx context.Context, c kubernetes.Interface, s Scope, opts metav1.ListOptions) ([]discoveryv1.EndpointSlice, error) {
	return listScoped(s, opts, func(ns string, o metav1.ListOptions) ([]discoveryv1.EndpointSlice, metav1.ListMeta, error) {
		l, err := c.DiscoveryV1().EndpointSlices(ns).List(ctx, o)
		if err != nil {
			return nil, metav1.ListMeta{}, err
		}
		return l.Items, l.ListMeta, nil
	})
}

// ListDeployments returns every Deployment in scope matching opts.
func ListDeployments(ctx context.Context, c kubernetes.Interface, s Scope, opts metav1.ListOptions) ([]appsv1.Deployment, error) {
	return listScoped(s, opts, func(ns string, o metav1.ListOptions) ([]appsv1.Deployment, metav1.ListMeta, error) {
		l, err := c.AppsV1().Deployments(ns).List(ctx, o)
		if err != nil {
			return nil, metav1.ListMeta{}, err
		}
		return l.Items, l.ListMeta, nil
	})
}

// ListStatefulSets returns every StatefulSet in scope matching opts.
func ListStatefulSets(ctx context.Context, c kubernetes.Interface, s Scope, opts metav1.ListOptions) ([]appsv1.StatefulSet, error) {
	return listScoped(s, opts, func(ns string, o metav1.ListOptions) ([]appsv1.StatefulSet, metav1.ListMeta, error) {
		l, err := c.AppsV1().StatefulSets(ns).List(ctx, o)
		if err != nil {
			return nil, metav1.ListMeta{}, err
		}
		return l.Items, l.ListMeta, nil
	})
}

// ListDaemonSets returns every DaemonSet in scope matching opts.
func ListDaemonSets(ctx context.Context, c kubernetes.Interface, s Scope, opts metav1.ListOptions) ([]appsv1.DaemonSet, error) {
	return listScoped(s, opts, func(ns string, o metav1.ListOptions) ([]appsv1.DaemonSet, metav1.ListMeta, error) {
		l, err := c.AppsV1().DaemonSets(ns).List(ctx, o)
		if err != nil {
			return nil, metav1.ListMeta{}, err
		}
		return l.Items, l.ListMeta, nil
	})
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
	return listScoped(s, opts, func(ns string, o metav1.ListOptions) ([]unstructured.Unstructured, metav1.ListMeta, error) {
		l, err := d.Resource(gvr).Namespace(ns).List(ctx, o)
		if err != nil {
			return nil, metav1.ListMeta{}, err
		}
		// An UnstructuredList keeps its list metadata in the object map, not in
		// an embedded ListMeta; the two fields paging needs are accessors.
		return l.Items, metav1.ListMeta{Continue: l.GetContinue(), RemainingItemCount: l.GetRemainingItemCount()}, nil
	})
}

// ListIngresses returns every Ingress in scope matching opts.
func ListIngresses(ctx context.Context, c kubernetes.Interface, s Scope, opts metav1.ListOptions) ([]networkingv1.Ingress, error) {
	return listScoped(s, opts, func(ns string, o metav1.ListOptions) ([]networkingv1.Ingress, metav1.ListMeta, error) {
		l, err := c.NetworkingV1().Ingresses(ns).List(ctx, o)
		if err != nil {
			return nil, metav1.ListMeta{}, err
		}
		return l.Items, l.ListMeta, nil
	})
}

// ListNamespaces returns every namespace matching opts.
func ListNamespaces(ctx context.Context, c kubernetes.Interface, opts metav1.ListOptions) ([]corev1.Namespace, error) {
	return listAll(opts, func(o metav1.ListOptions) ([]corev1.Namespace, metav1.ListMeta, error) {
		l, err := c.CoreV1().Namespaces().List(ctx, o)
		if err != nil {
			return nil, metav1.ListMeta{}, err
		}
		return l.Items, l.ListMeta, nil
	})
}

// ListPersistentVolumeClaims returns every PVC in scope matching opts.
func ListPersistentVolumeClaims(ctx context.Context, c kubernetes.Interface, s Scope, opts metav1.ListOptions) ([]corev1.PersistentVolumeClaim, error) {
	return listScoped(s, opts, func(ns string, o metav1.ListOptions) ([]corev1.PersistentVolumeClaim, metav1.ListMeta, error) {
		l, err := c.CoreV1().PersistentVolumeClaims(ns).List(ctx, o)
		if err != nil {
			return nil, metav1.ListMeta{}, err
		}
		return l.Items, l.ListMeta, nil
	})
}

// ListConfigMaps returns every ConfigMap in scope matching opts.
func ListConfigMaps(ctx context.Context, c kubernetes.Interface, s Scope, opts metav1.ListOptions) ([]corev1.ConfigMap, error) {
	return listScoped(s, opts, func(ns string, o metav1.ListOptions) ([]corev1.ConfigMap, metav1.ListMeta, error) {
		l, err := c.CoreV1().ConfigMaps(ns).List(ctx, o)
		if err != nil {
			return nil, metav1.ListMeta{}, err
		}
		return l.Items, l.ListMeta, nil
	})
}

// ListServiceAccounts returns every ServiceAccount in scope matching opts.
func ListServiceAccounts(ctx context.Context, c kubernetes.Interface, s Scope, opts metav1.ListOptions) ([]corev1.ServiceAccount, error) {
	return listScoped(s, opts, func(ns string, o metav1.ListOptions) ([]corev1.ServiceAccount, metav1.ListMeta, error) {
		l, err := c.CoreV1().ServiceAccounts(ns).List(ctx, o)
		if err != nil {
			return nil, metav1.ListMeta{}, err
		}
		return l.Items, l.ListMeta, nil
	})
}

// ListJobs returns every Job in scope matching opts.
func ListJobs(ctx context.Context, c kubernetes.Interface, s Scope, opts metav1.ListOptions) ([]batchv1.Job, error) {
	return listScoped(s, opts, func(ns string, o metav1.ListOptions) ([]batchv1.Job, metav1.ListMeta, error) {
		l, err := c.BatchV1().Jobs(ns).List(ctx, o)
		if err != nil {
			return nil, metav1.ListMeta{}, err
		}
		return l.Items, l.ListMeta, nil
	})
}

// ListCronJobs returns every CronJob in scope matching opts.
func ListCronJobs(ctx context.Context, c kubernetes.Interface, s Scope, opts metav1.ListOptions) ([]batchv1.CronJob, error) {
	return listScoped(s, opts, func(ns string, o metav1.ListOptions) ([]batchv1.CronJob, metav1.ListMeta, error) {
		l, err := c.BatchV1().CronJobs(ns).List(ctx, o)
		if err != nil {
			return nil, metav1.ListMeta{}, err
		}
		return l.Items, l.ListMeta, nil
	})
}

// ListStorageClasses returns every StorageClass matching opts. Cluster-scoped:
// callers holding only namespace rights must tolerate the error.
func ListStorageClasses(ctx context.Context, c kubernetes.Interface, opts metav1.ListOptions) ([]storagev1.StorageClass, error) {
	return listAll(opts, func(o metav1.ListOptions) ([]storagev1.StorageClass, metav1.ListMeta, error) {
		l, err := c.StorageV1().StorageClasses().List(ctx, o)
		if err != nil {
			return nil, metav1.ListMeta{}, err
		}
		return l.Items, l.ListMeta, nil
	})
}
