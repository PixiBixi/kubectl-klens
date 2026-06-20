package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/PixiBixi/kubectl-klens/internal/kube"
)

func testApp(out, errw *bytes.Buffer) App {
	return App{
		Info:      BuildInfo{Version: "test", Commit: "abc", Date: "today"},
		NewClient: func(kube.Flags) (kubernetes.Interface, error) { return fake.NewClientset(), nil },
		Namespace: func(kube.Flags) (string, error) { return "current-ns", nil },
		Out:       out,
		Err:       errw,
	}
}

// listedNamespace returns the namespace of the first pods "list" action the
// fake clientset recorded, or "<none>" if no such action happened.
func listedNamespace(c *fake.Clientset) string {
	for _, action := range c.Actions() {
		if action.GetVerb() == "list" && action.GetResource().Resource == "pods" {
			return action.GetNamespace()
		}
	}
	return "<none>"
}

func TestRunNoArgs(t *testing.T) {
	var out, errw bytes.Buffer
	if code := testApp(&out, &errw).Run(nil); code != 1 {
		t.Fatalf("want exit 1, got %d", code)
	}
	if !strings.Contains(errw.String(), "Usage:") {
		t.Fatalf("want usage, got %q", errw.String())
	}
}

func TestRunUnknown(t *testing.T) {
	var out, errw bytes.Buffer
	if code := testApp(&out, &errw).Run([]string{"bogus"}); code != 1 {
		t.Fatalf("want exit 1, got %d", code)
	}
	if !strings.Contains(errw.String(), "unknown subcommand") {
		t.Fatalf("want unknown subcommand, got %q", errw.String())
	}
}

func TestRunVersion(t *testing.T) {
	var out, errw bytes.Buffer
	if code := testApp(&out, &errw).Run([]string{"--version"}); code != 0 {
		t.Fatalf("want exit 0, got %d", code)
	}
	if !strings.Contains(out.String(), "test") {
		t.Fatalf("want version, got %q", out.String())
	}
}

func TestRunHelpListsAllCommands(t *testing.T) {
	var out, errw bytes.Buffer
	testApp(&out, &errw).Run([]string{"--help"})
	for _, c := range commands {
		if !strings.Contains(out.String(), c.Name) {
			t.Fatalf("help missing %q", c.Name)
		}
	}
}

func TestRunOnNodeMissingArg(t *testing.T) {
	var out, errw bytes.Buffer
	if code := testApp(&out, &errw).Run([]string{"on-node"}); code != 1 {
		t.Fatalf("want exit 1, got %d", code)
	}
	if !strings.Contains(errw.String(), "requires a node") {
		t.Fatalf("want node-required error, got %q", errw.String())
	}
}

func TestRunDispatchesNodes(t *testing.T) {
	var out, errw bytes.Buffer
	if code := testApp(&out, &errw).Run([]string{"nodes"}); code != 0 {
		t.Fatalf("want exit 0, got %d (err=%q)", code, errw.String())
	}
	if !strings.Contains(out.String(), "NODEPOOL") {
		t.Fatalf("want nodes header, got %q", out.String())
	}
}

// reqlimApp builds an App whose injected client and namespace resolver are
// observable: it records resolver calls and exposes the fake clientset.
func reqlimApp(out, errw *bytes.Buffer, resolved string) (App, *fake.Clientset, *bool) {
	c := fake.NewClientset()
	called := false
	return App{
		Info:      BuildInfo{Version: "test"},
		NewClient: func(kube.Flags) (kubernetes.Interface, error) { return c, nil },
		Namespace: func(kube.Flags) (string, error) { called = true; return resolved, nil },
		Out:       out,
		Err:       errw,
	}, c, &called
}

