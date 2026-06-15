package cli

import (
	"bytes"
	"strings"
	"testing"

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
	for _, c := range commands() {
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
	// images has no CurrentNSDefault: it must keep listing all namespaces.
	var out, errw bytes.Buffer
	app, c, called := reqlimApp(&out, &errw, "team-a")
	if code := app.Run([]string{"images"}); code != 0 {
		t.Fatalf("want exit 0, got %d (err=%q)", code, errw.String())
	}
	if *called {
		t.Fatal("resolver must not be called for non-CurrentNSDefault commands")
	}
	if got := listedNamespace(c); got != "" {
		t.Fatalf("want images across all namespaces (empty), got %q", got)
	}
}
