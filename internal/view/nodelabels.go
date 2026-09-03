package view

import (
	"strings"

	"github.com/PixiBixi/kubectl-klens/internal/kube"
)

// nodePoolLabels lists, in priority order, the node-pool/group label key each
// platform emits. The first key present on the node wins.
var nodePoolLabels = []string{
	"cloud.google.com/gke-nodepool",  // GKE standard and Autopilot node pools
	"eks.amazonaws.com/nodegroup",    // EKS managed node groups
	"karpenter.sh/nodepool",          // Karpenter v1 NodePool (AWS, also used by AKS Node Auto Provisioning)
	"kubernetes.azure.com/agentpool", // AKS agent pools
}

// computeClassLabels lists the label(s) exposing a real compute/instance
// class distinct from the raw instance type or the pool name. Only GKE has a
// genuine equivalent (the Autopilot built-in classes and GKE custom
// ComputeClasses); AWS and Azure expose nothing comparable, so CLASS stays
// empty there rather than faking it from an instance family or pool name.
var computeClassLabels = []string{
	"cloud.google.com/compute-class",
}

// provisioningLabels maps each cloud's provisioning-model label to the klens
// vocabulary (spot, on-demand, preemptible, capacity-block, reserved), in the
// order they are checked. gke-provisioning only exists on GKE 1.25.5+ nodes,
// so the older gke-spot/gke-preemptible booleans are checked first.
var provisioningLabels = []struct {
	key    string
	values map[string]string
}{
	{"cloud.google.com/gke-spot", map[string]string{"true": "spot"}},
	{"cloud.google.com/gke-preemptible", map[string]string{"true": "preemptible"}},
	{"cloud.google.com/gke-provisioning", map[string]string{"spot": "spot", "preemptible": "preemptible"}},
	{"eks.amazonaws.com/capacityType", map[string]string{"ON_DEMAND": "on-demand", "SPOT": "spot", "CAPACITY_BLOCK": "capacity-block"}},
	{"karpenter.sh/capacity-type", map[string]string{"spot": "spot", "on-demand": "on-demand", "reserved": "reserved"}},
	{"kubernetes.azure.com/priority", map[string]string{"spot": "spot", "regular": "on-demand"}},
	{"kubernetes.azure.com/scalesetpriority", map[string]string{"spot": "spot"}},
}

// cloudLabelPrefixes identify which cloud a node belongs to even when no
// provisioning label matched: on-demand is usually the unlabeled default (AKS
// "regular" nodes carry no scalesetpriority label at all), so a node with
// other labels from a known cloud namespace is inferred on-demand rather than
// reported as unknown.
var cloudLabelPrefixes = []string{
	"cloud.google.com/",
	"eks.amazonaws.com/",
	"karpenter.sh/",
	"kubernetes.azure.com/",
}

// firstLabel returns the value of the first key present in keys, or a muted
// "<none>" when none match.
func firstLabel(paint kube.Painter, labels map[string]string, keys []string) string {
	for _, k := range keys {
		if v, ok := labels[k]; ok && v != "" {
			return v
		}
	}
	return paint.Muted("<none>")
}

// nodeProvisioning resolves the provisioning model from labels, normalized to
// one lowercase vocabulary across clouds. Returns "" when no cloud label at all
// is present: that case must render as a caller-drawn <none>, never a guessed
// "on-demand".
func nodeProvisioning(labels map[string]string) string {
	for _, cl := range provisioningLabels {
		if v, ok := labels[cl.key]; ok {
			if norm, matched := cl.values[v]; matched {
				return norm
			}
		}
	}
	for key := range labels {
		for _, prefix := range cloudLabelPrefixes {
			if strings.HasPrefix(key, prefix) {
				return "on-demand"
			}
		}
	}
	return ""
}

// nodeClass renders the CLASS cell for the views that report it (`nodes`,
// `node-ips`), so a new compute-class label key reaches every one of them at
// once instead of only the view it was added to.
func nodeClass(paint kube.Painter, labels map[string]string) string {
	return firstLabel(paint, labels, computeClassLabels)
}
