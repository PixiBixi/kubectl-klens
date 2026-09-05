package view

import (
	"cmp"
	"sync"

	corev1 "k8s.io/api/core/v1"

	"github.com/PixiBixi/kubectl-klens/internal/kube"
)

// nodeStatus returns Ready / NotReady / Unknown from a node's Ready condition.
// Ready=Unknown is reported as "Unknown", not "NotReady": it means the kubelet
// stopped reporting altogether (the state that triggers pod eviction once the
// unreachable toleration expires), which is a different failure from a node
// whose kubelet is alive and answering NotReady. A node with no Ready condition
// at all is also Unknown.
func nodeStatus(n *corev1.Node) string {
	for _, cond := range n.Status.Conditions {
		if cond.Type == corev1.NodeReady {
			switch cond.Status {
			case corev1.ConditionTrue:
				return "Ready"
			case corev1.ConditionFalse:
				return "NotReady"
			default:
				return "Unknown"
			}
		}
	}
	return "Unknown"
}

// Container roles reported in the KIND column. Init and ephemeral containers are
// inspected alongside app containers because they are a routine blind spot: an
// init container carries the same security context, images and resource
// requests as any other (a privileged one escalates just as far, and its
// requests count toward scheduling), and an init container stuck in
// CrashLoopBackOff is a common cause of a wedged pod.
const (
	kindApp  = "app"
	kindInit = "init"
	kindEph  = "eph"
)

// podContainer pairs a container spec with its role in the pod.
type podContainer struct {
	Spec *corev1.Container
	Kind string
}

// podContainers enumerates a pod's containers in startup order - init, then
// app, then ephemeral (debug) - tagged with their kind. The returned pointers
// alias p, so they stay valid only as long as p does.
// podContainers enumerates a pod's containers in startup order - init, then
// app, then ephemeral (debug) - tagged with their kind. The returned pointers
// alias p, so they stay valid only as long as p does.
//
// A synthetic pod standing in for a workload template (see podsForView) has no
// ephemeral containers - those are attached to a running pod, never declared in
// a workload's spec - so this reads the same for a real pod and a template
// without needing a second version.
func podContainers(p *corev1.Pod) []podContainer {
	out := make([]podContainer, 0, len(p.Spec.InitContainers)+len(p.Spec.Containers)+len(p.Spec.EphemeralContainers))
	for i := range p.Spec.InitContainers {
		out = append(out, podContainer{&p.Spec.InitContainers[i], kindInit})
	}
	for i := range p.Spec.Containers {
		out = append(out, podContainer{&p.Spec.Containers[i], kindApp})
	}
	for i := range p.Spec.EphemeralContainers {
		// EphemeralContainerCommon is field-for-field identical to Container by
		// API contract, precisely so it can be treated as one.
		out = append(out, podContainer{(*corev1.Container)(&p.Spec.EphemeralContainers[i].EphemeralContainerCommon), kindEph})
	}
	return out
}

// podContainerStatus pairs a container status with its role in the pod.
type podContainerStatus struct {
	Status *corev1.ContainerStatus
	Kind   string
}

// podContainerStatuses enumerates a pod's container statuses in startup order,
// tagged with their kind, so runtime state (restarts, crash reasons) is reported
// for init and ephemeral containers too. The returned pointers alias p.
func podContainerStatuses(p *corev1.Pod) []podContainerStatus {
	st := &p.Status
	out := make([]podContainerStatus, 0, len(st.InitContainerStatuses)+len(st.ContainerStatuses)+len(st.EphemeralContainerStatuses))
	for i := range st.InitContainerStatuses {
		out = append(out, podContainerStatus{&st.InitContainerStatuses[i], kindInit})
	}
	for i := range st.ContainerStatuses {
		out = append(out, podContainerStatus{&st.ContainerStatuses[i], kindApp})
	}
	for i := range st.EphemeralContainerStatuses {
		out = append(out, podContainerStatus{&st.EphemeralContainerStatuses[i], kindEph})
	}
	return out
}

// bothLists runs two independent list calls concurrently and returns the first
// error if either fails. The two are separate apiserver round trips with no
// dependency between them, so running them in sequence just adds the smaller
// one's latency to the total.
func bothLists[A, B any](listA func() (A, error), listB func() (B, error)) (A, B, error) {
	var (
		a    A
		b    B
		errA error
		errB error
		wg   sync.WaitGroup
	)
	wg.Add(2)
	go func() { defer wg.Done(); a, errA = listA() }()
	go func() { defer wg.Done(); b, errB = listB() }()
	wg.Wait()
	return a, b, cmp.Or(errA, errB)
}

// allLists runs independent list calls concurrently and returns the first error,
// for the views that need more than the two bothLists covers. Each fn writes its
// own result into a variable the caller captured.
func allLists(fns ...func() error) error {
	errs := make([]error, len(fns))
	var wg sync.WaitGroup
	wg.Add(len(fns))
	for i, fn := range fns {
		go func() { defer wg.Done(); errs[i] = fn() }()
	}
	wg.Wait()
	return cmp.Or(errs...)
}

// skipNamespace reports whether a pod's namespace should be dropped from a
// cluster-wide listing. kube-system is excluded from the -A view so operator
// noise doesn't bury workload rows - but only there: when the user scoped to a
// single namespace they asked for it explicitly, and silently returning nothing
// for `-n kube-system` would be a lie.
func skipNamespace(f kube.Flags, namespace string) bool {
	return f.ScopeIsAll() && namespace == "kube-system"
}

// qtyOrNone returns the string form of a resource quantity, or a muted "none"
// if unset.
func qtyOrNone(paint kube.Painter, rl corev1.ResourceList, name corev1.ResourceName) string {
	if q, ok := rl[name]; ok {
		return q.String()
	}
	return paint.Muted("none")
}

// ownerHeader is the column name that replaces a view's pod-identity column
// under --by-owner: the rows are then workloads, read straight from their
// controllers, not pods.
const ownerHeader = "WORKLOAD"

// podColumn returns the header for a view's pod-identity column: its own name,
// or WORKLOAD when --by-owner is reading workloads instead of pods.
func podColumn(f kube.Flags, header string) string {
	if f.ByOwner {
		return ownerHeader
	}
	return header
}

// podSort maps a --sort value naming the pod-identity column onto whichever
// header the table actually carries, so `--sort pod` keeps working under
// --by-owner and `--sort workload` without it. Both names are accepted in both
// modes: which one is live depends on a flag, and rejecting the other would
// make --sort fail for a reason that has nothing to do with the sort.
func podSort(f kube.Flags, sort, header string) string {
	switch sort {
	case "pod", "podname", "workload":
		return podColumn(f, header)
	}
	return sort
}

// ownerHeaders and appendOwnerCells insert a REPLICAS column right after the
// identity column when --by-owner is active - free at that point, since
// podsForView already listed the controller to get there - and contribute
// nothing when it is off, so the table renders byte-identical to before the
// flag existed.
//
// appendOwnerCells appends into the caller's reusable row buffer rather than
// returning a slice: it runs once per row, where a fresh slice (or the
// slices.Concat this replaced) is a heap allocation per line. ownerHeaders runs
// once per table, so it can allocate.
func ownerHeaders(f kube.Flags) []string {
	if !f.ByOwner {
		return nil
	}
	return []string{"REPLICAS"}
}

func appendOwnerCells(row []string, paint kube.Painter, f kube.Flags, p *corev1.Pod) []string {
	if !f.ByOwner {
		return row
	}
	return append(row, replicaCell(paint, replicasOf(p)))
}
