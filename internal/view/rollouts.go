package view

import (
	"cmp"
	"context"
	"io"
	"slices"
	"strconv"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/PixiBixi/kubectl-klens/internal/kube"
)

// rolloutGVR is the Argo Rollouts CRD. Rollouts are read through the dynamic
// client because the typed clientset has no scheme for them, and they are the
// one workload kind whose progress is invisible to `get deploy`.
var rolloutGVR = schema.GroupVersionResource{Group: "argoproj.io", Version: "v1alpha1", Resource: "rollouts"}

// Rollouts answers "is everything finished rolling out": one row per workload
// with its replica counts and a verdict, so a deployment stuck behind its
// progress deadline or a canary paused mid-promotion is visible without reading
// conditions. Covers Deployments, StatefulSets, DaemonSets and, when the CRD is
// installed, Argo Rollouts. Rows default to VERDICT (risk) order, riskiest at
// the bottom.
func Rollouts(ctx context.Context, c kube.Clients, f kube.Flags, args []string, out io.Writer) error {
	var (
		w    builtinWorkloads
		argo []unstructured.Unstructured
	)
	scope := f.Scope()
	err := kube.Concurrent(append(w.listers(ctx, c, scope), optionalCRD(ctx, c, rolloutGVR, scope, &argo))...)
	if err != nil {
		return err
	}
	paint := kube.NewPainter(f)

	rows := make([]rolloutRow, 0, w.len()+len(argo))
	for i := range w.deploys {
		rows = append(rows, deploymentRow(&w.deploys[i]))
	}
	for i := range w.stateful {
		rows = append(rows, statefulSetRow(&w.stateful[i]))
	}
	for i := range w.daemons {
		rows = append(rows, daemonSetRow(&w.daemons[i]))
	}
	for i := range argo {
		rows = append(rows, argoRolloutRow(&argo[i]))
	}
	// Deterministic tiebreak for rows sharing a verdict; the VERDICT sort applied
	// at Flush is stable, so this order survives within each verdict.
	slices.SortStableFunc(rows, func(a, b rolloutRow) int {
		return cmp.Or(
			cmp.Compare(a.ns, b.ns),
			cmp.Compare(a.kind, b.kind),
			cmp.Compare(a.name, b.name),
		)
	})

	t := kube.NewTable(out, paint, "NS", "KIND", "NAME", "DESIRED", "READY", "UPDATED", "AVAILABLE", "STATE", "VERDICT")
	for i := range rows {
		r := &rows[i]
		v, sev := rolloutVerdict(r)
		t.Row(
			r.ns,
			r.kind,
			r.name,
			strconv.Itoa(r.desired),
			countCell(paint, r.ready, r.desired),
			countCell(paint, r.updated, r.desired),
			countCell(paint, r.available, r.desired),
			orMutedDash(paint, r.state),
			sevPaint(paint, sev)(v),
		)
	}
	return flushVerdicts(t, f.Sort, "STALLED", "DOWN", "NOT-OBSERVED", "PROGRESSING", "PAUSED", "SCALED-ZERO", "OK")
}

// rolloutRow is one workload normalized across the four kinds, so the verdict
// rules are written once instead of per API type.
type rolloutRow struct {
	ns, kind, name                     string
	desired, ready, updated, available int
	state                              string // the controller's own word for where it is
	paused, degraded, observed         bool
}

// rolloutVerdict classifies rollout progress. The first matching rule wins and
// the rules are total.
func rolloutVerdict(r *rolloutRow) (verdict, sev string) {
	switch {
	case r.desired == 0:
		return "SCALED-ZERO", "muted" // nothing wanted, nothing to roll out
	case r.degraded:
		return "STALLED", "bad" // the controller gave up: past its progress deadline, or Argo called it degraded
	case r.available == 0:
		return "DOWN", "bad" // replicas wanted, none serving
	case !r.observed:
		return "NOT-OBSERVED", "bad" // the controller has not acted on the current spec yet: a wedged or missing controller
	case r.paused:
		return "PAUSED", "warn" // deliberate, but the rollout will not finish on its own
	case r.ready < r.desired || r.updated < r.desired:
		return "PROGRESSING", "warn" // mid-rollout: normal for a while, a problem if it stays
	default:
		return "OK", "ok"
	}
}

func deploymentRow(d *appsv1.Deployment) rolloutRow {
	state, failed := progressing(d.Status.Conditions)
	return rolloutRow{
		ns: d.Namespace, kind: "Deployment", name: d.Name,
		desired:   replicasOrOne(d.Spec.Replicas),
		ready:     int(d.Status.ReadyReplicas),
		updated:   int(d.Status.UpdatedReplicas),
		available: int(d.Status.AvailableReplicas),
		state:     state,
		paused:    d.Spec.Paused,
		// Progressing=False is how the deployment controller reports
		// ProgressDeadlineExceeded, the one state that will not resolve itself.
		degraded: failed,
		observed: d.Status.ObservedGeneration >= d.Generation,
	}
}

