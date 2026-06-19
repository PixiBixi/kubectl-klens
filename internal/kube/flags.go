package kube

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

// Flags holds the standard kubeconfig-related options shared by all commands,
// plus optional command-specific options registered via Command.RegisterFlags.
type Flags struct {
	Kubeconfig    string
	Context       string
	Namespace     string
	AllNamespaces bool
	Sort          string // command-specific sort column (e.g. image-count)
	ColorMode     string // raw --color value: "auto"|"always"|"never"|"" (unset)
	Color         bool   // resolved: whether to emit ANSI color
}

// NamespaceScope returns the namespace to list in. Empty string means all
// namespaces. Default (no -n, no -A) is all namespaces, matching the original
// wiki one-liners. -A forces all; -n narrows.
func (f Flags) NamespaceScope() string {
	if f.AllNamespaces {
		return metav1.NamespaceAll
	}
	return f.Namespace
}
