package view

import (
	"context"
	"io"
	"slices"

	corev1 "k8s.io/api/core/v1"

	"github.com/PixiBixi/kubectl-klens/internal/kube"
)

// Reqlim shows per-container requests/limits, including init and ephemeral
// containers: an init container's requests count toward the pod's effective
// scheduling footprint and toward ResourceQuota, so leaving them out
// understated the real cost. kube-system is excluded from the -A view only.
//
// --by-owner reads the workload specs (Deployment/StatefulSet/DaemonSet/Argo
// Rollout) instead of the running pods: one row per container per workload,
// with a REPLICAS column, and far cheaper on a large cluster - see
// podsForView. Ephemeral containers do not exist in a spec, so they only ever
// appear without the flag.
func Reqlim(ctx context.Context, c kube.Clients, f kube.Flags, args []string, out io.Writer) error {
	pods, err := podsForView(ctx, c, f)
	if err != nil {
		return err
	}
	paint := kube.NewPainter(f)
	t := kube.NewTable(out, paint, slices.Concat(
		[]string{"NS", podColumn(f, "POD")}, ownerHeaders(f),
		[]string{"CONTAINER", "KIND", "REQ_CPU", "LIM_CPU", "REQ_MEM", "LIM_MEM"},
	)...)
	// One reusable row buffer: Table.Row copies into its own arena, so nothing
	// outlives the iteration. A slices.Concat per row would heap-allocate on
	// every line - including without --by-owner, where it concatenates nothing.
	row := make([]string, 0, 9)
	for i := range pods {
		p := &pods[i]
		if skipNamespace(f, p.Namespace) {
			continue
		}
		for _, pc := range podContainers(p) {
			req, lim := pc.Spec.Resources.Requests, pc.Spec.Resources.Limits
			row = append(row[:0], p.Namespace, p.Name)
			row = appendOwnerCells(row, paint, f, p)
			row = append(row,
				pc.Spec.Name, pc.Kind,
				qtyOrNone(paint, req, corev1.ResourceCPU),
				qtyOrNone(paint, lim, corev1.ResourceCPU),
				qtyOrNone(paint, req, corev1.ResourceMemory),
				qtyOrNone(paint, lim, corev1.ResourceMemory),
			)
			t.Row(row...)
		}
	}
	t.SortBy(podSort(f, f.Sort, "POD"))
	return t.Flush()
}
