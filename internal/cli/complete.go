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

	"github.com/PixiBixi/kubectl-klens/internal/kube"
)

// completionFlags are the global flag tokens offered during shell completion,
// derived from the globalFlags usage strings so the two cannot drift.
var completionFlags = func() []string {
	var out []string
	for _, gf := range globalFlags {
		for token := range strings.FieldsSeq(strings.ReplaceAll(gf.usage, ",", " ")) {
			if strings.HasPrefix(token, "-") { // skip the type word, e.g. "string"
				out = append(out, token)
			}
		}
	}
	return append(out, "--version", "--help", "-h")
}()

// complete implements the cobra-compatible "__complete" protocol that kubectl
// invokes (through the kubectl_complete-klens shim) to complete "kubectl klens".
// It prints candidate completions followed by a ShellCompDirective line.
func (a App) complete(args []string) int {
	var toComplete string
	var prior []string
	if n := len(args); n > 0 {
		toComplete, prior = args[n-1], args[:n-1]
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
	var prev string
	if len(prior) > 0 {
		prev = prior[len(prior)-1]
	}
	cmd, chosen := chosenCommand(prior)
	switch {
	case prev == "--sort":
		return withPrefix(cmd.SortColumns, toComplete)
	case prev == "--color":
		return withPrefix(colorModes, toComplete)
	case slices.Contains(namespaceFlags, prev):
		return a.namespaceCompletions(prior, toComplete)
	case strings.HasPrefix(toComplete, "-"):
		flags := slices.Clone(completionFlags)
		for _, cf := range commandFlags {
			if chosen && cf.on(cmd) {
				flags = append(flags, cf.tokens...)
			}
		}
		return withPrefix(flags, toComplete)
	case chosen:
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
	// Both spellings the flag package accepts: "--context x" and "--context=x".
	for i, arg := range prior {
		name, val, inline := strings.Cut(arg, "=")
		if !inline {
			if i+1 == len(prior) {
				break
			}
			val = prior[i+1]
		}
		switch name {
		case "--kubeconfig":
			f.Kubeconfig = val
		case "--context":
			f.Context = val
		}
	}
	c, err := a.NewClient(f)
	if err != nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), completionTimeout)
	defer cancel()
	names, err := kube.NamespaceNames(ctx, c)
	if err != nil {
		return nil
	}
	return withPrefix(names, toComplete)
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
	return slices.ContainsFunc(filepath.SplitList(os.Getenv("PATH")), func(p string) bool { return filepath.Clean(p) == want })
}
