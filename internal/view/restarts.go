package view

import (
	"cmp"
	"context"
	"io"
	"slices"
	"strconv"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/PixiBixi/kubectl-klens/internal/kube"
)

// Restarts lists containers that have restarted, most restarts first, with the
// reason behind the current or last termination (e.g. CrashLoopBackOff,
// OOMKilled) and its exit code (137/143 = SIGKILL/SIGTERM). Containers with zero
// restarts are omitted.
func Restarts(ctx context.Context, c kubernetes.Interface, f kube.Flags, args []string, out io.Writer) error {
	pods, err := c.CoreV1().Pods(f.NamespaceScope()).List(ctx, metav1.ListOptions{})
	if err != nil {
		return err
	}
	type entry struct {
		ns, pod, container, state string
		restarts                  int32
		exit                      int32
		hasExit                   bool
	}
	var list []entry
	for _, p := range pods.Items {
		for _, cs := range p.Status.ContainerStatuses {
			if cs.RestartCount == 0 {
				continue
			}
			exit, hasExit := lastExitCode(cs)
			list = append(list, entry{p.Namespace, p.Name, cs.Name, containerState(cs), cs.RestartCount, exit, hasExit})
		}
	}
	slices.SortFunc(list, func(a, b entry) int {
		return cmp.Or(
			cmp.Compare(b.restarts, a.restarts), // most restarts first
			cmp.Compare(a.ns, b.ns),
			cmp.Compare(a.pod, b.pod),
		)
	})
	paint := kube.NewPainter(f)
	t := kube.NewTable(out, paint, "NS", "POD", "CONTAINER", "RESTARTS", "STATE", "EXIT")
	t.SortRank("EXIT", exitRank)
	for _, e := range list {
		t.Row(e.ns, e.pod, e.container, paint.Warn(strconv.Itoa(int(e.restarts))), paint.Status(e.state), exitCell(paint, e.exit, e.hasExit))
	}
	t.SortBy(f.Sort)
	return t.Flush()
}

// lastExitCode reports the exit code of the container's most recent termination:
// the current terminated state if present, else the last termination. The bool
// is false when the container has no recorded termination (e.g. only ever
// Waiting/CrashLoopBackOff without a completed run).
func lastExitCode(cs corev1.ContainerStatus) (int32, bool) {
	switch {
	case cs.State.Terminated != nil:
		return cs.State.Terminated.ExitCode, true
	case cs.LastTerminationState.Terminated != nil:
		return cs.LastTerminationState.Terminated.ExitCode, true
	default:
		return 0, false
	}
}

// exitCell renders the EXIT column: green for a clean exit (0), red for any
// non-zero code (137/143 = SIGKILL/SIGTERM from OOM or eviction, else an app
// error), muted "-" when no termination is recorded.
func exitCell(paint kube.Painter, code int32, ok bool) string {
	if !ok {
		return paint.Muted("-")
	}
	s := strconv.Itoa(int(code))
	if code == 0 {
		return paint.OK(s)
	}
	return paint.Bad(s)
}

// exitRank orders the EXIT column numerically when sorted, keeping the muted "-"
// placeholder ahead of real codes instead of falling back to text ordering.
func exitRank(cell string) int {
	if cell == "-" {
		return -1
	}
	n, _ := strconv.Atoi(cell)
	return n
}

// containerState reports why a container is or was last down: the current
// waiting reason, else the current/last termination reason, else its run state.
func containerState(cs corev1.ContainerStatus) string {
	switch {
	case cs.State.Waiting != nil && cs.State.Waiting.Reason != "":
		return cs.State.Waiting.Reason
	case cs.State.Terminated != nil && cs.State.Terminated.Reason != "":
		return cs.State.Terminated.Reason
	case cs.LastTerminationState.Terminated != nil && cs.LastTerminationState.Terminated.Reason != "":
		return cs.LastTerminationState.Terminated.Reason
	case cs.State.Running != nil:
		return "Running"
	default:
		return "Unknown"
	}
}
