package view

import (
	"context"
	"io"
	"slices"
	"strings"

	corev1 "k8s.io/api/core/v1"

	"github.com/PixiBixi/kubectl-klens/internal/kube"
)

// NoLimits lists containers missing CPU and/or memory limits (kube-system
// excluded from the -A view), the usual source of noisy-neighbour and eviction
// surprises. --by-owner reads the workload specs instead of the running pods;
// see Reqlim's doc comment.
func NoLimits(ctx context.Context, c kube.Clients, f kube.Flags, args []string, out io.Writer) error {
	return reportMissing(ctx, c, f, out, func(ctr *corev1.Container) corev1.ResourceList {
		return ctr.Resources.Limits
	})
}

// reportMissing lists containers whose selected resource list is missing cpu
// and/or memory, with a MISSING column naming the gaps. Init and ephemeral
// containers are checked too: LimitRange and ResourceQuota admission apply to
// them as well, so an unbounded init container is a real gap, not a detail.
// kube-system is skipped only when listing across all namespaces.
func reportMissing(ctx context.Context, c kube.Clients, f kube.Flags, out io.Writer, pick func(*corev1.Container) corev1.ResourceList) error {
	pods, err := podsForView(ctx, c, f)
	if err != nil {
		return err
	}
	paint := kube.NewPainter(f)
	t := kube.NewTable(out, paint, slices.Concat(
		[]string{"NS", podColumn(f, "POD")}, ownerHeaders(f),
		[]string{"CONTAINER", "KIND", "MISSING"},
	)...)
	// Reused row buffer; see Reqlim for why this is not a slices.Concat.
	row := make([]string, 0, 6)
	for i := range pods {
		p := &pods[i]
		if skipNamespace(f, p.Namespace) {
			continue
		}
		for _, pc := range podContainers(p) {
			if m := missingResources(pick(pc.Spec)); m != "" {
				row = append(row[:0], p.Namespace, p.Name)
				row = appendOwnerCells(row, paint, f, p)
				row = append(row, pc.Spec.Name, pc.Kind, paint.Warn(m))
				t.Row(row...)
			}
		}
	}
	t.SortBy(podSort(f, f.Sort, "POD"))
	return t.Flush()
}

// missingResources returns which of cpu/memory are absent from rl as a
// comma-joined string, or "" when both are present.
func missingResources(rl corev1.ResourceList) string {
	var missing []string
	if _, ok := rl[corev1.ResourceCPU]; !ok {
		missing = append(missing, "cpu")
	}
	if _, ok := rl[corev1.ResourceMemory]; !ok {
		missing = append(missing, "memory")
	}
	return strings.Join(missing, ",")
}
