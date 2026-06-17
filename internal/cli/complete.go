package cli

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// completionFlags are the global flag tokens offered during shell completion.
var completionFlags = []string{
	"--kubeconfig", "--context", "--namespace", "-n",
	"--all-namespaces", "-A", "--version", "--help", "-h",
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
	for _, cand := range completions(prior, toComplete) {
		fmt.Fprintln(a.Out, cand)
	}
	// :4 == cobra ShellCompDirectiveNoFileComp (suppress filename fallback).
	fmt.Fprintln(a.Out, ":4")
	return 0
}

func completions(prior []string, toComplete string) []string {
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
	if strings.HasPrefix(toComplete, "-") {
		flags := completionFlags
		if c, ok := chosenCommand(prior); ok && len(c.SortColumns) > 0 {
			flags = append(append([]string{}, completionFlags...), "--sort")
		}
		return withPrefix(flags, toComplete)
	}
	if subcommandChosen(prior) {
		return nil
	}
	names := make([]string, 0, len(commands())+1)
	for _, c := range commands() {
		names = append(names, c.Name)
	}
	names = append(names, "completion")
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
	if err := os.WriteFile(path, []byte(completionShim), 0o755); err != nil {
		fmt.Fprintln(a.Err, "error: failed to write shim:", err)
		return 1
	}
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
		return "", fmt.Errorf("cannot locate home dir; pass --dir <dir on your PATH>")
	}
	krew := filepath.Join(home, ".krew", "bin")
	if fi, err := os.Stat(krew); err == nil && fi.IsDir() {
		return krew, nil
	}
	return "", fmt.Errorf("no krew bin dir found; pass --dir <dir on your PATH>")
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