func statefulSetRow(s *appsv1.StatefulSet) rolloutRow {
	return rolloutRow{
		ns: s.Namespace, kind: "StatefulSet", name: s.Name,
		desired:   replicasOrOne(s.Spec.Replicas),
		ready:     int(s.Status.ReadyReplicas),
		updated:   int(s.Status.UpdatedReplicas),
		available: int(s.Status.AvailableReplicas),
		// A StatefulSet has no progress deadline and no conditions worth a
		// state: its revisions are the only hint about an in-flight update.
		state:    revisionState(s.Status.CurrentRevision, s.Status.UpdateRevision),
		observed: s.Status.ObservedGeneration >= s.Generation,
	}
}

func daemonSetRow(d *appsv1.DaemonSet) rolloutRow {
	return rolloutRow{
		ns: d.Namespace, kind: "DaemonSet", name: d.Name,
		// A DaemonSet has no spec.replicas: its population is however many nodes
		// its selector and tolerations reach.
		desired:   int(d.Status.DesiredNumberScheduled),
		ready:     int(d.Status.NumberReady),
		updated:   int(d.Status.UpdatedNumberScheduled),
		available: int(d.Status.NumberAvailable),
		state:     misscheduledState(d.Status.NumberMisscheduled),
		observed:  d.Status.ObservedGeneration >= d.Generation,
	}
}

// argoRolloutRow reads an Argo Rollout's progress out of unstructured status.
// Every field is optional by construction: a Rollout the controller has not
// reconciled yet carries almost no status at all, and a missing field must read
// as zero rather than fail the whole table.
func argoRolloutRow(u *unstructured.Unstructured) rolloutRow {
	phase, _, _ := unstructured.NestedString(u.Object, "status", "phase")
	return rolloutRow{
		ns: u.GetNamespace(), kind: "Rollout", name: u.GetName(),
		desired:   argoReplicas(u),
		ready:     nestedInt(u, "status", "readyReplicas"),
		updated:   nestedInt(u, "status", "updatedReplicas"),
		available: nestedInt(u, "status", "availableReplicas"),
		state:     argoState(u, phase),
		paused:    phase == "Paused",
		degraded:  phase == "Degraded",
		// Argo's status.observedGeneration has been both an int and a string
		// across versions, so it is not compared: the phase already says
		// whether the controller is acting on the Rollout.
		observed: true,
	}
}

// argoState reports the phase with the canary step the rollout sits on, which is
// the question asked of a paused canary: how far did it get.
func argoState(u *unstructured.Unstructured, phase string) string {
	if phase == "" {
		phase = "Unknown"
	}
	steps, found, _ := unstructured.NestedSlice(u.Object, "spec", "strategy", "canary", "steps")
	if !found || len(steps) == 0 {
		return phase
	}
	step, _, _ := unstructured.NestedInt64(u.Object, "status", "currentStepIndex")
	return phase + " " + strconv.FormatInt(step, 10) + "/" + strconv.Itoa(len(steps))
}

// replicasOrOne resolves an unset spec.replicas to the API default of 1, so a
// manifest that omits it is not read as scaled to zero.
func replicasOrOne(n *int32) int {
	if n == nil {
		return 1
	}
	return int(*n)
}

// progressing reads a Deployment's Progressing condition: its reason, and
// whether it is False.
func progressing(conds []appsv1.DeploymentCondition) (reason string, failed bool) {
	for i := range conds {
		if conds[i].Type == appsv1.DeploymentProgressing {
			return conds[i].Reason, conds[i].Status == corev1.ConditionFalse
		}
	}
	return "", false
}

// revisionState names an in-flight StatefulSet update: the two revisions differ
// while pods are still being replaced.
func revisionState(current, update string) string {
	if update != "" && current != update {
		return "RevisionUpdating"
	}
	return ""
}

// misscheduledState surfaces DaemonSet pods running where they no longer belong,
// which no other column would show.
func misscheduledState(n int32) string {
	if n > 0 {
		return "Misscheduled=" + strconv.Itoa(int(n))
	}
	return ""
}

// countCell colors a count against the desired replica count: all there is
// healthy, none at all is bad, anything between is an incomplete rollout.
func countCell(paint kube.Painter, n, desired int) string {
	s := strconv.Itoa(n)
	switch {
	case desired == 0:
		return paint.Muted(s)
	case n >= desired:
		return paint.OK(s)
	case n == 0:
		return paint.Bad(s)
	default:
		return paint.Warn(s)
	}
}
