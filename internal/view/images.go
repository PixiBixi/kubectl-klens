package view

import (
	"context"
	"io"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/PixiBixi/kubectl-klens/internal/kube"
)

// Images lists every container image per pod, one row per container, including
// init and ephemeral containers — their images are pulled and run on the node
// like any other, so an unpatched init image has to be visible here.
func Images(ctx context.Context, c kubernetes.Interface, f kube.Flags, args []string, out io.Writer) error {
	pods, err := kube.ListPods(ctx, c, f.NamespaceScope(), metav1.ListOptions{})
	if err != nil {
		return err
	}
	paint := kube.NewPainter(f)
	t := kube.NewTable(out, paint, "PODNAME", "CONTAINER", "KIND", "PULL", "IMAGE", "TAG")
	for i := range pods {
		p := &pods[i]
		for _, pc := range podContainers(p) {
			image, tag := splitImageTag(pc.Spec.Image)
			t.Row(p.Name, pc.Spec.Name, pc.Kind, string(pc.Spec.ImagePullPolicy), image, latestTag(paint, tag))
		}
	}
	t.SortBy(f.Sort)
	return t.Flush()
}

// latestTag highlights a floating "latest" tag, an operational anti-pattern.
func latestTag(paint kube.Painter, tag string) string {
	if tag == "latest" {
		return paint.Warn(tag)
	}
	return tag
}

// splitImageTag separates an image reference into its name (registry plus
// repository) and its tag or digest, defaulting to "latest" when neither is
// present. A digest takes precedence over a tag.
func splitImageTag(ref string) (name, tag string) {
	if at := strings.LastIndex(ref, "@"); at >= 0 {
		return ref[:at], ref[at+1:]
	}
	if colon := strings.LastIndex(ref, ":"); colon > strings.LastIndex(ref, "/") {
		return ref[:colon], ref[colon+1:]
	}
	return ref, "latest"
}
