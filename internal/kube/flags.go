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
	Namespace      string   // raw -n value: a namespace name or a glob
	Namespaces     []string // -n resolved against the cluster; see ResolveScope
	AllNamespaces  bool
	Sort           string        // command-specific sort column (e.g. image-count)
	ColorMode      string        // raw --color value: "auto"|"always"|"never"|"" (unset)
	Color          bool          // resolved: whether to emit ANSI color
	RequestTimeout time.Duration // per-request deadline; 0 means no limit
	Watch          bool          // re-run the command until interrupted
	Interval       time.Duration // --watch poll period
}

// Scope returns the resolved set of namespaces to list in. Default (no -n, no
// -A) is all namespaces, matching the original wiki one-liners. -A forces all;
// -n narrows to the names ResolveScope expanded it to.
//
// The f.Namespace fallback keeps a Flags built by hand - every view test does
// that - working without a dispatcher round trip to resolve it.
func (f Flags) Scope() Scope {
	switch {
	case f.AllNamespaces:
		return Scope{}
	case len(f.Namespaces) > 0:
		return Scope{names: f.Namespaces}
	case f.Namespace != "":
		return Scope{names: []string{f.Namespace}}
	}
	return Scope{}
}

// ScopeIsAll reports whether the scope covers every namespace, without building
// the Scope. It is called once per object on the render path, so it must not
// allocate.
func (f Flags) ScopeIsAll() bool {
	return f.AllNamespaces || (f.Namespace == "" && len(f.Namespaces) == 0)
}

// NamespaceScope returns the single namespace a Get must target, or "" when the
// scope is not a single namespace (-A, or a glob that matched several). Lists
// use Scope instead.
func (f Flags) NamespaceScope() string {
	if ns, ok := f.Scope().One(); ok {
		return ns
	}
	return metav1.NamespaceAll
}
