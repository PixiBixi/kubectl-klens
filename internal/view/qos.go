package view

import (
	"cmp"
	"context"
	"io"
	"slices"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/PixiBixi/kubectl-klens/internal/kube"
)

// Qos reports each pod's QoS class with its effective requests and limits and an
// eviction-risk verdict. The class alone is on every pod's status; what it does
// not say is that a Burstable pod with no memory request is ranked with the
// BestEffort crowd when the kubelet starts evicting, which is the finding this
// view exists for. Rows default to VERDICT (risk) order, riskiest at the bottom.
func Qos(ctx context.Context, c kube.Clients, f kube.Flags, args []string, out io.Writer) error {
	pods, err := kube.ListPods(ctx, c, f.Scope(), metav1.ListOptions{})
	if err != nil {
		return err
	}
	paint := kube.NewPainter(f)

	type entry struct {
		pod          *corev1.Pod
		class        corev1.PodQOSClass
		req, lim     corev1.ResourceList
		verdict, sev string
	}
	list := make([]entry, 0, len(pods))
	for i := range pods {
		p := &pods[i]
		if skipNamespace(f, p.Namespace) {
			continue
		}
		class := qosClass(p)
		req, lim := podResources(p)
		v, sev := qosVerdict(class, req[corev1.ResourceMemory])
		list = append(list, entry{p, class, req, lim, v, sev})
	}
	// Deterministic tiebreak for rows sharing a verdict; the VERDICT sort applied
	// at Flush is stable, so this order survives within each verdict.
	slices.SortStableFunc(list, func(a, b entry) int {
		return cmp.Or(
			cmp.Compare(a.pod.Namespace, b.pod.Namespace),
			cmp.Compare(a.pod.Name, b.pod.Name),
		)
	})

	t := kube.NewTable(out, paint, "NS", "POD", "QOS", "REQ_CPU", "LIM_CPU", "REQ_MEM", "LIM_MEM", "VERDICT")
	for i := range list {
		e := &list[i]
		t.Row(
			e.pod.Namespace,
			e.pod.Name,
			string(e.class),
			qtyOrNone(paint, e.req, corev1.ResourceCPU),
			qtyOrNone(paint, e.lim, corev1.ResourceCPU),
			qtyOrNone(paint, e.req, corev1.ResourceMemory),
			qtyOrNone(paint, e.lim, corev1.ResourceMemory),
			sevPaint(paint, e.sev)(e.verdict),
		)
	}
	t.SortRank("VERDICT", verdictRank("EVICT-FIRST", "NO-MEM-FLOOR", "BURSTABLE", "GUARANTEED"))
	t.SortBy(orDefault(f.Sort, "verdict"))
	return t.Flush()
}

// qosVerdict classifies eviction risk. NO-MEM-FLOOR is the one that is not
// visible in the class: the kubelet ranks pods by memory usage over request, so
// a Burstable pod that requested no memory is evicted alongside BestEffort ones
// however modest its actual footprint.
func qosVerdict(class corev1.PodQOSClass, memReq resource.Quantity) (verdict, sev string) {
	switch class {
	case corev1.PodQOSGuaranteed:
		return "GUARANTEED", "ok"
	case corev1.PodQOSBestEffort:
		return "EVICT-FIRST", "bad"
	default:
		if memReq.IsZero() {
			return "NO-MEM-FLOOR", "bad"
		}
		return "BURSTABLE", "warn"
	}
}

// qosClass returns the class the apiserver assigned, falling back to deriving it
// so the column is never blank on an object that never went through admission
// (a static manifest, a fake in tests).
func qosClass(p *corev1.Pod) corev1.PodQOSClass {
	if p.Status.QOSClass != "" {
		return p.Status.QOSClass
	}
	guaranteed, anySet := true, false
	for _, containers := range [][]corev1.Container{p.Spec.InitContainers, p.Spec.Containers} {
		for i := range containers {
			r := containers[i].Resources
			for _, name := range []corev1.ResourceName{corev1.ResourceCPU, corev1.ResourceMemory} {
				rq, hasReq := r.Requests[name]
				lq, hasLim := r.Limits[name]
				anySet = anySet || hasReq || hasLim
				// Guaranteed demands both set and equal on both resources. A
				// limit-only container qualifies on a real cluster because the
				// defaulter copies the limit into the request, which is exactly
				// the case this fallback cannot see - hence preferring status.
				if !hasReq || !hasLim || rq.Cmp(lq) != 0 {
					guaranteed = false
				}
			}
		}
	}
	switch {
	case !anySet:
		return corev1.PodQOSBestEffort
	case guaranteed:
		return corev1.PodQOSGuaranteed
	default:
		return corev1.PodQOSBurstable
	}
}

// podResources totals what the pod reserves in steady state: app containers plus
// native sidecars (init containers with restartPolicy Always), which are the
// ones still running once the pod is up. Plain init containers are left out;
// they can spike higher but only before any app container starts.
func podResources(p *corev1.Pod) (req, lim corev1.ResourceList) {
	req, lim = corev1.ResourceList{}, corev1.ResourceList{}
	add := func(c *corev1.Container) {
		addQuantities(req, c.Resources.Requests)
		addQuantities(lim, c.Resources.Limits)
	}
	for i := range p.Spec.Containers {
		add(&p.Spec.Containers[i])
	}
	for i := range p.Spec.InitContainers {
		ic := &p.Spec.InitContainers[i]
		if ic.RestartPolicy != nil && *ic.RestartPolicy == corev1.ContainerRestartPolicyAlways {
			add(ic)
		}
	}
	return req, lim
}

// addQuantities accumulates src into dst for cpu and memory only: those are the
// two the QoS class and the eviction ranking are computed from.
func addQuantities(dst, src corev1.ResourceList) {
	for _, name := range []corev1.ResourceName{corev1.ResourceCPU, corev1.ResourceMemory} {
		q, ok := src[name]
		if !ok {
			continue
		}
		if cur, ok := dst[name]; ok {
			cur.Add(q)
			dst[name] = cur
			continue
		}
		dst[name] = q.DeepCopy()
	}
}
