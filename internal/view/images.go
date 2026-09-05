package view

import (
	"context"
	"io"
	"slices"
	"strings"

	"github.com/PixiBixi/kubectl-klens/internal/kube"
)

// Images lists every container image per pod, one row per container, including
// init and ephemeral containers - their images are pulled and run on the node
// like any other, so an unpatched init image has to be visible here. --by-owner
// reads the workload specs instead of the running pods; see Reqlim's doc
// comment. Note this view is not namespace-scoped by default (unlike the other
// five --by-owner views), so kube-system is never excluded here.
func Images(ctx context.Context, c kube.Clients, f kube.Flags, args []string, out io.Writer) error {
	pods, err := podsForView(ctx, c, f)
	if err != nil {
		return err
	}
	paint := kube.NewPainter(f)
	t := kube.NewTable(out, paint, slices.Concat(
		[]string{podColumn(f, "PODNAME")}, ownerHeaders(f),
		[]string{"CONTAINER", "KIND", "PULL", "IMAGE", "TAG"},
	)...)
	// Reused row buffer; see Reqlim for why this is not a slices.Concat.
	row := make([]string, 0, 7)
	for i := range pods {
		p := &pods[i]
		for _, pc := range podContainers(p) {
			image, tag := splitImageTag(pc.Spec.Image)
			row = append(row[:0], p.Name)
			row = appendOwnerCells(row, paint, f, p)
			row = append(row, pc.Spec.Name, pc.Kind, string(pc.Spec.ImagePullPolicy), image, latestTag(paint, tag))
			t.Row(row...)
		}
	}
	t.SortBy(podSort(f, f.Sort, "PODNAME"))
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
	if repo, digest, ok := strings.CutLast(ref, "@"); ok {
		return repo, digest
	}
	// A colon after the last "/" is a tag; before it, it is a registry port.
	if repo, last, ok := strings.CutLast(ref, ":"); ok && !strings.Contains(last, "/") {
		return repo, last
	}
	return ref, "latest"
}
