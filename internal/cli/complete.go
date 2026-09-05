package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/PixiBixi/kubectl-klens/internal/kube"
)

// completionFlags are the global flag tokens offered during shell completion.
// TestCompletionOffersEveryGlobalFlag checks this stays in step with the
// globalFlags table, which is the source of truth for registration and --help.
var completionFlags = []string{
	"--kubeconfig", "--context", "--namespace", "-n",
	"--all-namespaces", "-A", "--color", "--request-timeout",
	"--version", "--help", "-h",
}

// complete implements the cobra-compatible "__complete" protocol that kubectl
// invokes (through the kubectl_complete-klens shim) to complete "kubectl klens".
// It prints candidate completions followed by a ShellCompDirective line.
func (a App) complete(args []string) int {
	toComplete := ""
	if len(args) > 0 {
		toComplete = args[len(args)-1]
	}
	var prior []string
	if len(args) > 1 {
		prior = args[:len(args)-1]
	}
	for _, cand := range a.completions(prior, toComplete) {
		fmt.Fprintln(a.Out, cand)
	}
	// :4 == cobra ShellCompDirectiveNoFileComp (suppress filename fallback).
	fmt.Fprintln(a.Out, ":4")
	return 0
}

// completionTimeout bounds the one cluster call completion makes. A TAB that
// hangs is worse than a TAB that offers nothing, so an unreachable cluster has
// to give up inside the time a user will wait for their prompt to come back.
const completionTimeout = 2 * time.Second

// namespaceFlags are the tokens after which a value is a namespace.
var namespaceFlags = []string{"-n", "--namespace"}

func (a App) completions(prior []string, toComplete string) []string {
	if len(prior) > 0 && prior[0] == "completion" {
		if strings.HasPrefix(toComplete, "-") {
			return withPrefix([]string{"--dir"}, toComplete)
		}
		return withPrefix([]string{"install"}, toComplete)
	}
	if len(prior) > 0 && prior[len(prior)-1] == "--sort" {
		if c, ok := chosenCommand(prior); ok {
			return withPrefix(c.SortColumns, toComplete)
		}
		return nil
	}
	if len(prior) > 0 && prior[len(prior)-1] == "--color" {
		return withPrefix([]string{"auto", "always", "never"}, toComplete)
	}
	if len(prior) > 0 && slices.Contains(namespaceFlags, prior[len(prior)-1]) {
		return a.namespaceCompletions(prior, toComplete)
	}
	if strings.HasPrefix(toComplete, "-") {
		flags := completionFlags
		if c, ok := chosenCommand(prior); ok {
			// Per-command flags are only offered where they are registered, so a
			// completion never suggests a flag the dispatcher would reject.
			var extra []string
			if len(c.SortColumns) > 0 {
				extra = append(extra, "--sort")
			}
			if c.Watch {
				extra = append(extra, "-w", "--watch", "--interval")
			}
			if len(extra) > 0 {
				flags = slices.Concat(completionFlags, extra)
			}
		}
		return withPrefix(flags, toComplete)
	}
	if subcommandChosen(prior) {
		return nil
	}
	names := make([]string, 0, len(commands)+1)
	for _, c := range commands {
		names = append(names, c.Name)
	}
	names = append(names, "completion")
	return withPrefix(names, toComplete)
}

// namespaceCompletions lists the cluster's namespaces. This is the only
// completion that talks to a cluster, so it is also the only one that can fail:
// no kubeconfig, no network, no list rights on namespaces. Every one of those
// returns no candidates and no message - a completion helper writing an error to
// stdout would paste it into the user's command line.
func (a App) namespaceCompletions(prior []string, toComplete string) []string {
	if a.NewClient == nil {
		return nil
	}
	f := kube.Flags{RequestTimeout: completionTimeout}
	// --kubeconfig and --context change which cluster to ask, and the user may
	// well have typed them before the -n they are completing.
	for i := 0; i+1 < len(prior); i++ {
		switch prior[i] {
		case "--kubeconfig":
			f.Kubeconfig = prior[i+1]
		case "--context":
			f.Context = prior[i+1]
		}
	}
	c, err := a.NewClient(f)
	if err != nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), completionTimeout)
	defer cancel()
	list, err := kube.ListNamespaces(ctx, c, metav1.ListOptions{})
	if err != nil {
		return nil
	}
	names := make([]string, 0, len(list))
	for i := range list {
		names = append(names, list[i].Name)
	}
	slices.Sort(names)
	return withPrefix(names, toComplete)
}

