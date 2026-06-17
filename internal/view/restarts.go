package view

import (
	"context"
	"io"
	"sort"
	"strconv"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/PixiBixi/kubectl-klens/internal/kube"
)

// Restarts lists containers that have restarted, most restarts first, with the
// reason behind the current or last termination (e.g. CrashLoopBackOff,
// OOMKilled). Containers with zero restarts are omitted.
func Restarts(ctx context.Context, c kubernetes.Interface, f kube.Flags, args []string, out io.Writer) error {
	pods, err := c.CoreV1().Pods(f.NamespaceScope()).List(ctx, metav1.ListOptions{})
	if err != nil {
		return err
	}
	type entry struct {
		ns, pod, container, state string
		restarts                  int32
	}
	var list []entry
	for _, p := range pods.Items {
		for _, cs := range p.Status.ContainerStatuses {
			if cs.RestartCount == 0 {
				continue
			}
			list = append(list, entry{p.Namespace, p.Name, cs.Name, containerState(cs), cs.RestartCount})
		}
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].restarts != list[j].restarts {
			return list[i].restarts > list[j].restarts
		}
		if list[i].ns != list[j].ns {
			return list[i].ns < list[j].ns
		}
		return list[i].pod < list[j].pod
	})
	t := kube.NewTable(out, "NS", "POD", "CONTAINER", "RESTARTS", "STATE")
	for _, e := range list {
		t.Row(e.ns, e.pod, e.container, strconv.Itoa(int(e.restarts)), e.state)
	}
	t.SortBy(f.Sort)
	return t.Flush()
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