func TestRunReqlimDefaultsToCurrentNamespace(t *testing.T) {
	var out, errw bytes.Buffer
	app, c, called := reqlimApp(&out, &errw, "team-a")
	if code := app.Run([]string{"reqlim"}); code != 0 {
		t.Fatalf("want exit 0, got %d (err=%q)", code, errw.String())
	}
	if !*called {
		t.Fatal("resolver should be called when neither -n nor -A is set")
	}
	if got := listedNamespace(c); got != "team-a" {
		t.Fatalf("want list scoped to current namespace team-a, got %q", got)
	}
}

func TestRunReqlimAllNamespaces(t *testing.T) {
	var out, errw bytes.Buffer
	app, c, called := reqlimApp(&out, &errw, "team-a")
	if code := app.Run([]string{"reqlim", "-A"}); code != 0 {
		t.Fatalf("want exit 0, got %d (err=%q)", code, errw.String())
	}
	if *called {
		t.Fatal("resolver must not be called when -A is set")
	}
	if got := listedNamespace(c); got != "" {
		t.Fatalf("want list across all namespaces (empty), got %q", got)
	}
}

func TestRunReqlimExplicitNamespace(t *testing.T) {
	var out, errw bytes.Buffer
	app, c, called := reqlimApp(&out, &errw, "team-a")
	if code := app.Run([]string{"reqlim", "-n", "custom"}); code != 0 {
		t.Fatalf("want exit 0, got %d (err=%q)", code, errw.String())
	}
	if *called {
		t.Fatal("resolver must not be called when -n is set")
	}
	if got := listedNamespace(c); got != "custom" {
		t.Fatalf("want list scoped to custom, got %q", got)
	}
}

func TestRunPodCommandStaysClusterWide(t *testing.T) {
	// image-count has no CurrentNSDefault: it must keep listing all namespaces.
	var out, errw bytes.Buffer
	app, c, called := reqlimApp(&out, &errw, "team-a")
	if code := app.Run([]string{"image-count"}); code != 0 {
		t.Fatalf("want exit 0, got %d (err=%q)", code, errw.String())
	}
	if *called {
		t.Fatal("resolver must not be called for non-CurrentNSDefault commands")
	}
	if got := listedNamespace(c); got != "" {
		t.Fatalf("want image-count across all namespaces (empty), got %q", got)
	}
}

// TestSortColumnsMatchHeaders guards against a declared --sort column drifting
// from a command's actual table headers.
func TestSortColumnsMatchHeaders(t *testing.T) {
	for _, c := range commands {
		if len(c.SortColumns) == 0 || c.Name == "autoscaler" {
			// autoscaler needs a status ConfigMap and prints a summary line
			// before its table: see TestAutoscalerSortColumnsMatchHeaders.
			continue
		}
		var buf bytes.Buffer
		if err := c.Run(context.Background(), fake.NewClientset(), kube.Flags{}, []string{"dummy"}, &buf); err != nil {
			t.Fatalf("%s: run failed: %v", c.Name, err)
		}
		header := strings.Split(buf.String(), "\n")[0]
		assertSortColumnsInHeader(t, c.Name, c.SortColumns, header)
	}
}

// assertSortColumnsInHeader checks every declared sort column appears as a
// whitespace-separated token in the (case-insensitive) header line.
func assertSortColumnsInHeader(t *testing.T, name string, cols []string, header string) {
	t.Helper()
	got := map[string]bool{}
	for _, h := range strings.Fields(strings.ToLower(header)) {
		got[h] = true
	}
	for _, col := range cols {
		if !got[col] {
			t.Errorf("%s: sort column %q not a header (%q)", name, col, header)
		}
	}
}

func autoscalerStatusCM(status string) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "cluster-autoscaler-status", Namespace: "kube-system"},
		Data:       map[string]string{"status": status},
	}
}

