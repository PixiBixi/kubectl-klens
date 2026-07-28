package view

import (
	"context"
	"fmt"
	"io"
	"strings"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/client-go/kubernetes"

	"github.com/PixiBixi/kubectl-klens/internal/kube"
)

// NodeIPs lists the internal and external addresses of every node — what a
// `-o jsonpath` over .status.addresses gives you, without the jsonpath. A node
// name narrows the listing to that node.
func NodeIPs(ctx context.Context, c kubernetes.Interface, f kube.Flags, args []string, out io.Writer) error {
	var opts metav1.ListOptions
	node := ""
	if len(args) > 0 && args[0] != "" {
		node = args[0]
		// Filtered by the apiserver, not here: metadata.name is indexed, so a
		// single-node lookup costs one object over the wire instead of the fleet.
		opts.FieldSelector = fields.OneTermEqualSelector("metadata.name", node).String()
	}
	nodes, err := kube.ListNodes(ctx, c, opts)
	if err != nil {
		return err
	}
	// A named node that matched nothing is a typo or a node that has left the
	// cluster; saying so beats printing a bare header the caller has to squint at.
	if node != "" && len(nodes) == 0 {
		return fmt.Errorf("node %q not found", node)
	}
	paint := kube.NewPainter(f)
	t := kube.NewTable(out, paint, "NAME", "INTERNAL-IP", "EXTERNAL-IP")
	for i := range nodes {
		n := &nodes[i]
		t.Row(
			n.Name,
			// A node with no InternalIP is broken, not merely unexposed: that
			// address is how the control plane reaches its kubelet.
			addressCell(paint.OK, paint.Bad, nodeAddresses(n, corev1.NodeInternalIP)),
			// An absent ExternalIP is the desired state on private nodes, so it
			// is muted rather than flagged; a public address is warned on
			// instead, being reachable from the internet.
			addressCell(paint.Warn, paint.Muted, nodeAddresses(n, corev1.NodeExternalIP)),
		)
	}
	t.SortBy(f.Sort)
	return t.Flush()
}

// nodeAddresses joins every address of the given type with a comma. Dual-stack
// nodes report two of each (one IPv4, one IPv6), and dropping the second would
// hide half the node's reachable surface.
func nodeAddresses(n *corev1.Node, typ corev1.NodeAddressType) string {
	var addrs []string
	for _, a := range n.Status.Addresses {
		if a.Type == typ && a.Address != "" {
			addrs = append(addrs, a.Address)
		}
	}
	return strings.Join(addrs, ",")
}

// addressCell paints a present address with found and renders an absent one as
// a muted-style "<none>" through missing.
func addressCell(found, missing func(string) string, addrs string) string {
	if addrs == "" {
		return missing("<none>")
	}
	return found(addrs)
}
