package view

import (
	"context"
	"io"
	"sort"
	"strconv"
	"strings"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/PixiBixi/kubectl-klens/internal/kube"
)

// Spread groups a namespace's replicas by their owning workload and reports how
// they are placed across nodes and zones, flagging single points of failure
// (all replicas on one node, or one zone). It complements pdb's drain-safety
// view with the placement side of availability. Rows default to VERDICT (risk)
// order, riskiest at the bottom.
func Spread(ctx context.Context, c kubernetes.Interface, f kube.Flags, args []string, out io.Writer) error {
	pods, err := c.CoreV1().Pods(f.NamespaceScope()).List(ctx, metav1.ListOptions{})
	if err != nil {
		return err
	}
	nodes, err := c.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return err
	}
	zoneOf := make(map[string]string, len(nodes.Items))
	for _, n := range nodes.Items {
		zoneOf[n.Name] = n.Labels["topology.kubernetes.io/zone"]
	}
	paint := kube.NewPainter(f)

	type agg struct {
		ns, workload string
		nodes, zones map[string]bool
		replicas     int
	}
	groups := map[string]*agg{}
	var order []string
	for _, p := range pods.Items {
		if p.Spec.NodeName == "" {
			continue
		}
		wl, ok := workloadKey(p)
		if !ok {
			continue
		}
		key := p.Namespace + "/" + wl
		g := groups[key]
		if g == nil {
			g = &agg{ns: p.Namespace, workload: wl, nodes: map[string]bool{}, zones: map[string]bool{}}
			groups[key] = g
			order = append(order, key)
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
	list := make([]entry, 0, len(order))
	for _, k := range order {
		g := groups[k]
		v, sev := spreadVerdict(g.replicas, len(g.nodes), len(g.zones))
		list = append(list, entry{g, v, sev})
	}
	sort.SliceStable(list, func(i, j int) bool {
		if ri, rj := sevRank(list[i].sev), sevRank(list[j].sev); ri != rj {
			return ri > rj
		}
		if list[i].g.ns != list[j].g.ns {
			return list[i].g.ns < list[j].g.ns
		}
		return list[i].g.workload < list[j].g.workload
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
	t.SortRank("VERDICT", verdictRank("SPOF-NODE", "SPOF-ZONE", "MULTI-NODE", "SINGLE", "SPREAD"))
	t.SortBy(orDefault(f.Sort, "verdict"))
	return t.Flush()
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
func workloadKey(p corev1.Pod) (string, bool) {
	ref := metav1.GetControllerOf(&p)
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
	if i := strings.LastIndex(name, "-"); i > 0 {
		return name[:i]
	}
	return name
}
