package kube

import (
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
)

// clientConfig builds the deferred loading config from the default loading
// rules plus the explicit kubeconfig path and context override. Same pattern
// as kubearch. Shared by Client and CurrentNamespace.
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

// Client builds a clientset from the resolved kubeconfig.
func Client(f Flags) (kubernetes.Interface, error) {
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
	// Bound the wait on an unresponsive apiserver. Without this a hung control
	// plane hangs the command indefinitely, since nothing else sets a deadline.
	cfg.Timeout = f.RequestTimeout
	return kubernetes.NewForConfig(cfg)
}

// CurrentNamespace returns the namespace of the active kubeconfig context — the
// "shell" namespace as set by kubens/kubectx — defaulting to "default" when the
// context pins none.
func CurrentNamespace(f Flags) (string, error) {
	ns, _, err := clientConfig(f).Namespace()
	return ns, err
}
