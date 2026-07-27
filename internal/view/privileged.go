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
// mode, allowed privilege escalation, added node-level capabilities, host ports,
// explicit root, or pods sharing the host network/PID/IPC namespaces or mounting
// host paths. Init and ephemeral containers are covered too — a privileged init
// container escalates exactly as far as an app one. Only flagged rows are shown.
func Privileged(ctx context.Context, c kubernetes.Interface, f kube.Flags, args []string, out io.Writer) error {
	pods, err := kube.ListPods(ctx, c, f.NamespaceScope(), metav1.ListOptions{})
	if err != nil {
		return err
	}
	paint := kube.NewPainter(f)
	t := kube.NewTable(out, paint, "NS", "POD", "CONTAINER", "KIND", "FLAGS")
	for i := range pods {
		p := &pods[i]
		podFlags := podSecurityFlags(p)
		for _, pc := range podContainers(p) {
			flags := containerSecurityFlags(pc.Spec, p)
			flags = append(flags, podFlags...)
			if !hasHardFinding(flags) {
				continue
			}
			t.Row(p.Namespace, p.Name, pc.Spec.Name, pc.Kind, paint.Bad(strings.Join(flags, ",")))
		}
	}
	t.SortBy(f.Sort)
	return t.Flush()
}

// privEscDefault marks a container that leaves allowPrivilegeEscalation unset,
// which Kubernetes resolves to true. It is reported only as extra context on
// rows that already carry another finding: on a normal cluster nearly every
// container leaves it unset, so triggering a row on it alone would bury the
// real findings instead of surfacing them.
const privEscDefault = "privesc-default"

// hasHardFinding reports whether flags hold anything beyond the ambient
// privesc-default note, i.e. whether the row is worth printing at all.
func hasHardFinding(flags []string) bool {
	for _, f := range flags {
		if f != privEscDefault {
			return true
		}
	}
	return false
}

// dangerousCaps are Linux capabilities that grant host-level power when added —
// SYS_ADMIN alone is broadly equivalent to privileged mode — or that the
// restricted Pod Security Standard forbids outright. Capabilities present in a
// runtime's default set (SETUID, DAC_OVERRIDE, ...) are deliberately absent:
// only explicit additions are inspected, and listing the defaults would add
// noise without adding risk.
var dangerousCaps = map[string]bool{
	"ALL": true, "SYS_ADMIN": true, "SYS_MODULE": true, "SYS_PTRACE": true,
	"SYS_BOOT": true, "SYS_TIME": true, "SYS_RAWIO": true, "SYS_CHROOT": true,
	"NET_ADMIN": true, "NET_RAW": true, "DAC_READ_SEARCH": true,
	"BPF": true, "PERFMON": true, "AUDIT_CONTROL": true,
}

// podSecurityFlags returns the pod-level security concerns shared by all of a
// pod's containers.
func podSecurityFlags(p *corev1.Pod) []string {
	var flags []string
	if p.Spec.HostNetwork {
		flags = append(flags, "hostNetwork")
	}
	if p.Spec.HostPID {
		flags = append(flags, "hostPID")
	}
	if p.Spec.HostIPC {
		flags = append(flags, "hostIPC")
	}
	for i := range p.Spec.Volumes {
		v := &p.Spec.Volumes[i]
		if v.HostPath != nil {
			flags = append(flags, "hostPath")
			break
		}
	}
	return flags
}

// containerSecurityFlags returns the container-level security concerns, falling
// back to the pod security context to decide root.
func containerSecurityFlags(ctr *corev1.Container, p *corev1.Pod) []string {
	var flags []string
	if sc := ctr.SecurityContext; sc != nil {
		if sc.Privileged != nil && *sc.Privileged {
			flags = append(flags, "privileged")
		}
		switch {
		case sc.AllowPrivilegeEscalation == nil:
			flags = append(flags, privEscDefault)
		case *sc.AllowPrivilegeEscalation:
			flags = append(flags, "privesc")
		}
		if caps := addedDangerousCaps(sc.Capabilities); caps != "" {
			flags = append(flags, "caps="+caps)
		}
	} else {
		// No securityContext at all: allowPrivilegeEscalation defaults to true.
		flags = append(flags, privEscDefault)
	}
	for _, port := range ctr.Ports {
		if port.HostPort != 0 {
			flags = append(flags, "hostPort")
			break
		}
	}
	if runsAsRoot(ctr, p) {
		flags = append(flags, "root")
	}
	return flags
}

// addedDangerousCaps lists the explicitly added capabilities that appear in
// dangerousCaps, joined with "+", or "" when none do. Names are matched
// case-insensitively with an optional CAP_ prefix, since both spellings occur in
// the wild.
func addedDangerousCaps(caps *corev1.Capabilities) string {
	if caps == nil {
		return ""
	}
	var found []string
	for _, c := range caps.Add {
		name := strings.TrimPrefix(strings.ToUpper(string(c)), "CAP_")
		if dangerousCaps[name] {
			found = append(found, name)
		}
	}
	return strings.Join(found, "+")
}

// runsAsRoot reports whether a container explicitly runs as UID 0, honoring the
// container security context first, then the pod's.
func runsAsRoot(ctr *corev1.Container, p *corev1.Pod) bool {
	if sc := ctr.SecurityContext; sc != nil && sc.RunAsUser != nil {
		return *sc.RunAsUser == 0
	}
	if sc := p.Spec.SecurityContext; sc != nil && sc.RunAsUser != nil {
		return *sc.RunAsUser == 0
	}
	return false
}
