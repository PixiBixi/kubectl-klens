package view

import (
	"context"
	"io"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/PixiBixi/kubectl-klens/internal/kube"
)

// NoRequests lists containers missing CPU and/or memory requests (kube-system
// excluded); without requests the scheduler cannot bin-pack or reserve for
// them, hurting both packing efficiency and reliability.
func NoRequests(ctx context.Context, c kubernetes.Interface, f kube.Flags, args []string, out io.Writer) error {
	return reportMissing(ctx, c, f, out, func(ctr corev1.Container) corev1.ResourceList {
		return ctr.Resources.Requests
	})
}
