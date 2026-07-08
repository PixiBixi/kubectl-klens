package view

import (
	"context"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/duration"
	"k8s.io/client-go/kubernetes"

	"github.com/PixiBixi/kubectl-klens/internal/kube"
)

// Pending lists pods stuck in the Pending phase and synthesizes why: the
// scheduler's dominant rejection cause, or the container waiting reason (image
// pull / config errors). Everything is read from the pod object, so no events
// or extra API calls are needed. Oldest (most-stuck) pods sort first.
func Pending(ctx context.Context, c kubernetes.Interface, f kube.Flags, args []string, out io.Writer) error {
	pods, err := c.CoreV1().Pods(f.NamespaceScope()).List(ctx, metav1.ListOptions{})
	if err != nil {
		return err
	}
	paint := kube.NewPainter(f)

	type entry struct {
		pod            corev1.Pod
		reason, detail string
	}
	var list []entry
	for _, p := range pods.Items {
		if p.Status.Phase != corev1.PodPending {
			continue
		}
		reason, detail := pendingReason(p)
		list = append(list, entry{p, reason, detail})
	}
	slices.SortStableFunc(list, func(a, b entry) int {
		return a.pod.CreationTimestamp.Time.Compare(b.pod.CreationTimestamp.Time)
	})

	t := kube.NewTable(out, paint, "NS", "POD", "AGE", "REASON", "DETAIL")
	for _, e := range list {
		detail := e.detail
		if detail == "" || detail == "-" {
			detail = paint.Muted("-")
		}
		t.Row(e.pod.Namespace, e.pod.Name, age(e.pod.CreationTimestamp), paint.Status(e.reason), detail)
	}
	t.SortBy(f.Sort)
	return t.Flush()
}

// pendingReason derives a Pending pod's blocking reason and a compact detail.
// Scheduling failure (PodScheduled=False) takes precedence over container
// waiting states (image pulls, config errors).
func pendingReason(p corev1.Pod) (reason, detail string) {
	for _, cond := range p.Status.Conditions {
		if cond.Type == corev1.PodScheduled && cond.Status == corev1.ConditionFalse {
			r := cond.Reason
			if r == "" {
				r = "Unschedulable"
			}
			return r, schedulerCause(cond.Message)
		}
	}
	for _, css := range [][]corev1.ContainerStatus{p.Status.ContainerStatuses, p.Status.InitContainerStatuses} {
		for _, cs := range css {
			if cs.State.Waiting != nil && cs.State.Waiting.Reason != "" {
				reason = cs.State.Waiting.Reason
				switch reason {
				case "ImagePullBackOff", "ErrImagePull", "InvalidImageName":
					return reason, containerImage(p, cs.Name)
				default:
					return reason, "-"
				}
			}
		}
	}
	return "Pending", "-"
}

// containerImage returns the configured image for the named (init) container.
func containerImage(p corev1.Pod, name string) string {
	for _, css := range [][]corev1.Container{p.Spec.Containers, p.Spec.InitContainers} {
		for _, c := range css {
			if c.Name == name {
				return c.Image
			}
		}
	}
	return "-"
}

// schedulerCause condenses a verbose scheduler message into one clause, e.g.
// "Insufficient cpu (3 nodes)". It picks the clause with the largest node count
// and falls back to the trimmed raw message when the format is unrecognized, so
// the result is never empty.
func schedulerCause(msg string) string {
	const marker = "available: "
	i := strings.Index(msg, marker)
	if i < 0 {
		return trimSentence(msg)
	}
	tail := msg[i+len(marker):]
	if j := strings.Index(tail, ". "); j >= 0 {
		tail = tail[:j] // drop the trailing "preemption: ..." sentence
	}
	tail = strings.TrimRight(tail, ".")

	bestPhrase, bestCount := "", -1
	for _, clause := range strings.Split(tail, ", ") {
		count, phrase := splitLeadingCount(strings.TrimSpace(clause))
		if k := strings.Index(phrase, " {"); k >= 0 {
			phrase = phrase[:k] // strip the " {key: value}" blob
		}
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
	if fields := strings.SplitN(s, " ", 2); len(fields) == 2 {
		if n, err := strconv.Atoi(fields[0]); err == nil {
			return n, fields[1]
		}
	}
	return -1, s
}

// trimSentence keeps the first sentence of s, capped at 60 runes.
func trimSentence(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.Index(s, ". "); i >= 0 {
		s = s[:i]
	}
	s = strings.TrimRight(s, ".")
	if r := []rune(s); len(r) > 60 {
		return string(r[:60])
	}
	return s
}

// age renders a kubectl-style short duration since t, or "-" when unset.
func age(t metav1.Time) string {
	if t.IsZero() {
		return "-"
	}
	return duration.ShortHumanDuration(time.Since(t.Time))
}
