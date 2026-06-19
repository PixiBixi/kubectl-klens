package view

import (
	"context"
	"io"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/PixiBixi/kubectl-klens/internal/kube"
)

// Images lists every container image per pod, one row per container.
func Images(ctx context.Context, c kubernetes.Interface, f kube.Flags, args []string, out io.Writer) error {
	pods, err := c.CoreV1().Pods(f.NamespaceScope()).List(ctx, metav1.ListOptions{})
	if err != nil {
		return err
	}
	paint := kube.NewPainter(f)
	t := kube.NewTable(out, paint, "PODNAME", "CONTAINER", "PULL", "IMAGE", "TAG")
	for _, p := range pods.Items {
		for _, ctr := range p.Spec.Containers {
			image, tag := splitImageTag(ctr.Image)
			t.Row(p.Name, ctr.Name, string(ctr.ImagePullPolicy), image, latestTag(paint, tag))
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
