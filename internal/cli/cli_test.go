package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"

	"github.com/PixiBixi/kubectl-klens/internal/kube"
)

func testApp(out, errw *bytes.Buffer) App {
	return App{
		Info:      BuildInfo{Version: "test", Commit: "abc", Date: "today"},
		NewClient: func(kube.Flags) (kube.Clients, error) { return kube.Clients{Interface: fake.NewClientset()}, nil },
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
		NewClient: func(kube.Flags) (kube.Clients, error) { return kube.Clients{Interface: c}, nil },
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
		// The positional arg is a node name for the commands that take one, and
		// node-ips errors out when it names nothing, so back it with a node.
		objs := []runtime.Object{&corev1.Node{Name: "dummy"}}
		if err := c.Run(context.Background(), kube.Clients{Interface: fake.NewClientset(objs...)}, kube.Flags{}, []string{"dummy"}, &buf); err != nil {
			t.Fatalf("%s: run failed: %v", c.Name, err)
		}
		header, _, _ := strings.Cut(buf.String(), "\n")
		assertSortColumnsInHeader(t, c.Name, c.SortColumns, header)
	}
}

// assertSortColumnsInHeader checks every declared sort column appears as a
// whitespace-separated token in the (case-insensitive) header line.
func assertSortColumnsInHeader(t *testing.T, name string, cols []string, header string) {
	t.Helper()
	got := map[string]bool{}
	for h := range strings.FieldsSeq(strings.ToLower(header)) {
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
		Name: "cluster-autoscaler-status", Namespace: "kube-system",
		Data: map[string]string{"status": status},
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
	if err := cmd.Run(context.Background(), kube.Clients{Interface: fake.NewClientset(autoscalerStatusCM(status))}, kube.Flags{}, nil, &buf); err != nil {
		t.Fatalf("run failed: %v", err)
	}
	var header string
	for line := range strings.SplitSeq(buf.String(), "\n") {
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
		"probes":      true,
	}
	for _, c := range commands {
		if got := c.CurrentNSDefault; got != want[c.Name] {
			t.Errorf("%s: CurrentNSDefault = %v, want %v", c.Name, got, want[c.Name])
		}
	}
}

// TestRequestTimeoutReachesClient checks the flag is wired through to the client
// builder: the timeout only protects anything if kube.NewClients sees it.
func TestRequestTimeoutReachesClient(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want time.Duration
	}{
		{"default applies", []string{"nodes"}, kube.DefaultRequestTimeout},
		{"explicit value", []string{"nodes", "--request-timeout", "5s"}, 5 * time.Second},
		{"zero disables", []string{"nodes", "--request-timeout", "0"}, 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var got time.Duration
			var out, errw bytes.Buffer
			app := testApp(&out, &errw)
			app.NewClient = func(f kube.Flags) (kube.Clients, error) {
				got = f.RequestTimeout
				return kube.Clients{Interface: fake.NewClientset()}, nil
			}
			if code := app.Run(tc.args); code != 0 {
				t.Fatalf("exit %d: %s", code, errw.String())
			}
			if got != tc.want {
				t.Fatalf("RequestTimeout = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestRequestTimeoutRejectsGarbage keeps the flag from silently falling back to
// the default when the value is unparseable.
func TestRequestTimeoutRejectsGarbage(t *testing.T) {
	var out, errw bytes.Buffer
	if code := testApp(&out, &errw).Run([]string{"nodes", "--request-timeout", "soon"}); code != 1 {
		t.Fatalf("want exit 1 for an invalid duration, got %d", code)
	}
}

// TestRequestTimeoutInHelp guards that the flag is listed, since globalFlags
// drives both registration and the help text.
func TestRequestTimeoutInHelp(t *testing.T) {
	var out, errw bytes.Buffer
	testApp(&out, &errw).Run([]string{"--help"})
	if !strings.Contains(out.String(), "--request-timeout") {
		t.Fatalf("--request-timeout missing from help:\n%s", out.String())
	}
}

// TestCanceledContextExits130 pins the interrupt path: a command that stops
// because the user hit Ctrl-C is not a failure, and should report the
// conventional SIGINT exit code rather than a generic error.
func TestCanceledContextExits130(t *testing.T) {
	var out, errw bytes.Buffer
	app := testApp(&out, &errw)
	app.NewClient = func(kube.Flags) (kube.Clients, error) {
		c := fake.NewClientset()
		c.PrependReactor("list", "nodes", func(k8stesting.Action) (bool, runtime.Object, error) {
			// Stand in for the signal arriving mid-request.
			return true, nil, context.Canceled
		})
		return kube.Clients{Interface: c}, nil
	}
	code := app.Run([]string{"nodes"})
	if code != 130 {
		t.Fatalf("want exit 130 on cancellation, got %d (stderr: %s)", code, errw.String())
	}
	if !strings.Contains(errw.String(), "canceled") {
		t.Fatalf("want a 'canceled' notice, got %q", errw.String())
	}
}

// TestTimeoutErrorNamesTheFlag guards that hitting the default bound produces an
// actionable message: a default timeout with an opaque error would be a trap.
func TestTimeoutErrorNamesTheFlag(t *testing.T) {
	var out, errw bytes.Buffer
	app := testApp(&out, &errw)
	app.NewClient = func(kube.Flags) (kube.Clients, error) {
		c := fake.NewClientset()
		c.PrependReactor("list", "nodes", func(k8stesting.Action) (bool, runtime.Object, error) {
			return true, nil, context.DeadlineExceeded
		})
		return kube.Clients{Interface: c}, nil
	}
	if code := app.Run([]string{"nodes", "--request-timeout", "3s"}); code != 1 {
		t.Fatalf("want exit 1 on timeout, got %d", code)
	}
	for _, want := range []string{"timed out after 3s", "--request-timeout"} {
		if !strings.Contains(errw.String(), want) {
			t.Fatalf("want %q in stderr, got %q", want, errw.String())
		}
	}
}