func subcommandChosen(prior []string) bool {
	_, ok := chosenCommand(prior)
	return ok
}

// chosenCommand returns the first already-typed word that resolves to a command
// (honoring singular/plural aliases).
func chosenCommand(prior []string) (Command, bool) {
	for _, w := range prior {
		if c, ok := lookup(w); ok {
			return c, true
		}
	}
	return Command{}, false
}

func withPrefix(candidates []string, prefix string) []string {
	out := make([]string, 0, len(candidates))
	for _, c := range candidates {
		if strings.HasPrefix(c, prefix) {
			out = append(out, c)
		}
	}
	return out
}

// completionShim is the kubectl_complete-klens executable that kubectl runs to
// fetch candidates; it forwards to the plugin's hidden __complete command.
const completionShim = `#!/usr/bin/env bash
exec kubectl-klens __complete "$@"
`

// completionInstall writes the completion shim into a directory on the PATH so
// "kubectl klens <TAB>" works. It needs no cluster access.
func (a App) completionInstall(args []string) int {
	if len(args) == 0 || args[0] != "install" {
		fmt.Fprintln(a.Err, "usage: kubectl klens completion install [--dir <dir>]")
		return 1
	}
	fs := flag.NewFlagSet("klens completion", flag.ContinueOnError)
	fs.SetOutput(a.Err)
	var dir string
	fs.StringVar(&dir, "dir", "", "target directory (must be on PATH); defaults to krew's bin dir")
	if err := fs.Parse(args[1:]); err != nil {
		return 1
	}
	target, err := completionDir(dir)
	if err != nil {
		fmt.Fprintln(a.Err, "error:", err)
		return 1
	}
	path := filepath.Join(target, "kubectl_complete-klens")
	//nolint:gosec // G306: the shim is an executable kubectl plugin, it must be 0755
	if err := os.WriteFile(path, []byte(completionShim), 0o755); err != nil {
		fmt.Fprintln(a.Err, "error: failed to write shim:", err)
		return 1
	}
	//nolint:gosec // G302: same, the shim must carry the executable bit
	if err := os.Chmod(path, 0o755); err != nil {
		fmt.Fprintln(a.Err, "error: failed to set exec bit:", err)
		return 1
	}
	fmt.Fprintf(a.Out, "installed %s\n", path)
	if !dirOnPath(target) {
		fmt.Fprintf(a.Out, "warning: %s is not on your PATH; completion will activate once it is\n", target)
	}
	fmt.Fprintln(a.Out, "load kubectl completion too, e.g. source <(kubectl completion zsh)")
	return 0
}

// completionDir resolves where to drop the shim: an explicit override, else
// krew's bin dir (KREW_ROOT or ~/.krew/bin).
func completionDir(override string) (string, error) {
	if override != "" {
		return override, nil
	}
	if r := os.Getenv("KREW_ROOT"); r != "" {
		return filepath.Join(r, "bin"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", errors.New("cannot locate home dir; pass --dir <dir on your PATH>")
	}
	krew := filepath.Join(home, ".krew", "bin")
	if fi, err := os.Stat(krew); err == nil && fi.IsDir() {
		return krew, nil
	}
	return "", errors.New("no krew bin dir found; pass --dir <dir on your PATH>")
}

func dirOnPath(dir string) bool {
	want := filepath.Clean(dir)
	for _, p := range filepath.SplitList(os.Getenv("PATH")) {
		if filepath.Clean(p) == want {
			return true
		}
	}
	return false
}
