package view

import (
	"cmp"
	"context"
	"io"
	"slices"
	"strconv"
	"strings"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/PixiBixi/kubectl-klens/internal/kube"
)

// UnusedConfig lists ConfigMaps and Secrets nothing references: leftovers from a
// migration, a renamed key, a chart that moved on. Every object is billed no
// space to speak of, but each one is read by anybody auditing the namespace, and
// a stale secret is a credential nobody rotates.
//
// The reference scan is the whole value of this command, so it reads pod
// templates as well as live pods (a CronJob between runs and a workload scaled to
// zero have no pods, and calling their config unused would be wrong), it counts a
// name cited in a container's args or command (the `--configmap=ns/name` flag
// several ingress controllers take), and it ignores what the platform manages:
// the kube-root-ca.crt ConfigMap, service-account tokens, Helm release history,
// and the objects a sidecar consumes by label selector rather than by name.
//
// An object a controller created keeps its OWNER, because that is the object to
// act on: deleting a Secret an ExternalSecret owns just has it recreated a minute
// later. Rows are biggest first.
func UnusedConfig(ctx context.Context, c kube.Clients, f kube.Flags, args []string, out io.Writer) error {
	var (
		cms      []corev1.ConfigMap
		secrets  []corev1.Secret
		pods     []corev1.Pod
		deploys  []appsv1.Deployment
		stateful []appsv1.StatefulSet
		daemons  []appsv1.DaemonSet
		jobs     []batchv1.Job
		crons    []batchv1.CronJob
		sas      []corev1.ServiceAccount
		ings     []networkingv1.Ingress
	)
	scope := f.Scope()
	opts := metav1.ListOptions{}
	err := allLists(
		func() (err error) { cms, err = kube.ListConfigMaps(ctx, c, scope, opts); return err },
		func() (err error) { secrets, err = kube.ListSecrets(ctx, c, scope, opts); return err },
		func() (err error) { pods, err = kube.ListPods(ctx, c, scope, opts); return err },
		func() (err error) { deploys, err = kube.ListDeployments(ctx, c, scope, opts); return err },
		func() (err error) { stateful, err = kube.ListStatefulSets(ctx, c, scope, opts); return err },
		func() (err error) { daemons, err = kube.ListDaemonSets(ctx, c, scope, opts); return err },
		func() (err error) { jobs, err = kube.ListJobs(ctx, c, scope, opts); return err },
		func() (err error) { crons, err = kube.ListCronJobs(ctx, c, scope, opts); return err },
		func() (err error) { sas, err = kube.ListServiceAccounts(ctx, c, scope, opts); return err },
		func() (err error) { ings, err = kube.ListIngresses(ctx, c, scope, opts); return err },
	)
	if err != nil {
		return err
	}

	used := newRefSet()
	for i := range pods {
		used.addPodSpec(pods[i].Namespace, &pods[i].Spec)
	}
	for i := range deploys {
		used.addPodSpec(deploys[i].Namespace, &deploys[i].Spec.Template.Spec)
	}
	for i := range stateful {
		used.addPodSpec(stateful[i].Namespace, &stateful[i].Spec.Template.Spec)
	}
	for i := range daemons {
		used.addPodSpec(daemons[i].Namespace, &daemons[i].Spec.Template.Spec)
	}
	for i := range jobs {
		used.addPodSpec(jobs[i].Namespace, &jobs[i].Spec.Template.Spec)
	}
	for i := range crons {
		used.addPodSpec(crons[i].Namespace, &crons[i].Spec.JobTemplate.Spec.Template.Spec)
	}
	for i := range sas {
		used.addServiceAccount(&sas[i])
	}
	for i := range ings {
		used.addIngress(&ings[i])
	}
	paint := kube.NewPainter(f)

	type entry struct {
		ns, kind, name, typ string
		owner               string
		size                int
		created             metav1.Time
	}
	var list []entry
	for i := range cms {
		cm := &cms[i]
		if skipNamespace(f, cm.Namespace) || platformConfigMap(cm) || used.has(refConfigMap, cm.Namespace, cm.Name) {
			continue
		}
		list = append(list, entry{
			ns: cm.Namespace, kind: "ConfigMap", name: cm.Name, typ: paint.Muted("-"),
			owner: ownerCell(paint, cm.Name, cm.OwnerReferences),
			size:  configMapSize(cm), created: cm.CreationTimestamp,
		})
	}
	for i := range secrets {
		s := &secrets[i]
		if skipNamespace(f, s.Namespace) || platformSecret(s) || used.has(refSecret, s.Namespace, s.Name) {
			continue
		}
		list = append(list, entry{
			ns: s.Namespace, kind: "Secret", name: s.Name, typ: secretTypeCell(s.Type),
			owner: ownerCell(paint, s.Name, s.OwnerReferences),
			size:  secretSize(s), created: s.CreationTimestamp,
		})
	}
	// Biggest first: the size is what makes one leftover worth deleting before
	// another. Ties fall back to a stable ns/kind/name order.
	slices.SortStableFunc(list, func(a, b entry) int {
		return cmp.Or(
			cmp.Compare(b.size, a.size),
			cmp.Compare(a.ns, b.ns),
			cmp.Compare(a.kind, b.kind),
			cmp.Compare(a.name, b.name),
		)
	})

	t := kube.NewTable(out, paint, "NS", "KIND", "NAME", "TYPE", "OWNER", "SIZE", "AGE")
	for i := range list {
		e := &list[i]
		t.Row(e.ns, e.kind, e.name, e.typ, e.owner, humanBytes(e.size), age(e.created))
	}
	t.SortBy(f.Sort)
	return t.Flush()
}

