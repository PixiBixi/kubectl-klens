package view

import (
	"cmp"
	"context"
	"io"
	"slices"
	"strconv"

	appsv1 "k8s.io/api/apps/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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
		deploys  []appsv1.Deployment
		stateful []appsv1.StatefulSet
		daemons  []appsv1.DaemonSet
		argo     []unstructured.Unstructured
	)
	ns := f.NamespaceScope()
	err := allLists(
		func() (err error) {
			deploys, err = kube.ListDeployments(ctx, c, ns, metav1.ListOptions{})
			return err
		},
		func() (err error) {
			stateful, err = kube.ListStatefulSets(ctx, c, ns, metav1.ListOptions{})
			return err
		},
		func() (err error) {
			daemons, err = kube.ListDaemonSets(ctx, c, ns, metav1.ListOptions{})
			return err
		},
		func() error {
			list, err := kube.ListCustom(ctx, c.Dynamic, rolloutGVR, ns, metav1.ListOptions{})
			// A cluster without Argo Rollouts installed, or a user without
			// access to them, is the normal case rather than a failure: the
			// other three kinds still make a useful table, so the CRD rows are
			// simply absent.
			if meta.IsNoMatchError(err) || apierrors.IsNotFound(err) || apierrors.IsForbidden(err) {
				return nil
			}
			argo = list
			return err
		},
	)
	if err != nil {
		return err
	}
	paint := kube.NewPainter(f)

	rows := make([]rolloutRow, 0, len(deploys)+len(stateful)+len(daemons)+len(argo))
	for i := range deploys {
		rows = append(rows, deploymentRow(&deploys[i]))
	}
	for i := range stateful {
		rows = append(rows, statefulSetRow(&stateful[i]))
	}
	for i := range daemons {
		rows = append(rows, daemonSetRow(&daemons[i]))
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
			stateCell(paint, r.state),
			sevPaint(paint, sev)(v),
		)
	}
	t.SortRank("VERDICT", verdictRank("STALLED", "DOWN", "NOT-OBSERVED", "PROGRESSING", "PAUSED", "SCALED-ZERO", "OK"))
	t.SortBy(orDefault(f.Sort, "verdict"))
	return t.Flush()
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
	return rolloutRow{
		ns: d.Namespace, kind: "Deployment", name: d.Name,
		desired:   replicasOrOne(d.Spec.Replicas),
		ready:     int(d.Status.ReadyReplicas),
		updated:   int(d.Status.UpdatedReplicas),
		available: int(d.Status.AvailableReplicas),
		state:     progressState(d.Status.Conditions),
		paused:    d.Spec.Paused,
		// Progressing=False is how the deployment controller reports
		// ProgressDeadlineExceeded, the one state that will not resolve itself.
		degraded: progressingFailed(d.Status.Conditions),
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
	spec, _, _ := unstructured.NestedInt64(u.Object, "spec", "replicas")
	if spec == 0 {
		// Argo defaults spec.replicas to 1 like the built-in controllers do, so
		// an unset field means one replica, not zero.
		if _, found, _ := unstructured.NestedFieldNoCopy(u.Object, "spec", "replicas"); !found {
			spec = 1
		}
	}
	return rolloutRow{
		ns: u.GetNamespace(), kind: "Rollout", name: u.GetName(),
		desired:   int(spec),
		ready:     nestedCount(u, "readyReplicas"),
		updated:   nestedCount(u, "updatedReplicas"),
		available: nestedCount(u, "availableReplicas"),
		state:     argoState(u, phase),
		paused:    phase == "Paused",
		degraded:  phase == "Degraded",
		// Argo's status.observedGeneration has been both an int and a string
		// across versions, so it is not compared: the phase already says
		// whether the controller is acting on the Rollout.
		observed: true,
	}
}

func nestedCount(u *unstructured.Unstructured, field string) int {
	n, _, _ := unstructured.NestedInt64(u.Object, "status", field)
	return int(n)
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

func progressState(conds []appsv1.DeploymentCondition) string {
	for i := range conds {
		if conds[i].Type == appsv1.DeploymentProgressing {
			return conds[i].Reason
		}
	}
	return ""
}

func progressingFailed(conds []appsv1.DeploymentCondition) bool {
	for i := range conds {
		if conds[i].Type == appsv1.DeploymentProgressing {
			return conds[i].Status == "False"
		}
	}
	return false
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

// stateCell mutes the placeholder for the kinds that report no state.
func stateCell(paint kube.Painter, state string) string {
	if state == "" {
		return paint.Muted("-")
	}
	return state
}
