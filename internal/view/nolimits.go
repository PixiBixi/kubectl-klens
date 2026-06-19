package view

import (
	"context"
	"io"
	"strings"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/PixiBixi/kubectl-klens/internal/kube"
)

// NoLimits lists containers missing CPU and/or memory limits (kube-system
// excluded), the usual source of noisy-neighbour and eviction surprises.
func NoLimits(ctx context.Context, c kubernetes.Interface, f kube.Flags, args []string, out io.Writer) error {
	return reportMissing(ctx, c, f, out, func(ctr corev1.Container) corev1.ResourceList {
		return ctr.Resources.Limits
	})
}

// reportMissing lists containers whose selected resource list is missing cpu
// and/or memory, with a MISSING column naming the gaps. kube-system is skipped.
func reportMissing(ctx context.Context, c kubernetes.Interface, f kube.Flags, out io.Writer, pick func(corev1.Container) corev1.ResourceList) error {
	pods, err := c.CoreV1().Pods(f.NamespaceScope()).List(ctx, metav1.ListOptions{})
	if err != nil {
		return err
	}
	paint := kube.NewPainter(f)
	t := kube.NewTable(out, paint, "NS", "POD", "CONTAINER", "MISSING")
	for _, p := range pods.Items {
		if p.Namespace == "kube-system" {
			continue
		}
		for _, ctr := range p.Spec.Containers {
			if m := missingResources(pick(ctr)); m != "" {
				t.Row(p.Namespace, p.Name, ctr.Name, paint.Warn(m))
			}
		}
	}
	t.SortBy(f.Sort)
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