// refKind distinguishes the two namespaces of names a reference can land in.
type refKind string

const (
	refConfigMap refKind = "cm"
	refSecret    refKind = "secret"
)

// refSet collects every ConfigMap and Secret name something in the cluster
// points at, keyed by kind and namespace. args holds the names cited in a
// container's args or command, which cannot be attributed to a kind.
type refSet struct {
	seen map[string]bool
	args map[string]bool
}

func newRefSet() refSet {
	return refSet{seen: map[string]bool{}, args: map[string]bool{}}
}

func (r refSet) add(kind refKind, ns, name string) {
	if name != "" {
		r.seen[string(kind)+"|"+ns+"/"+name] = true
	}
}

func (r refSet) has(kind refKind, ns, name string) bool {
	return r.seen[string(kind)+"|"+ns+"/"+name] || r.args[ns+"|"+name]
}

// addPodSpec collects every reference a pod spec can carry: volumes (including
// the projected sources that mount several at once), the env of every container
// kind, and the image-pull secrets.
func (r refSet) addPodSpec(ns string, spec *corev1.PodSpec) {
	for i := range spec.Volumes {
		r.addVolume(ns, &spec.Volumes[i])
	}
	for i := range spec.ImagePullSecrets {
		r.add(refSecret, ns, spec.ImagePullSecrets[i].Name)
	}
	for _, c := range podSpecContainers(spec) {
		r.addContainer(ns, c)
		r.addArgs(ns, c)
	}
}

// nameRune reports whether c can appear in a Kubernetes object name, which is
// what splits an argument into candidate names: `--configmap=ns/name` yields
// "ns" and "name", so the flag several ingress controllers take counts as a
// reference to the ConfigMap it points at.
func nameRune(c rune) bool {
	return c == '-' || c == '.' || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9')
}

// addArgs indexes the whole tokens of a container's command line. Whole tokens,
// not substrings: an object named "operator" would otherwise be considered
// referenced by any argument that merely contains the word, hiding a real
// leftover. The reverse mistake is the cheaper one here - a name that happens to
// appear on a command line is left off the list rather than proposed for
// deletion - which is the right way round for a command that says what to
// delete.
func (r refSet) addArgs(ns string, c *corev1.Container) {
	for _, list := range [][]string{c.Command, c.Args} {
		for _, arg := range list {
			for _, token := range strings.FieldsFunc(arg, func(c rune) bool { return !nameRune(c) }) {
				r.args[ns+"|"+token] = true
			}
		}
	}
}

func (r refSet) addVolume(ns string, v *corev1.Volume) {
	if v.ConfigMap != nil {
		r.add(refConfigMap, ns, v.ConfigMap.Name)
	}
	if v.Secret != nil {
		r.add(refSecret, ns, v.Secret.SecretName)
	}
	// A CSI driver takes its credentials from a secret in the pod's namespace.
	if v.CSI != nil && v.CSI.NodePublishSecretRef != nil {
		r.add(refSecret, ns, v.CSI.NodePublishSecretRef.Name)
	}
	if v.Projected == nil {
		return
	}
	for i := range v.Projected.Sources {
		src := &v.Projected.Sources[i]
		if src.ConfigMap != nil {
			r.add(refConfigMap, ns, src.ConfigMap.Name)
		}
		if src.Secret != nil {
			r.add(refSecret, ns, src.Secret.Name)
		}
	}
}

func (r refSet) addContainer(ns string, c *corev1.Container) {
	for i := range c.EnvFrom {
		src := &c.EnvFrom[i]
		if src.ConfigMapRef != nil {
			r.add(refConfigMap, ns, src.ConfigMapRef.Name)
		}
		if src.SecretRef != nil {
			r.add(refSecret, ns, src.SecretRef.Name)
		}
	}
	for i := range c.Env {
		from := c.Env[i].ValueFrom
		if from == nil {
			continue
		}
		if from.ConfigMapKeyRef != nil {
			r.add(refConfigMap, ns, from.ConfigMapKeyRef.Name)
		}
		if from.SecretKeyRef != nil {
			r.add(refSecret, ns, from.SecretKeyRef.Name)
		}
	}
}

