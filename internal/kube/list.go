package kube

import (
	"context"

	autoscalingv2 "k8s.io/api/autoscaling/v2"
	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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
// with no copy — that is the common case (most namespaces hold far fewer than
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

// ListPods returns every pod in ns (empty means all namespaces) matching opts.
func ListPods(ctx context.Context, c kubernetes.Interface, ns string, opts metav1.ListOptions) ([]corev1.Pod, error) {
	return listAll(opts, func(o metav1.ListOptions) ([]corev1.Pod, metav1.ListMeta, error) {
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

// ListSecrets returns every secret in ns matching opts.
func ListSecrets(ctx context.Context, c kubernetes.Interface, ns string, opts metav1.ListOptions) ([]corev1.Secret, error) {
	return listAll(opts, func(o metav1.ListOptions) ([]corev1.Secret, metav1.ListMeta, error) {
		l, err := c.CoreV1().Secrets(ns).List(ctx, o)
		if err != nil {
			return nil, metav1.ListMeta{}, err
		}
		return l.Items, l.ListMeta, nil
	})
}

// ListServices returns every service in ns matching opts.
func ListServices(ctx context.Context, c kubernetes.Interface, ns string, opts metav1.ListOptions) ([]corev1.Service, error) {
	return listAll(opts, func(o metav1.ListOptions) ([]corev1.Service, metav1.ListMeta, error) {
		l, err := c.CoreV1().Services(ns).List(ctx, o)
		if err != nil {
			return nil, metav1.ListMeta{}, err
		}
		return l.Items, l.ListMeta, nil
	})
}

// ListPodDisruptionBudgets returns every PDB in ns matching opts.
func ListPodDisruptionBudgets(ctx context.Context, c kubernetes.Interface, ns string, opts metav1.ListOptions) ([]policyv1.PodDisruptionBudget, error) {
	return listAll(opts, func(o metav1.ListOptions) ([]policyv1.PodDisruptionBudget, metav1.ListMeta, error) {
		l, err := c.PolicyV1().PodDisruptionBudgets(ns).List(ctx, o)
		if err != nil {
			return nil, metav1.ListMeta{}, err
		}
		return l.Items, l.ListMeta, nil
	})
}

// ListHorizontalPodAutoscalers returns every HPA in ns matching opts.
func ListHorizontalPodAutoscalers(ctx context.Context, c kubernetes.Interface, ns string, opts metav1.ListOptions) ([]autoscalingv2.HorizontalPodAutoscaler, error) {
	return listAll(opts, func(o metav1.ListOptions) ([]autoscalingv2.HorizontalPodAutoscaler, metav1.ListMeta, error) {
		l, err := c.AutoscalingV2().HorizontalPodAutoscalers(ns).List(ctx, o)
		if err != nil {
			return nil, metav1.ListMeta{}, err
		}
		return l.Items, l.ListMeta, nil
	})
}
