package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func completeOut(t *testing.T, args ...string) string {
	t.Helper()
	var out, errw bytes.Buffer
	app := testApp(&out, &errw)
	full := append([]string{"__complete"}, args...)
	if code := app.Run(full); code != 0 {
		t.Fatalf("complete exit = %d, want 0 (err=%q)", code, errw.String())
	}
	return out.String()
}

func TestCompleteSubcommands(t *testing.T) {
	out := completeOut(t, "")
	for _, want := range []string{"nodes", "pvc", "secret"} {
		if !strings.Contains(out, want) {
			t.Errorf("completion missing %q:\n%s", want, out)
		}
	}
	if !strings.Contains(out, ":4") {
		t.Errorf("missing directive line:\n%s", out)
	}
	if strings.Contains(out, "--kubeconfig") {
		t.Errorf("should not offer flags when completing a subcommand:\n%s", out)
	}
}

func TestCompleteSubcommandPrefix(t *testing.T) {
	out := completeOut(t, "no")
	if !strings.Contains(out, "nodes") {
		t.Errorf("want nodes:\n%s", out)
	}
	if strings.Contains(out, "pvc") {
		t.Errorf("pvc should be filtered out by prefix 'no':\n%s", out)
	}
}

func TestCompleteFlags(t *testing.T) {
	out := completeOut(t, "nodes", "-")
	for _, want := range []string{"--kubeconfig", "-n", "-A"} {
		if !strings.Contains(out, want) {
			t.Errorf("flag completion missing %q:\n%s", want, out)
		}
	}
}

func TestCompleteLongFlagPrefix(t *testing.T) {
	out := completeOut(t, "nodes", "--name")
	if !strings.Contains(out, "--namespace") {
		t.Errorf("want --namespace:\n%s", out)
	}
}

func TestCompleteOffersSortForSortableCommands(t *testing.T) {
	for _, name := range []string{"image-count", "nodes", "zones", "autoscaler"} {
		if out := completeOut(t, name, "-"); !strings.Contains(out, "--sort") {
			t.Errorf("want --sort offered for %s:\n%s", name, out)
		}
	}
	// Commands without sort columns must not offer --sort.
	if out := completeOut(t, "secret", "-"); strings.Contains(out, "--sort") {
		t.Errorf("--sort must not be offered for secret:\n%s", out)
	}
}

func TestCompleteSortColumns(t *testing.T) {
	out := completeOut(t, "image-count", "--sort", "")
	for _, want := range []string{"count", "registry", "image", "tag"} {
		if !strings.Contains(out, want) {
			t.Errorf("sort-column completion missing %q:\n%s", want, out)
		}
	}
}

func TestCompleteAfterSubcommandNoNames(t *testing.T) {
	out := completeOut(t, "secret", "")
	if strings.Contains(out, "nodes") {
		t.Errorf("should not offer subcommand names after a subcommand:\n%s", out)
	}
}

func TestCompleteOffersCompletion(t *testing.T) {
	out := completeOut(t, "comp")
	if !strings.Contains(out, "completion") {
		t.Errorf("want completion in candidates:\n%s", out)
	}
}

func TestCompleteCompletionInstall(t *testing.T) {
	out := completeOut(t, "completion", "")
	if !strings.Contains(out, "install") {
		t.Errorf("want install candidate:\n%s", out)
	}
}

func TestCompletionInstallWritesShim(t *testing.T) {
	dir := t.TempDir()
	var out, errw bytes.Buffer
	app := testApp(&out, &errw)
	if code := app.Run([]string{"completion", "install", "--dir", dir}); code != 0 {
		t.Fatalf("exit = %d, want 0 (err=%q)", code, errw.String())
	}
	path := filepath.Join(dir, "kubectl_complete-klens")
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("shim not written: %v", err)
	}
	if fi.Mode().Perm()&0o100 == 0 {
		t.Errorf("shim is not executable: %v", fi.Mode())
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `exec kubectl-klens __complete "$@"`) {
		t.Errorf("unexpected shim content:\n%s", b)
	}
}

func TestCompletionInstallRequiresInstallArg(t *testing.T) {
	var out, errw bytes.Buffer
	app := testApp(&out, &errw)
	if code := app.Run([]string{"completion"}); code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
	if !strings.Contains(errw.String(), "completion install") {
		t.Errorf("want usage hint, got %q", errw.String())
	}
}
