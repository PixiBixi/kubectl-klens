package view

import (
	"bytes"
	"context"
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/PixiBixi/kubectl-klens/internal/kube"
)

func cmWith(name, ns string, data map[string]string) *corev1.ConfigMap {
	return &corev1.ConfigMap{Name: name, Namespace: ns, Data: data}
}

func secretWith(name, ns string, typ corev1.SecretType, data map[string][]byte) *corev1.Secret {
	return &corev1.Secret{Name: name, Namespace: ns, Type: typ, Data: data}
}

func TestUnusedConfig(t *testing.T) {
	envPod := &corev1.Pod{
		Name: "web", Namespace: "app",
		Spec: corev1.PodSpec{
			Volumes: []corev1.Volume{{
				Name:      "conf",
				ConfigMap: &corev1.ConfigMapVolumeSource{Name: "mounted-cm"},
			}},
			ImagePullSecrets: []corev1.LocalObjectReference{{Name: "registry"}},
			Containers: []corev1.Container{{
				Name: "app",
				EnvFrom: []corev1.EnvFromSource{{
					SecretRef: &corev1.SecretEnvSource{Name: "envfrom-secret"},
				}},
				Env: []corev1.EnvVar{{
					Name: "K",
					ValueFrom: &corev1.EnvVarSource{ConfigMapKeyRef: &corev1.ConfigMapKeySelector{
						Name: "keyref-cm",
					}},
				}},
			}},
		},
	}
	c := fake.NewClientset(
		envPod,
		cmWith("mounted-cm", "app", map[string]string{"a": "1"}),
		cmWith("keyref-cm", "app", map[string]string{"a": "1"}),
		cmWith("leftover", "app", map[string]string{"big": strings.Repeat("x", 2048)}),
		cmWith(rootCAConfigMap, "app", map[string]string{"ca.crt": "..."}),
		secretWith("registry", "app", corev1.SecretTypeDockerConfigJson, nil),
		secretWith("envfrom-secret", "app", corev1.SecretTypeOpaque, nil),
		secretWith("stale-creds", "app", corev1.SecretTypeOpaque, map[string][]byte{"password": []byte("hunter2")}),
		secretWith("sa-token", "app", corev1.SecretTypeServiceAccountToken, map[string][]byte{"token": []byte("x")}),
		secretWith("sh.helm.release.v1.app.v1", "app", "helm.sh/release.v1", map[string][]byte{"release": []byte("x")}),
	)
	var buf bytes.Buffer
	if err := UnusedConfig(context.Background(), clients(c), kube.Flags{}, nil, &buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{"NS", "KIND", "NAME", "TYPE", "KEYS", "SIZE", "AGE", "leftover", "2.0KiB", "stale-creds"} {
		if !strings.Contains(out, want) {
			t.Fatalf("output missing %q:\n%s", want, out)
		}
	}
	for _, unwanted := range []string{"mounted-cm", "keyref-cm", "registry", "envfrom-secret", rootCAConfigMap, "sa-token", "sh.helm.release"} {
		if strings.Contains(out, unwanted) {
			t.Fatalf("%q is referenced or platform-owned and must not be listed:\n%s", unwanted, out)
		}
	}
	// Biggest first, so the leftover worth deleting is the top row.
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if !strings.Contains(lines[1], "leftover") {
		t.Fatalf("want the biggest object first, got %q", lines[1])
	}
}

// TestUnusedConfigReadsPodTemplates is the false-positive guard: a CronJob
// between runs and a workload scaled to zero have no pods, and their config is
// still very much in use.
func TestUnusedConfigReadsPodTemplates(t *testing.T) {
	specWith := func(cm string) corev1.PodSpec {
		return corev1.PodSpec{Volumes: []corev1.Volume{{
			Name:      "conf",
			ConfigMap: &corev1.ConfigMapVolumeSource{Name: cm},
		}}}
	}
	zero := int32(0)
	c := fake.NewClientset(
		cmWith("cron-cm", "app", map[string]string{"a": "1"}),
		cmWith("deploy-cm", "app", map[string]string{"a": "1"}),
		cmWith("sts-cm", "app", map[string]string{"a": "1"}),
		cmWith("ds-cm", "app", map[string]string{"a": "1"}),
		cmWith("job-cm", "app", map[string]string{"a": "1"}),
		&batchv1.CronJob{Name: "nightly", Namespace: "app", Spec: batchv1.CronJobSpec{
			JobTemplate: batchv1.JobTemplateSpec{Spec: batchv1.JobSpec{
				Template: corev1.PodTemplateSpec{Spec: specWith("cron-cm")},
			}},
		}},
		&appsv1.Deployment{Name: "web", Namespace: "app", Spec: appsv1.DeploymentSpec{
			Replicas: &zero,
			Template: corev1.PodTemplateSpec{Spec: specWith("deploy-cm")},
		}},
		&appsv1.StatefulSet{Name: "db", Namespace: "app", Spec: appsv1.StatefulSetSpec{
			Template: corev1.PodTemplateSpec{Spec: specWith("sts-cm")},
		}},
		&appsv1.DaemonSet{Name: "agent", Namespace: "app", Spec: appsv1.DaemonSetSpec{
			Template: corev1.PodTemplateSpec{Spec: specWith("ds-cm")},
		}},
		&batchv1.Job{Name: "once", Namespace: "app", Spec: batchv1.JobSpec{
			Template: corev1.PodTemplateSpec{Spec: specWith("job-cm")},
		}},
	)
	var buf bytes.Buffer
	if err := UnusedConfig(context.Background(), clients(c), kube.Flags{}, nil, &buf); err != nil {
		t.Fatal(err)
	}
	for _, cm := range []string{"cron-cm", "deploy-cm", "sts-cm", "ds-cm", "job-cm"} {
		if strings.Contains(buf.String(), cm) {
			t.Fatalf("%s is referenced by a pod template:\n%s", cm, buf.String())
		}
	}
}

// TestUnusedConfigCountsIngressAndServiceAccount covers the two referrers that
// are not pods: a TLS certificate the ingress controller reads, and the secrets
// a service account holds.
func TestUnusedConfigCountsIngressAndServiceAccount(t *testing.T) {
	c := fake.NewClientset(
		secretWith("web-tls", "app", corev1.SecretTypeTLS, map[string][]byte{"tls.crt": []byte("x")}),
		secretWith("sa-pull", "app", corev1.SecretTypeDockerConfigJson, nil),
		secretWith("sa-extra", "app", corev1.SecretTypeOpaque, nil),
		&networkingv1.Ingress{Name: "web", Namespace: "app", Spec: networkingv1.IngressSpec{
			TLS: []networkingv1.IngressTLS{{Hosts: []string{"app.example.com"}, SecretName: "web-tls"}},
		}},
		&corev1.ServiceAccount{
			Name: "runner", Namespace: "app",
			Secrets:          []corev1.ObjectReference{{Name: "sa-extra"}},
			ImagePullSecrets: []corev1.LocalObjectReference{{Name: "sa-pull"}},
		},
	)
	var buf bytes.Buffer
	if err := UnusedConfig(context.Background(), clients(c), kube.Flags{}, nil, &buf); err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{"web-tls", "sa-pull", "sa-extra"} {
		if strings.Contains(buf.String(), s) {
			t.Fatalf("%s is referenced:\n%s", s, buf.String())
		}
	}
}

// TestUnusedConfigProjectedVolume covers a projected volume, which mounts
// several sources at once and is easy to miss when collecting references.
func TestUnusedConfigProjectedVolume(t *testing.T) {
	pod := &corev1.Pod{
		Name: "web", Namespace: "app",
		Spec: corev1.PodSpec{Volumes: []corev1.Volume{{
			Name: "all",
			Projected: &corev1.ProjectedVolumeSource{Sources: []corev1.VolumeProjection{
				{ConfigMap: &corev1.ConfigMapProjection{Name: "proj-cm"}},
				{Secret: &corev1.SecretProjection{Name: "proj-secret"}},
			}},
		}}},
	}
	c := fake.NewClientset(pod,
		cmWith("proj-cm", "app", map[string]string{"a": "1"}),
		secretWith("proj-secret", "app", corev1.SecretTypeOpaque, nil),
	)
	var buf bytes.Buffer
	if err := UnusedConfig(context.Background(), clients(c), kube.Flags{}, nil, &buf); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(buf.String(), "proj-") {
		t.Fatalf("projected sources are references too:\n%s", buf.String())
	}
}

func TestUnusedConfigExcludesKubeSystemClusterWide(t *testing.T) {
	c := fake.NewClientset(
		cmWith("orphan", "kube-system", map[string]string{"a": "1"}),
		cmWith("orphan", "app", map[string]string{"a": "1"}),
	)
	var buf bytes.Buffer
	if err := UnusedConfig(context.Background(), clients(c), kube.Flags{}, nil, &buf); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(buf.String(), "kube-system") {
		t.Fatalf("kube-system must be excluded cluster-wide:\n%s", buf.String())
	}
	if !strings.Contains(buf.String(), "app") {
		t.Fatalf("workload namespace missing:\n%s", buf.String())
	}
}
