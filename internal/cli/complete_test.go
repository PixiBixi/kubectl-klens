package cli

import (
	"bytes"
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

func TestCompleteAfterSubcommandNoNames(t *testing.T) {
	out := completeOut(t, "secret", "")
	if strings.Contains(out, "nodes") {
		t.Errorf("should not offer subcommand names after a subcommand:\n%s", out)
	}
}
