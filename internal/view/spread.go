package view

import (
	"cmp"
	"context"
	"io"
	"slices"
	"strconv"
	"strings"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	"github.com/PixiBixi/kubectl-klens/internal/kube"
)

// Spread groups a namespace's replicas by their owning workload and reports how
// they are placed across nodes and zones, flagging single points of failure
// (all replicas on one node, or one zone). It complements pdb's drain-safety
// view with the placement side of availability. Rows default to VERDICT (risk)
// order, riskiest at the bottom.
func Spread(ctx context.Context, c kube.Clients, f kube.Flags, args []string, out io.Writer) error {
	pods, nodes, err := bothLists(
		func() ([]corev1.Pod, error) { return kube.ListPods(ctx, c, f.Scope(), metav1.ListOptions{}) },
		func() ([]corev1.Node, error) { return kube.ListNodes(ctx, c, metav1.ListOptions{}) },
	)
	if err != nil {
		return err
	}
	zoneOf := make(map[string]string, len(nodes))
	for i := range nodes {
		n := &nodes[i]
		zoneOf[n.Name] = n.Labels[corev1.LabelTopologyZone]
	}
	paint := kube.NewPainter(f)

	type agg struct {
		ns, workload string
		nodes, zones map[string]bool
		replicas     int
	}
	groups := map[types.NamespacedName]*agg{}
	for i := range pods {
		p := &pods[i]
		if p.Spec.NodeName == "" {
			continue
		}
		wl, ok := workloadKey(p)
		if !ok {
			continue
		}
		key := types.NamespacedName{Namespace: p.Namespace, Name: wl}
		g := groups[key]
		if g == nil {
			g = &agg{ns: p.Namespace, workload: wl, nodes: map[string]bool{}, zones: map[string]bool{}}
			groups[key] = g
		}
		g.replicas++
		g.nodes[p.Spec.NodeName] = true
		if z := zoneOf[p.Spec.NodeName]; z != "" {
			g.zones[z] = true
		}
	}

	type entry struct {
		g            *agg
		verdict, sev string
	}
	// Map order is fine: (ns, workload) is unique, so the sort below is total.
	list := make([]entry, 0, len(groups))
	for _, g := range groups {
		v, sev := spreadVerdict(g.replicas, len(g.nodes), len(g.zones))
		list = append(list, entry{g, v, sev})
	}
	// Deterministic tiebreak for rows with equal sort keys; the VERDICT sort
	// applied at Flush is stable, so this order survives within each verdict.
	slices.SortStableFunc(list, func(a, b entry) int {
		return cmp.Or(
			cmp.Compare(a.g.ns, b.g.ns),
			cmp.Compare(a.g.workload, b.g.workload),
		)
	})

	t := kube.NewTable(out, paint, "NS", "WORKLOAD", "REPLICAS", "NODES", "ZONES", "VERDICT")
	for _, e := range list {
		t.Row(
			e.g.ns, e.g.workload,
			strconv.Itoa(e.g.replicas),
			strconv.Itoa(len(e.g.nodes)),
			strconv.Itoa(len(e.g.zones)),
			sevPaint(paint, e.sev)(e.verdict),
		)
	}
	return flushVerdicts(t, f.Sort, "SPOF-NODE", "SPOF-ZONE", "MULTI-NODE", "SINGLE", "SPREAD")
}

// spreadVerdict classifies replica placement from the distinct node and zone
// counts. The first matching rule wins; the rules are total. sev is one of
// ok/warn/bad/muted.
func spreadVerdict(replicas, nodes, zones int) (verdict, sev string) {
	switch {
	case replicas <= 1:
		return "SINGLE", "muted" // non-HA by design
	case nodes <= 1:
		return "SPOF-NODE", "bad" // all replicas on one node
	case zones >= 2:
		return "SPREAD", "ok" // across zones
	case zones == 1:
		return "SPOF-ZONE", "warn" // multi-node, single zone
	default:
		return "MULTI-NODE", "muted" // multi-node, zone topology unknown
	}
}

// workloadKey maps a pod to its owning workload label, reporting false for pods
// that aren't HA replicas (DaemonSet, Job, uncontrolled). ReplicaSet owners are
// collapsed to their Deployment by trimming the pod-template-hash suffix.
func workloadKey(p *corev1.Pod) (string, bool) {
	ref := metav1.GetControllerOf(p)
	if ref == nil {
		return "", false
	}
	switch ref.Kind {
	case "ReplicaSet":
		return "Deployment/" + trimHash(ref.Name), true
	case "StatefulSet", "ReplicationController":
		return ref.Kind + "/" + ref.Name, true
	default:
		return "", false
	}
}

// trimHash drops the final "-<segment>" of a ReplicaSet name (its
// pod-template-hash) to recover the Deployment name.
func trimHash(name string) string {
	if before, _, ok := strings.CutLast(name, "-"); ok && before != "" {
		return before
	}
	return name
}
