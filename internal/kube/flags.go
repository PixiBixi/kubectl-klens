package kube

import (
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// DefaultRequestTimeout bounds each apiserver request. It is a safety net for an
// unresponsive control plane, not a budget: the heaviest command measured on a
// 6500-pod cluster takes about four seconds, so this leaves an order of
// magnitude of headroom. --request-timeout=0 removes the bound.
const DefaultRequestTimeout = 60 * time.Second

// DefaultWatchInterval is the --watch poll period, matching watch(1). Every tick
// re-runs the command's full list calls, so MinWatchInterval floors it: the
// heaviest command takes about four seconds on a 6500-pod cluster, and a
// sub-second poll would hammer the apiserver with requests that cannot finish.
const (
	DefaultWatchInterval = 2 * time.Second
	MinWatchInterval     = 1 * time.Second
)

// Flags holds the standard kubeconfig-related options shared by all commands,
// plus optional command-specific options registered via Command.RegisterFlags.
type Flags struct {
	Kubeconfig     string
	Context        string
	Namespace      string
	AllNamespaces  bool
	Sort           string        // command-specific sort column (e.g. image-count)
	ColorMode      string        // raw --color value: "auto"|"always"|"never"|"" (unset)
	Color          bool          // resolved: whether to emit ANSI color
	RequestTimeout time.Duration // per-request deadline; 0 means no limit
	Watch          bool          // re-run the command until interrupted
	Interval       time.Duration // --watch poll period
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