// addServiceAccount collects the secrets an SA holds: a token it names stays in
// use as long as the SA exists, and so do its pull secrets.
func (r refSet) addServiceAccount(sa *corev1.ServiceAccount) {
	for i := range sa.Secrets {
		r.add(refSecret, sa.Namespace, sa.Secrets[i].Name)
	}
	for i := range sa.ImagePullSecrets {
		r.add(refSecret, sa.Namespace, sa.ImagePullSecrets[i].Name)
	}
}

// addIngress collects TLS certificates, which no pod mounts but the ingress
// controller reads.
func (r refSet) addIngress(ing *networkingv1.Ingress) {
	for i := range ing.Spec.TLS {
		r.add(refSecret, ing.Namespace, ing.Spec.TLS[i].SecretName)
	}
}

// podSpecContainers enumerates a spec's containers in startup order, the same
// init/app/ephemeral coverage podContainers gives for a live pod.
func podSpecContainers(spec *corev1.PodSpec) []*corev1.Container {
	out := make([]*corev1.Container, 0, len(spec.InitContainers)+len(spec.Containers)+len(spec.EphemeralContainers))
	for i := range spec.InitContainers {
		out = append(out, &spec.InitContainers[i])
	}
	for i := range spec.Containers {
		out = append(out, &spec.Containers[i])
	}
	for i := range spec.EphemeralContainers {
		out = append(out, (*corev1.Container)(&spec.EphemeralContainers[i].EphemeralContainerCommon))
	}
	return out
}

// rootCAConfigMap is created by the apiserver in every namespace for pods to
// verify it. Reporting it would be one guaranteed false positive per namespace.
const rootCAConfigMap = "kube-root-ca.crt"

func platformConfigMap(cm *corev1.ConfigMap) bool {
	return cm.Name == rootCAConfigMap || sidecarSelected(cm.Labels)
}

// sidecarLabels are the label keys a sidecar selects on to load an object it
// never mounts. The Grafana sidecar convention is the one that matters in
// practice: every kube-prometheus-stack ships dozens of dashboard ConfigMaps
// that no pod references by name, and reporting them buries everything else.
var sidecarLabels = []string{
	"grafana_dashboard",
	"grafana_datasource",
	"grafana_alert",
	"grafana_folder",
}

func sidecarSelected(labels map[string]string) bool {
	for _, k := range sidecarLabels {
		if _, ok := labels[k]; ok {
			return true
		}
	}
	return false
}

// ownerCell names the controller that created the object, which is what has to
// be deleted for the object to stay gone. The owner's name is dropped when it
// matches the object's, which is the common case and pure width otherwise.
func ownerCell(paint kube.Painter, name string, refs []metav1.OwnerReference) string {
	if len(refs) == 0 {
		return paint.Muted("-")
	}
	if refs[0].Name == name {
		return refs[0].Kind
	}
	return refs[0].Kind + "/" + refs[0].Name
}

// secretTypeCell drops the kubernetes.io/ prefix every built-in secret type
// carries: 14 columns that say nothing, on a table already wide enough to wrap.
func secretTypeCell(t corev1.SecretType) string {
	if short, ok := strings.CutPrefix(string(t), "kubernetes.io/"); ok {
		return short
	}
	return string(t)
}

// platformSecret reports the secrets the platform owns: a service-account token
// belongs to its SA, and a Helm release record is the history of a chart, not
// configuration anything mounts.
func platformSecret(s *corev1.Secret) bool {
	switch s.Type {
	case corev1.SecretTypeServiceAccountToken, "helm.sh/release.v1":
		return true
	}
	return sidecarSelected(s.Labels)
}

func configMapSize(cm *corev1.ConfigMap) int {
	n := 0
	for _, v := range cm.Data {
		n += len(v)
	}
	for _, v := range cm.BinaryData {
		n += len(v)
	}
	return n
}

func secretSize(s *corev1.Secret) int {
	n := 0
	for _, v := range s.Data {
		n += len(v)
	}
	for _, v := range s.StringData {
		n += len(v)
	}
	return n
}

// humanBytes renders a size the way a reader scans it, in the binary units
// kubectl uses everywhere else.
func humanBytes(n int) string {
	const unit = 1024
	if n < unit {
		return strconv.Itoa(n) + "B"
	}
	value, units := float64(n), []string{"KiB", "MiB", "GiB"}
	for _, u := range units {
		value /= unit
		if value < unit {
			return strconv.FormatFloat(value, 'f', 1, 64) + u
		}
	}
	return strconv.FormatFloat(value, 'f', 1, 64) + "GiB"
}
