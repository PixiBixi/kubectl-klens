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
		Out:       out,
		Err:       errw,
	}
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
