package kube

import (
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

// Clients bundles the API clients a view can reach. kubernetes.Interface is
// embedded rather than named so a view keeps calling c.CoreV1() directly and
// stays passable to the List* helpers; Dynamic is what reads resources the
// typed clientset has no scheme for - CRDs such as Argo Rollouts.
//
// Dynamic is nil in tests that never touch a CRD, so a view must treat a
// missing dynamic client as "CRD unavailable" instead of dereferencing it.
type Clients struct {
	kubernetes.Interface
	Dynamic dynamic.Interface
}

// clientConfig builds the deferred loading config from the default loading
// rules plus the explicit kubeconfig path and context override. Same pattern
// as kubearch. Shared by NewClients and CurrentNamespace.
func clientConfig(f Flags) clientcmd.ClientConfig {
	loadingRules := clientcmd.NewDefaultClientConfigLoadingRules()
	if f.Kubeconfig != "" {
		loadingRules.ExplicitPath = f.Kubeconfig
	}
	overrides := &clientcmd.ConfigOverrides{}
	if f.Context != "" {
		overrides.CurrentContext = f.Context
	}
	return clientcmd.NewNonInteractiveDeferredLoadingClientConfig(loadingRules, overrides)
}

// restConfig resolves the kubeconfig into a REST config both clients share.
func restConfig(f Flags) (*rest.Config, error) {
	cfg, err := clientConfig(f).ClientConfig()
	if err != nil {
		return nil, err
	}
	// Deliberately not setting ContentType/AcceptContentTypes: the typed
	// clientset already negotiates protobuf on its own. Measured against a
	// 6300-pod cluster, listing every pod transfers 85.7 MiB in 3.0s by default
	// versus 116.5 MiB in 5.4s with JSON forced, and the default response comes
	// back as application/vnd.kubernetes.protobuf. Setting it explicitly is a
	// no-op, so there is no protobuf tuning to be had here.
	//
	// Turn off client-side rate limiting. client-go defaults a kubeconfig-built
	// config to QPS 5 / Burst 10, sized for a controller that runs forever, and
	// the limiter is shared across the concurrent lists a view issues. Paging a
	// 6500-pod cluster at ChunkSize takes 13 requests: measured against a local
	// server with no network latency, that is 606ms with the defaults versus
	// 44ms without, i.e. ~560ms of pure self-inflicted waiting per list.
	//
	// Safe because the apiserver does its own admission control (API Priority
	// and Fairness), a one-shot CLI issues tens of requests and then exits, and
	// kubectl disables it the same way. See TestNoClientSideThrottling.
	cfg.QPS = -1

	// Bound the wait on an unresponsive apiserver. Without this a hung control
	// plane hangs the command indefinitely, since nothing else sets a deadline.
	cfg.Timeout = f.RequestTimeout
	return cfg, nil
}

// NewClients builds the typed and dynamic clients from the resolved kubeconfig.
// Both are created up front: it costs two structs and no round trip, and the
// dynamic client only talks to the apiserver when a view lists a CRD.
func NewClients(f Flags) (Clients, error) {
	cfg, err := restConfig(f)
	if err != nil {
		return Clients{}, err
	}
	typed, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return Clients{}, err
	}
	dyn, err := dynamic.NewForConfig(cfg)
	if err != nil {
		return Clients{}, err
	}
	return Clients{Interface: typed, Dynamic: dyn}, nil
}

// CurrentNamespace returns the namespace of the active kubeconfig context - the
// "shell" namespace as set by kubens/kubectx - defaulting to "default" when the
// context pins none.
func CurrentNamespace(f Flags) (string, error) {
	ns, _, err := clientConfig(f).Namespace()
	return ns, err
}
