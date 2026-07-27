package view

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

// newClientsetWithFieldSelectors returns a fake clientset that honours pod field
// selectors, which its default tracker ignores outright.
//
// This exists so pushing a filter down to the apiserver stays testable on both
// counts: that the view asks for the right selector (assertPodFieldSelector) and
// that it renders the right rows given a server which actually applies it.
// Without it, a view that pushes filtering down could only be tested for its
// intent, and dropping the redundant client-side filter would lose coverage.
func newClientsetWithFieldSelectors(objs ...runtime.Object) *fake.Clientset {
	c := fake.NewClientset(objs...)
	c.PrependReactor("list", "pods", func(action k8stesting.Action) (bool, runtime.Object, error) {
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
		all, err := c.Tracker().List(
			action.GetResource(),
			corev1.SchemeGroupVersion.WithKind("Pod"),
			action.GetNamespace(),
		)
		if err != nil {
			return true, nil, err
		}
		podList, ok := all.(*corev1.PodList)
		if !ok {
			return true, nil, nil
		}
		kept := make([]corev1.Pod, 0, len(podList.Items))
		for i := range podList.Items {
			p := &podList.Items[i]
			if selector.Matches(podFields(p)) {
				kept = append(kept, *p)
			}
		}
		podList.Items = kept
		return true, podList, nil
	})
	return c
}

// podFields mirrors the pod field selectors the apiserver supports, so the fake
// rejects a selector on an unsupported field the same way a real cluster would
// reject it — silently matching nothing rather than everything.
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

// assertPodFieldSelector fails unless exactly one pod list was issued and it
// carried want as its field selector. This is the "intent" half of the pushdown
// contract: it proves the filter reached the apiserver instead of being applied
// client-side after transferring the whole collection.
func assertPodFieldSelector(t *testing.T, c *fake.Clientset, want string) {
	t.Helper()
	var got []string
	for _, a := range c.Actions() {
		la, ok := a.(k8stesting.ListAction)
		if !ok || a.GetResource().Resource != "pods" {
			continue
		}
		if sel := la.GetListRestrictions().Fields; sel != nil {
			got = append(got, sel.String())
		} else {
			got = append(got, "")
		}
	}
	if len(got) != 1 {
		t.Fatalf("expected exactly one pod list, got %d: %q", len(got), got)
	}
	if got[0] != want {
		t.Fatalf("field selector = %q, want %q", got[0], want)
	}
}
