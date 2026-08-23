package view

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

// newClientsetWithFieldSelectors returns a fake clientset that honours pod and
// node field selectors, which its default tracker ignores outright.
//
// This exists so pushing a filter down to the apiserver stays testable on both
// counts: that the view asks for the right selector (assertFieldSelector) and
// that it renders the right rows given a server which actually applies it.
// Without it, a view that pushes filtering down could only be tested for its
// intent, and dropping the redundant client-side filter would lose coverage.
func newClientsetWithFieldSelectors(objs ...runtime.Object) *fake.Clientset {
	c := fake.NewClientset(objs...)
	addFieldSelectorReactor(c, "pods", corev1.SchemeGroupVersion.WithKind("Pod"), filterPods)
	addFieldSelectorReactor(c, "nodes", corev1.SchemeGroupVersion.WithKind("Node"), filterNodes)
	return c
}

// addFieldSelectorReactor makes the fake apply a field selector to one resource
// the way the apiserver would. filter narrows the tracker's full list; returning
// nil stands for "unexpected list type", answered as an empty result.
func addFieldSelectorReactor(c *fake.Clientset, resource string, kind schema.GroupVersionKind, filter func(runtime.Object, fields.Selector) runtime.Object) {
	c.PrependReactor("list", resource, func(action k8stesting.Action) (bool, runtime.Object, error) {
		list, ok := action.(k8stesting.ListAction)
		if !ok {
			return false, nil, nil
		}
		selector := list.GetListRestrictions().Fields
		if selector == nil || selector.Empty() {
			return false, nil, nil // no selector: let the default tracker answer
		}
		// Re-dispatch without the selector to get the full list, then filter it
		// the way the apiserver would.
		all, err := c.Tracker().List(action.GetResource(), kind, action.GetNamespace())
		if err != nil {
			return true, nil, err
		}
		return true, filter(all, selector), nil
	})
}

func filterPods(all runtime.Object, selector fields.Selector) runtime.Object {
	list, ok := all.(*corev1.PodList)
	if !ok {
		return nil
	}
	kept := make([]corev1.Pod, 0, len(list.Items))
	for i := range list.Items {
		p := &list.Items[i]
		if selector.Matches(podFields(p)) {
			kept = append(kept, *p)
		}
	}
	list.Items = kept
	return list
}

func filterNodes(all runtime.Object, selector fields.Selector) runtime.Object {
	list, ok := all.(*corev1.NodeList)
	if !ok {
		return nil
	}
	kept := make([]corev1.Node, 0, len(list.Items))
	for i := range list.Items {
		n := &list.Items[i]
		// metadata.name is the only field selector the apiserver indexes for
		// nodes, so it is the only one the fake honours.
		if selector.Matches(fields.Set{"metadata.name": n.Name}) {
			kept = append(kept, *n)
		}
	}
	list.Items = kept
	return list
}

// podFields mirrors the pod field selectors the apiserver supports, so the fake
// rejects a selector on an unsupported field the same way a real cluster would
// reject it - silently matching nothing rather than everything.
func podFields(p *corev1.Pod) fields.Set {
	return fields.Set{
		"metadata.name":            p.Name,
		"metadata.namespace":       p.Namespace,
		"spec.nodeName":            p.Spec.NodeName,
		"spec.restartPolicy":       string(p.Spec.RestartPolicy),
		"spec.schedulerName":       p.Spec.SchedulerName,
		"spec.serviceAccountName":  p.Spec.ServiceAccountName,
		"status.phase":             string(p.Status.Phase),
		"status.podIP":             p.Status.PodIP,
		"status.nominatedNodeName": p.Status.NominatedNodeName,
	}
}

// assertFieldSelector fails unless exactly one list of resource was issued and
// it carried want as its field selector. This is the "intent" half of the
// pushdown contract: it proves the filter reached the apiserver instead of being
// applied client-side after transferring the whole collection.
func assertFieldSelector(t *testing.T, c *fake.Clientset, resource, want string) {
	t.Helper()
	var got []string
	for _, a := range c.Actions() {
		la, ok := a.(k8stesting.ListAction)
		if !ok || a.GetResource().Resource != resource {
			continue
		}
		if sel := la.GetListRestrictions().Fields; sel != nil {
			got = append(got, sel.String())
		} else {
			got = append(got, "")
		}
	}
	if len(got) != 1 {
		t.Fatalf("expected exactly one %s list, got %d: %q", resource, len(got), got)
	}
	if got[0] != want {
		t.Fatalf("field selector = %q, want %q", got[0], want)
	}
}