// TestAutoscalerSortColumnsMatchHeaders is the autoscaler-specific counterpart
// to TestSortColumnsMatchHeaders: it backs the command with a status ConfigMap
// and locates the table header beneath the cluster-wide summary.
func TestAutoscalerSortColumnsMatchHeaders(t *testing.T) {
	cmd, ok := lookup("autoscaler")
	if !ok {
		t.Fatal("autoscaler command not found")
	}
	const status = `autoscalerStatus: Running
nodeGroups:
  - name: grp-a
    health:
      status: Healthy
      cloudProviderTarget: 1
      minSize: 0
      maxSize: 3
`
	var buf bytes.Buffer
	if err := cmd.Run(context.Background(), fake.NewClientset(autoscalerStatusCM(status)), kube.Flags{}, nil, &buf); err != nil {
		t.Fatalf("run failed: %v", err)
	}
	var header string
	for _, line := range strings.Split(buf.String(), "\n") {
		if strings.HasPrefix(line, "NODEGROUP") {
			header = line
			break
		}
	}
	if header == "" {
		t.Fatalf("no nodegroup table header in output:\n%s", buf.String())
	}
	assertSortColumnsInHeader(t, "autoscaler", cmd.SortColumns, header)
}

func TestRunRejectsInvalidSort(t *testing.T) {
	var out, errw bytes.Buffer
	if code := testApp(&out, &errw).Run([]string{"zones", "--sort", "bogus"}); code != 1 {
		t.Fatalf("want exit 1, got %d", code)
	}
	if !strings.Contains(errw.String(), "invalid --sort") {
		t.Fatalf("want invalid --sort error, got %q", errw.String())
	}
}

func TestRunRejectsSortOnNonSortable(t *testing.T) {
	var out, errw bytes.Buffer
	// secret declares no SortColumns, so --sort is an unknown flag.
	if code := testApp(&out, &errw).Run([]string{"secret", "--sort", "name"}); code != 1 {
		t.Fatalf("want exit 1, got %d", code)
	}
}

func TestRunRejectsInvalidColor(t *testing.T) {
	var out, errw bytes.Buffer
	if code := testApp(&out, &errw).Run([]string{"nodes", "--color", "bogus"}); code != 1 {
		t.Fatalf("want exit 1, got %d", code)
	}
	if !strings.Contains(errw.String(), "invalid --color") {
		t.Fatalf("want invalid --color error, got %q", errw.String())
	}
}

func TestRunAcceptsColorNever(t *testing.T) {
	var out, errw bytes.Buffer
	if code := testApp(&out, &errw).Run([]string{"nodes", "--color", "never"}); code != 0 {
		t.Fatalf("want exit 0, got %d (err=%q)", code, errw.String())
	}
	if strings.Contains(out.String(), "\x1b[") {
		t.Fatalf("want no ANSI with --color never, got %q", out.String())
	}
}

func TestRunAcceptsSingularAlias(t *testing.T) {
	var out, errw bytes.Buffer
	// "image" (singular) must resolve to the "images" command.
	if code := testApp(&out, &errw).Run([]string{"image"}); code != 0 {
		t.Fatalf("want exit 0, got %d (err=%q)", code, errw.String())
	}
	if !strings.Contains(out.String(), "PODNAME") {
		t.Fatalf("want images header from singular alias, got %q", out.String())
	}
}

// TestCurrentNSDefaultFlags locks in which commands scope to the current
// kubeconfig namespace by default (vs. all namespaces).
func TestCurrentNSDefaultFlags(t *testing.T) {
	want := map[string]bool{
		"reqlim":      true,
		"svc-fqdn":    true,
		"secret":      true,
		"pvc":         true,
		"images":      true,
		"restarts":    true,
		"no-limits":   true,
		"no-requests": true,
		"privileged":  true,
		"pdb":         true,
		"pending":     true,
		"hpa":         true,
		"spread":      true,
	}
	for _, c := range commands {
		if got := c.CurrentNSDefault; got != want[c.Name] {
			t.Errorf("%s: CurrentNSDefault = %v, want %v", c.Name, got, want[c.Name])
		}
	}
}
