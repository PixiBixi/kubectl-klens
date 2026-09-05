package view

import (
	"cmp"
	"context"
	"io"
	"slices"
	"strconv"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/PixiBixi/kubectl-klens/internal/kube"
)

// Restarts lists containers that have restarted, most restarts first, with the
// reason behind the current or last termination (e.g. CrashLoopBackOff,
// OOMKilled) and its exit code (137/143 = SIGKILL/SIGTERM). Init and ephemeral
// containers are included - an init container looping in CrashLoopBackOff is a
// classic cause of a pod that never starts, and it used to be invisible here.
// Containers with zero restarts are omitted.
func Restarts(ctx context.Context, c kube.Clients, f kube.Flags, args []string, out io.Writer) error {
	pods, err := kube.ListPods(ctx, c, f.Scope(), metav1.ListOptions{})
	if err != nil {
		return err
	}
	type entry struct {
		ns, pod, container, kind, state string
		restarts                        int32
		exit                            int32
		hasExit                         bool
		last                            time.Time
		hasLast                         bool
	}
	var list []entry
	for i := range pods {
		p := &pods[i]
		for _, pcs := range podContainerStatuses(p) {
			cs := pcs.Status
			if cs.RestartCount == 0 {
				continue
			}
			exit, hasExit := lastExitCode(cs)
			last, hasLast := lastRestart(cs)
			list = append(list, entry{p.Namespace, p.Name, cs.Name, pcs.Kind, containerState(cs), cs.RestartCount, exit, hasExit, last, hasLast})
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
	t := kube.NewTable(out, paint, "NS", "POD", "CONTAINER", "KIND", "RESTARTS", "STATE", "EXIT", "LAST")
	t.SortRank("EXIT", exitRank)
	for i := range list {
		e := &list[i]
		t.Row(e.ns, e.pod, e.container, e.kind, paint.Warn(strconv.Itoa(int(e.restarts))), paint.Status(e.state), exitCell(paint, e.exit, e.hasExit), lastCell(paint, e.last, e.hasLast))
	}
	t.SortBy(f.Sort)
	return t.Flush()
}

// lastExitCode reports the exit code of the container's most recent termination:
// the current terminated state if present, else the last termination. The bool
// is false when the container has no recorded termination (e.g. only ever
// Waiting/CrashLoopBackOff without a completed run).
func lastExitCode(cs *corev1.ContainerStatus) (int32, bool) {
	switch {
	case cs.State.Terminated != nil:
		return cs.State.Terminated.ExitCode, true
	case cs.LastTerminationState.Terminated != nil:
		return cs.LastTerminationState.Terminated.ExitCode, true
	default:
		return 0, false
	}
}

// lastRestart reports when the container last restarted: the start of the
// current run for a container that came back up, else the end of its most
// recent termination (the crash itself, for one stuck in CrashLoopBackOff).
// The bool is false when the kubelet recorded no usable timestamp.
func lastRestart(cs *corev1.ContainerStatus) (time.Time, bool) {
	switch {
	case cs.State.Running != nil && !cs.State.Running.StartedAt.IsZero():
		return cs.State.Running.StartedAt.Time, true
	case cs.State.Terminated != nil && !cs.State.Terminated.FinishedAt.IsZero():
		return cs.State.Terminated.FinishedAt.Time, true
	case cs.LastTerminationState.Terminated != nil && !cs.LastTerminationState.Terminated.FinishedAt.IsZero():
		return cs.LastTerminationState.Terminated.FinishedAt.Time, true
	default:
		return time.Time{}, false
	}
}

// lastRestartLayout is fixed-width and month-first so the LAST column sorts
// lexically (the muted "-" placeholder sorts ahead of any digit).
const lastRestartLayout = "01-02 15:04:05"

// lastCell renders the LAST column in local time, colored by how fresh the
// restart is: red under 15m (still crashing), orange under 2h, green beyond -
// a container that last restarted days ago has settled.
func lastCell(paint kube.Painter, t time.Time, ok bool) string {
	if !ok {
		return paint.Muted("-")
	}
	s := t.Local().Format(lastRestartLayout)
	switch d := time.Since(t); {
	case d < 15*time.Minute:
		return paint.Bad(s)
	case d < 2*time.Hour:
		return paint.Warn(s)
	default:
		return paint.OK(s)
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
func containerState(cs *corev1.ContainerStatus) string {
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
