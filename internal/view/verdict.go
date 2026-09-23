package view

import (
	"cmp"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/PixiBixi/kubectl-klens/internal/kube"
)

// Shared helpers for the verdict commands (pdb, hpa, spread, probes): each
// classifies rows into a VERDICT column with a severity tier (ok/warn/bad/
// muted) that drives both the cell color and the default risk ordering.

// orDefault returns sort when the user set --sort, else fallback. The verdict
// commands default to sorting by their VERDICT column (risk order) so the
// riskiest rows land at the bottom, nearest the prompt, without a flag.
func orDefault(sort, fallback string) string {
	if sort == "" {
		return fallback
	}
	return sort
}

// byNsName orders objects by namespace, then name. Verdict views pre-sort
// with it as a deterministic tiebreak: the VERDICT sort applied at Flush is
// stable, so this order survives within each verdict.
func byNsName(a, b *metav1.ObjectMeta) int {
	return cmp.Or(cmp.Compare(a.Namespace, b.Namespace), cmp.Compare(a.Name, b.Name))
}

// flushVerdicts renders a verdict table: VERDICT ranked by the command's
// worst-first order, sorted by it unless --sort names another column.
func flushVerdicts(t *kube.Table, sort string, worstFirst ...string) error {
	t.SortRank("VERDICT", verdictRank(worstFirst...))
	t.SortBy(orDefault(sort, "verdict"))
	return t.Flush()
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

// verdictRank builds a Table.SortRank key for a VERDICT column from a command's
// verdicts listed worst-first. Severity tiers (the cell colors) are too coarse
// to order verdicts within a tier, so each command states its own risk order
// explicitly. `--sort verdict` then lands the riskiest rows at the bottom,
// nearest the shell prompt; verdicts absent from the list sort to the top.
func verdictRank(orderedWorstFirst ...string) func(string) int {
	n := len(orderedWorstFirst)
	rank := make(map[string]int, n)
	for i, v := range orderedWorstFirst {
		rank[v] = n - i
	}
	return func(cell string) int {
		return rank[cell] // unknown verdicts → 0 (top)
	}
}
