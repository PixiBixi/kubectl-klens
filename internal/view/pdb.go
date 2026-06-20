package view

import (
	"context"
	"io"
	"sort"
	"strconv"

	policyv1 "k8s.io/api/policy/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/PixiBixi/kubectl-klens/internal/kube"
)

// Pdb lists PodDisruptionBudgets with a computed drain-safety verdict, so a
// stuck-drain or misconfigured PDB is readable at a glance instead of inferred
// from raw status fields. Rows default to risk-descending order.
func Pdb(ctx context.Context, c kubernetes.Interface, f kube.Flags, args []string, out io.Writer) error {
	pdbs, err := c.PolicyV1().PodDisruptionBudgets(f.NamespaceScope()).List(ctx, metav1.ListOptions{})
	if err != nil {
		return err
	}
	paint := kube.NewPainter(f)

	type entry struct {
		pdb     policyv1.PodDisruptionBudget
		verdict string
		sev     string
	}
	list := make([]entry, 0, len(pdbs.Items))
	for _, p := range pdbs.Items {
		v, sev := pdbVerdict(p.Status)
		list = append(list, entry{p, v, sev})
	}
	sort.SliceStable(list, func(i, j int) bool {
		if ri, rj := sevRank(list[i].sev), sevRank(list[j].sev); ri != rj {
			return ri > rj
		}
		if list[i].pdb.Namespace != list[j].pdb.Namespace {
			return list[i].pdb.Namespace < list[j].pdb.Namespace
		}
		return list[i].pdb.Name < list[j].pdb.Name
	})

	t := kube.NewTable(out, paint, "NS", "NAME", "POLICY", "EXPECTED", "DESIRED", "HEALTHY", "ALLOWED", "VERDICT")
	for _, e := range list {
		st := e.pdb.Status
		t.Row(
			e.pdb.Namespace,
			e.pdb.Name,
			pdbPolicy(paint, e.pdb.Spec),
			expectedCell(paint, st.ExpectedPods),
			strconv.Itoa(int(st.DesiredHealthy)),
			strconv.Itoa(int(st.CurrentHealthy)),
			allowedCell(paint, st.DisruptionsAllowed),
			sevPaint(paint, e.sev)(e.verdict),
		)
	}
	t.SortBy(f.Sort)
	return t.Flush()
}

// pdbVerdict classifies a PDB's drain-safety state from its status fields. The
// first matching rule wins; the rules are total, so a verdict is always
// returned. sev is one of ok/warn/bad/muted.
func pdbVerdict(s policyv1.PodDisruptionBudgetStatus) (verdict, sev string) {
	switch {
	case s.ExpectedPods == 0:
		return "ORPHAN", "muted" // selects no pods: inert, often a stale selector
	case s.DesiredHealthy == 0 && s.ExpectedPods >= 2:
		return "NO-GUARD", "bad" // zero floor on a multi-replica workload: a drain can evict every replica at once
	case s.DesiredHealthy >= s.ExpectedPods:
		return "PERMABLOCK", "bad" // floor >= population: never allows a disruption
	case s.DisruptionsAllowed == 0 && s.CurrentHealthy < s.DesiredHealthy:
		return "BLOCKED", "bad" // below floor and nothing allowed: drain stuck
	case s.DisruptionsAllowed == 0:
		return "AT-FLOOR", "warn" // at the floor: a drain blocks until a replacement is ready
	default:
		return "OK", "ok" // at least one pod can be evicted now
	}
}

// pdbPolicy renders the active constraint compactly: min=<v> or max=<v> (v may
// be a count or a percentage), or a muted placeholder when neither is set.
func pdbPolicy(paint kube.Painter, spec policyv1.PodDisruptionBudgetSpec) string {
	switch {
	case spec.MinAvailable != nil:
		return "min=" + spec.MinAvailable.String()
	case spec.MaxUnavailable != nil:
		return "max=" + spec.MaxUnavailable.String()
	}
	return paint.Muted("none")
}

// expectedCell mutes a zero population (ORPHAN), which would otherwise read like
// a healthy count of nothing.
func expectedCell(paint kube.Painter, n int32) string {
	s := strconv.Itoa(int(n))
	if n == 0 {
		return paint.Muted(s)
	}
	return s
}

// allowedCell colors the disruption budget headroom: none is bad, one is a
// warning, more is healthy slack.
func allowedCell(paint kube.Painter, n int32) string {
	s := strconv.Itoa(int(n))
	switch {
	case n == 0:
		return paint.Bad(s)
	case n == 1:
		return paint.Warn(s)
	default:
		return paint.OK(s)
	}
}

func sevRank(sev string) int {
	switch sev {
	case "bad":
		return 3
	case "warn":
		return 2
	case "muted":
		return 1
	default:
		return 0
	}
}

func sevPaint(paint kube.Painter, sev string) func(string) string {
	switch sev {
	case "bad":
		return paint.Bad
	case "warn":
		return paint.Warn
	case "muted":
		return paint.Muted
	default:
		return paint.OK
	}
}
