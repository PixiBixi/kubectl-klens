package view

import (
	"context"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/PixiBixi/kubectl-klens/internal/kube"
)

type imageCount struct {
	registry, repo, tag string
	n                   int
}

// ImageCount counts container image occurrences across pods, splitting each
// reference into registry, repository, and tag. The --sort column selects the
// primary order (count desc by default, the others ascending).
func ImageCount(ctx context.Context, c kubernetes.Interface, f kube.Flags, args []string, out io.Writer) error {
	less, err := imageCountLess(f.Sort)
	if err != nil {
		return err
	}
	pods, err := c.CoreV1().Pods(f.NamespaceScope()).List(ctx, metav1.ListOptions{})
	if err != nil {
		return err
	}
	type key struct{ registry, repo, tag string }
	counts := map[key]int{}
	for _, p := range pods.Items {
		for _, ctr := range p.Spec.Containers {
			registry, repo, tag := parseImageRef(ctr.Image)
			counts[key{registry, repo, tag}]++
		}
	}
	list := make([]imageCount, 0, len(counts))
	for k, n := range counts {
		list = append(list, imageCount{k.registry, k.repo, k.tag, n})
	}
	sort.Slice(list, func(i, j int) bool { return less(list[i], list[j]) })
	paint := kube.NewPainter(f)
	t := kube.NewTable(out, paint, "COUNT", "REGISTRY", "IMAGE", "TAG")
	for _, e := range list {
		t.Row(strconv.Itoa(e.n), e.registry, e.repo, latestTag(paint, e.tag))
	}
	return t.Flush()
}

// imageCountLess returns the comparison for the given --sort column. Empty
// defaults to "count". Every order falls back to count desc then the remaining
// columns so output stays deterministic.
func imageCountLess(column string) (func(a, b imageCount) bool, error) {
	byCount := func(a, b imageCount) bool {
		if a.n != b.n {
			return a.n > b.n
		}
		if a.registry != b.registry {
			return a.registry < b.registry
		}
		if a.repo != b.repo {
			return a.repo < b.repo
		}
		return a.tag < b.tag
	}
	switch column {
	case "", "count":
		return byCount, nil
	case "registry":
		return field(func(e imageCount) string { return e.registry }, byCount), nil
	case "image":
		return field(func(e imageCount) string { return e.repo }, byCount), nil
	case "tag":
		return field(func(e imageCount) string { return e.tag }, byCount), nil
	default:
		return nil, fmt.Errorf("invalid --sort %q (want count|registry|image|tag)", column)
	}
}

// field orders ascending by the extracted string, falling back to tie when equal.
func field(get func(imageCount) string, tie func(a, b imageCount) bool) func(a, b imageCount) bool {
	return func(a, b imageCount) bool {
		if get(a) != get(b) {
			return get(a) < get(b)
		}
		return tie(a, b)
	}
}

// parseImageRef splits an image reference into registry, repository, and
// tag/digest. The leading component is treated as a registry only when it looks
// like a host (contains '.' or ':' or equals "localhost"); otherwise the
// registry defaults to docker.io, matching Docker's own resolution rules.
func parseImageRef(ref string) (registry, repo, tag string) {
	name, tag := splitImageTag(ref)
	registry, repo = "docker.io", name
	if slash := strings.Index(name, "/"); slash >= 0 {
		first := name[:slash]
		if strings.ContainsAny(first, ".:") || first == "localhost" {
			registry, repo = first, name[slash+1:]
		}
	}
	return registry, repo, tag
}
