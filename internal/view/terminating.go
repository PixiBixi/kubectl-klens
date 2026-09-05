package view

import (
	"cmp"
	"context"
	"io"
	"slices"
	"strconv"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/PixiBixi/kubectl-klens/internal/kube"
)

// stuckAfter is how long a deletion has to hang before it stops being a normal
// shutdown and starts being a problem. Nothing in the API says when to give up
// waiting; five minutes is well past any sane terminationGracePeriodSeconds, so
// a deletion still pending after it is waiting on something that will not
// arrive by itself.
const stuckAfter = 5 * time.Minute

// Terminating lists what is being deleted and is not gone: pods carrying a
// deletionTimestamp and namespaces in the Terminating phase. It is the companion
// to pending - the same question at the other end of a resource's life - and it
// names the blocker, which is the part that takes digging: a finalizer nobody
// will clear, a node whose kubelet stopped answering (the pod cannot be
// confirmed dead, so it hangs forever), or namespace content that refuses to go.
// Cluster-wide by default, since a stuck namespace is not scoped to one.
func Terminating(ctx context.Context, c kube.Clients, f kube.Flags, args []string, out io.Writer) error {
	var (
		pods   []corev1.Pod
		nodes  []corev1.Node
		nsList []corev1.Namespace
	)
	scope := f.Scope()
	err := allLists(
		func() (err error) {
			pods, err = kube.ListPods(ctx, c, scope, metav1.ListOptions{})
			return err
		},
		func() (err error) {
			nodes, err = kube.ListNodes(ctx, c, metav1.ListOptions{})
			return err
		},
		func() (err error) {
			nsList, err = kube.ListNamespaces(ctx, c, metav1.ListOptions{})
			return err
		},
	)
	if err != nil {
		return err
	}
	unreachable := make(map[string]bool, len(nodes))
	for i := range nodes {
		if s := nodeStatus(&nodes[i]); s != "Ready" {
			unreachable[nodes[i].Name] = true
		}
	}
	paint := kube.NewPainter(f)

	type row struct {
		kind, ns, name, stuck, blocker, finalizers string
		verdict, sev                               string
	}
	var rows []row
	for i := range pods {
		p := &pods[i]
		if p.DeletionTimestamp == nil {
			continue
		}
		v, sev := terminatingVerdict(*p.DeletionTimestamp, p.DeletionGracePeriodSeconds)
		rows = append(rows, row{
			kind: "Pod", ns: p.Namespace, name: p.Name,
			stuck:      age(*p.DeletionTimestamp),
			blocker:    podBlocker(paint, p, unreachable),
			finalizers: finalizerCell(paint, p.Finalizers),
			verdict:    v, sev: sev,
		})
	}
	for i := range nsList {
		n := &nsList[i]
		if n.Status.Phase != corev1.NamespaceTerminating {
			continue
		}
		if !scope.All() && !slices.Contains(scope.Names(), n.Name) {
			continue
		}
		// A namespace has no grace period: it is deleted as soon as its content
		// is gone, so any elapsed time is time spent blocked on something.
		v, sev := terminatingVerdict(deletionTime(n), nil)
		rows = append(rows, row{
			kind: "Namespace", ns: paint.Muted("-"), name: n.Name,
			stuck:      age(deletionTime(n)),
			blocker:    namespaceBlocker(paint, n),
			finalizers: finalizerCell(paint, namespaceFinalizers(n)),
			verdict:    v, sev: sev,
		})
	}
	// Deterministic tiebreak for rows sharing a verdict; the VERDICT sort applied
	// at Flush is stable, so this order survives within each verdict.
	slices.SortStableFunc(rows, func(a, b row) int {
		return cmp.Or(
			cmp.Compare(a.kind, b.kind),
			cmp.Compare(a.ns, b.ns),
			cmp.Compare(a.name, b.name),
		)
	})

	t := kube.NewTable(out, paint, "KIND", "NS", "NAME", "STUCK-FOR", "BLOCKER", "FINALIZERS", "VERDICT")
	for i := range rows {
		r := &rows[i]
		t.Row(r.kind, r.ns, r.name, r.stuck, r.blocker, r.finalizers, sevPaint(paint, r.sev)(r.verdict))
	}
	t.SortRank("VERDICT", verdictRank("STUCK", "DELETING", "GRACE"))
	t.SortBy(orDefault(f.Sort, "verdict"))
	return t.Flush()
}

// terminatingVerdict grades how long a deletion has been pending. grace is the
// object's own remaining grace period when it has one.
func terminatingVerdict(deleted metav1.Time, grace *int64) (verdict, sev string) {
	elapsed := time.Since(deleted.Time)
	if grace != nil && elapsed < time.Duration(*grace)*time.Second {
		return "GRACE", "muted" // still shutting down within the period it was given
	}
	if elapsed < stuckAfter {
		return "DELETING", "warn" // past its grace period, not yet long enough to call it wedged
	}
	return "STUCK", "bad"
}

// podBlocker names what is most likely holding the pod, worst cause first: an
// unreachable node cannot confirm the pod is gone, and force-deleting is the
// only way out; a finalizer waits on whatever controller owns it.
func podBlocker(paint kube.Painter, p *corev1.Pod, unreachable map[string]bool) string {
	switch {
	case p.Spec.NodeName != "" && unreachable[p.Spec.NodeName]:
		return "node " + p.Spec.NodeName + " not ready"
	case len(p.Finalizers) > 0:
		return "finalizer"
	case p.Spec.NodeName == "":
		return paint.Muted("never scheduled")
	default:
		return paint.Muted("kubelet")
	}
}

// namespaceBlocker reports the namespace's own diagnosis. The controller records
// exactly why it cannot finish - leftover content, an unreachable aggregated API,
// finalizers it cannot clear - as conditions, which is the answer nobody finds
// because `get ns` does not print them.
func namespaceBlocker(paint kube.Painter, n *corev1.Namespace) string {
	for i := range n.Status.Conditions {
		cond := &n.Status.Conditions[i]
		if cond.Status == corev1.ConditionTrue {
			return string(cond.Type)
		}
	}
	if len(namespaceFinalizers(n)) > 0 {
		return "finalizer"
	}
	return paint.Muted("unknown")
}

// namespaceFinalizers merges the two places a namespace carries them: the usual
// metadata list and the legacy spec.finalizers the namespace controller uses.
func namespaceFinalizers(n *corev1.Namespace) []string {
	out := slices.Clone(n.Finalizers)
	for _, f := range n.Spec.Finalizers {
		out = append(out, string(f))
	}
	return out
}

// deletionTime falls back to the creation timestamp when a namespace reports
// Terminating without a deletionTimestamp, so the column is never blank.
func deletionTime(n *corev1.Namespace) metav1.Time {
	if n.DeletionTimestamp != nil {
		return *n.DeletionTimestamp
	}
	return n.CreationTimestamp
}

// finalizerCell lists the finalizers holding the object, which is what a reader
// has to know to unblock it, capped so one long list does not wreck the table.
func finalizerCell(paint kube.Painter, fins []string) string {
	if len(fins) == 0 {
		return paint.Muted("-")
	}
	if len(fins) > 2 {
		return strings.Join(fins[:2], ",") + ",+" + strconv.Itoa(len(fins)-2)
	}
	return strings.Join(fins, ",")
}
