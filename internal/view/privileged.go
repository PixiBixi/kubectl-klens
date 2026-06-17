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

// Privileged lists containers with security-sensitive settings: privileged
// mode, allowed privilege escalation, explicit root, or pods sharing the host
// network/PID namespace or mounting host paths. Only flagged rows are shown.
func Privileged(ctx context.Context, c kubernetes.Interface, f kube.Flags, args []string, out io.Writer) error {
	pods, err := c.CoreV1().Pods(f.NamespaceScope()).List(ctx, metav1.ListOptions{})
	if err != nil {
		return err
	}
	t := kube.NewTable(out, "NS", "POD", "CONTAINER", "FLAGS")
	for _, p := range pods.Items {
		podFlags := podSecurityFlags(p)
		for _, ctr := range p.Spec.Containers {
			flags := append(containerSecurityFlags(ctr, p), podFlags...)
			if len(flags) > 0 {
				t.Row(p.Namespace, p.Name, ctr.Name, strings.Join(flags, ","))
			}
		}
	}
	t.SortBy(f.Sort)
	return t.Flush()
}

// podSecurityFlags returns the pod-level security concerns shared by all of a
// pod's containers.
func podSecurityFlags(p corev1.Pod) []string {
	var flags []string
	if p.Spec.HostNetwork {
		flags = append(flags, "hostNetwork")
	}
	if p.Spec.HostPID {
		flags = append(flags, "hostPID")
	}
	for _, v := range p.Spec.Volumes {
		if v.HostPath != nil {
			flags = append(flags, "hostPath")
			break
		}
	}
	return flags
}

// containerSecurityFlags returns the container-level security concerns,
// falling back to the pod security context to decide root.
func containerSecurityFlags(ctr corev1.Container, p corev1.Pod) []string {
	var flags []string
	if sc := ctr.SecurityContext; sc != nil {
		if sc.Privileged != nil && *sc.Privileged {
			flags = append(flags, "privileged")
		}
		if sc.AllowPrivilegeEscalation != nil && *sc.AllowPrivilegeEscalation {
			flags = append(flags, "privesc")
		}
	}
	if runsAsRoot(ctr, p) {
		flags = append(flags, "root")
	}
	return flags
}

// runsAsRoot reports whether a container explicitly runs as UID 0, honoring the
// container security context first, then the pod's.
func runsAsRoot(ctr corev1.Container, p corev1.Pod) bool {
	if sc := ctr.SecurityContext; sc != nil && sc.RunAsUser != nil {
		return *sc.RunAsUser == 0
	}
	if sc := p.Spec.SecurityContext; sc != nil && sc.RunAsUser != nil {
		return *sc.RunAsUser == 0
	}
	return false
}
