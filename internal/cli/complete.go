package cli

import (
	"fmt"
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
	if strings.HasPrefix(toComplete, "-") {
		return withPrefix(completionFlags, toComplete)
	}
	if subcommandChosen(prior) {
		return nil
	}
	names := make([]string, 0, len(commands()))
	for _, c := range commands() {
		names = append(names, c.Name)
	}
	return withPrefix(names, toComplete)
}

func subcommandChosen(prior []string) bool {
	for _, w := range prior {
		if _, ok := lookup(w); ok {
			return true
		}
	}
	return false
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
