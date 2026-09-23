package view

import (
	"context"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"

	"github.com/PixiBixi/kubectl-klens/internal/kube"
)

// Pending lists pods stuck in the Pending phase and synthesizes why: the
// scheduler's dominant rejection cause, or the container waiting reason (image
// pull / config errors). Everything is read from the pod object, so no events
// or extra API calls are needed. Oldest (most-stuck) pods sort first.
//
// The phase filter is pushed down to the apiserver. It is the difference between
// transferring three pods and transferring every pod in the cluster, since the
// interesting answer here is almost always a handful of rows.
func Pending(ctx context.Context, c kube.Clients, f kube.Flags, args []string, out io.Writer) error {
	pods, err := kube.ListPods(ctx, c, f.Scope(), metav1.ListOptions{
		FieldSelector: fields.OneTermEqualSelector("status.phase", string(corev1.PodPending)).String(),
	})
	if err != nil {
		return err
	}
	paint := kube.NewPainter(f)

	// pod is a pointer into pods: the entry only needs the namespace, name and
	// creation timestamp, so carrying the whole 1.2 kB object by value copied it
	// twice over (once out of the range, once into the slice).
	type entry struct {
		pod            *corev1.Pod
		reason, detail string
	}
	list := make([]entry, 0, len(pods))
	for i := range pods {
		p := &pods[i]
		reason, detail := pendingReason(p)
		list = append(list, entry{p, reason, detail})
	}
	slices.SortStableFunc(list, func(a, b entry) int {
		return a.pod.CreationTimestamp.Compare(b.pod.CreationTimestamp.Time)
	})

	t := kube.NewTable(out, paint, "NS", "POD", "AGE", "REASON", "DETAIL")
	for i := range list {
		e := &list[i]
		t.Row(e.pod.Namespace, e.pod.Name, age(e.pod.CreationTimestamp), paint.Status(e.reason), orMutedDash(paint, e.detail))
	}
	t.SortBy(f.Sort)
	return t.Flush()
}

// pendingReason derives a Pending pod's blocking reason and a compact detail.
// Scheduling failure (PodScheduled=False) takes precedence over container
// waiting states (image pulls, config errors).
func pendingReason(p *corev1.Pod) (reason, detail string) {
	for _, cond := range p.Status.Conditions {
		if cond.Type == corev1.PodScheduled && cond.Status == corev1.ConditionFalse {
			r := cond.Reason
			if r == "" {
				r = "Unschedulable"
			}
			return r, schedulerCause(cond.Message)
		}
	}
	// Init first: while one is stuck, every app container reads PodInitializing,
	// which hides the init container's own reason.
	for _, cs := range podContainerStatuses(p) {
		if w := cs.Status.State.Waiting; w != nil && w.Reason != "" {
			switch w.Reason {
			case "ImagePullBackOff", "ErrImagePull", "InvalidImageName":
				return w.Reason, containerImage(p, cs.Status.Name)
			default:
				return w.Reason, ""
			}
		}
	}
	return "Pending", ""
}

// containerImage returns the configured image for the named container.
// Container names are unique across init, app and ephemeral containers.
func containerImage(p *corev1.Pod, name string) string {
	for _, pc := range podContainers(p) {
		if pc.Spec.Name == name {
			return pc.Spec.Image
		}
	}
	return ""
}

// schedulerCause condenses a verbose scheduler message into one clause, e.g.
// "Insufficient cpu (3 nodes)". It picks the clause with the largest node count
// and falls back to the trimmed raw message when the format is unrecognized, so
// the result is never empty.
func schedulerCause(msg string) string {
	const marker = "available: "
	_, after, ok := strings.Cut(msg, marker)
	if !ok {
		return trimSentence(msg)
	}
	tail, _, _ := strings.Cut(after, ". ") // drop the trailing "preemption: ..." sentence
	tail = strings.TrimRight(tail, ".")

	bestPhrase, bestCount := "", -1
	for clause := range strings.SplitSeq(tail, ", ") {
		count, phrase := splitLeadingCount(strings.TrimSpace(clause))
		phrase, _, _ = strings.Cut(phrase, " {") // strip the " {key: value}" blob
		phrase = strings.TrimSpace(phrase)
		if count > bestCount {
			bestCount, bestPhrase = count, phrase
		}
	}
	if bestPhrase == "" {
		return trimSentence(msg)
	}
	if bestCount >= 0 {
		return fmt.Sprintf("%s (%d nodes)", bestPhrase, bestCount)
	}
	return bestPhrase
}

// splitLeadingCount splits a leading integer off "3 Insufficient cpu" → (3,
// "Insufficient cpu"); returns (-1, s) when there is no leading count.
func splitLeadingCount(s string) (int, string) {
	if count, rest, ok := strings.Cut(s, " "); ok {
		if n, err := strconv.Atoi(count); err == nil {
			return n, rest
		}
	}
	return -1, s
}

// trimSentence keeps the first sentence of s, capped at 60 runes.
func trimSentence(s string) string {
	s = strings.TrimSpace(s)
	s, _, _ = strings.Cut(s, ". ")
	s = strings.TrimRight(s, ".")
	if utf8.RuneCountInString(s) > 60 {
		return string([]rune(s)[:60])
	}
	return s
}
