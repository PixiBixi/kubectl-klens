package view

import (
	"context"
	"fmt"
	"io"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/PixiBixi/kubectl-klens/internal/kube"
)

// Autoscaler reads the cluster-autoscaler-status ConfigMap from kube-system
// and prints its status field verbatim.
func Autoscaler(ctx context.Context, c kubernetes.Interface, f kube.Flags, args []string, out io.Writer) error {
	cm, err := c.CoreV1().ConfigMaps("kube-system").Get(ctx, "cluster-autoscaler-status", metav1.GetOptions{})
	if err != nil {
		return err
	}
	status, ok := cm.Data["status"]
	if !ok {
		return fmt.Errorf("configmap cluster-autoscaler-status has no \"status\" field")
	}
	fmt.Fprintln(out, status)
	return nil
}
