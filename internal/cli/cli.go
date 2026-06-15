package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"text/tabwriter"

	"k8s.io/client-go/kubernetes"

	"github.com/PixiBixi/kubectl-klens/internal/kube"
	"github.com/PixiBixi/kubectl-klens/internal/view"
)

// BuildInfo carries ldflags-injected version metadata.
type BuildInfo struct {
	Version, Commit, Date string
}

// RunFunc is the signature every subcommand implements.
type RunFunc func(ctx context.Context, c kubernetes.Interface, f kube.Flags, args []string, out io.Writer) error

// Command is a registry entry.
type Command struct {
	Name    string
	Summary string
	Run     RunFunc
}

func commands() []Command {
	return []Command{
		{"nodes", "List nodes with GKE nodepool and instance-type", view.Nodes},
		{"taints", "List taints of all nodes", view.Taints},
		{"capacity", "Show CPU/memory capacity and allocatable per node", view.Capacity},
		{"zones", "Show region and zone per node", view.Zones},
		{"pods-per-node", "Count pods per node", view.PodsPerNode},
		{"reqlim", "Show requests/limits per container (excludes kube-system)", view.Reqlim},
		{"images", "Count image occurrences across the cluster", view.Images},
		{"on-node", "List pods scheduled on a given node", view.OnNode},
		{"pvc", "List PVCs bound to a pod and node", view.Pvc},
	}
}

// App wires dependencies so dispatch is testable with an injected client.
type App struct {
	Info      BuildInfo
	NewClient func(kube.Flags) (kubernetes.Interface, error)
	Out       io.Writer
	Err       io.Writer
}

// NewApp returns an App backed by the real kube client and os streams.
func NewApp(info BuildInfo) App {
	return App{Info: info, NewClient: kube.Client, Out: os.Stdout, Err: os.Stderr}
}

// Run parses args, dispatches the subcommand, and returns a process exit code.
func (a App) Run(args []string) int {
	if len(args) == 0 {
		a.usage(a.Err)
		return 1
	}
	switch args[0] {
	case "-h", "--help", "help":
		a.usage(a.Out)
		return 0
	case "-v", "--version", "version":
		fmt.Fprintf(a.Out, "klens %s (commit %s, built %s)\n", a.Info.Version, a.Info.Commit, a.Info.Date)
		return 0
	}
	cmd, ok := lookup(args[0])
	if !ok {
		fmt.Fprintf(a.Err, "unknown subcommand %q\n\n", args[0])
		a.usage(a.Err)
		return 1
	}
	fs := flag.NewFlagSet("klens "+cmd.Name, flag.ContinueOnError)
	fs.SetOutput(a.Err)
	var f kube.Flags
	fs.StringVar(&f.Kubeconfig, "kubeconfig", "", "path to the kubeconfig file")
	fs.StringVar(&f.Context, "context", "", "kubeconfig context to use")
	fs.StringVar(&f.Namespace, "namespace", "", "namespace scope (pod-based commands)")
	fs.StringVar(&f.Namespace, "n", "", "namespace scope (shorthand)")
	fs.BoolVar(&f.AllNamespaces, "all-namespaces", false, "list across all namespaces")
	fs.BoolVar(&f.AllNamespaces, "A", false, "list across all namespaces (shorthand)")
	if err := fs.Parse(args[1:]); err != nil {
		return 1
	}
	client, err := a.NewClient(f)
	if err != nil {
		fmt.Fprintln(a.Err, "error: failed to build kubernetes client:", err)
		return 1
	}
	if err := cmd.Run(context.Background(), client, f, fs.Args(), a.Out); err != nil {
		fmt.Fprintln(a.Err, "error:", err)
		return 1
	}
	return 0
}

func lookup(name string) (Command, bool) {
	for _, c := range commands() {
		if c.Name == name {
			return c, true
		}
	}
	return Command{}, false
}

func (a App) usage(w io.Writer) {
	fmt.Fprintln(w, "klens — kubectl plugin for quick cluster inspection")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Usage:")
	fmt.Fprintln(w, "  kubectl klens <command> [flags]")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Commands:")
	tw := tabwriter.NewWriter(w, 0, 8, 2, ' ', 0)
	for _, c := range commands() {
		fmt.Fprintf(tw, "  %s\t%s\n", c.Name, c.Summary)
	}
	tw.Flush()
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Flags:")
	fmt.Fprintln(w, "  --kubeconfig string    path to the kubeconfig file")
	fmt.Fprintln(w, "  --context string       kubeconfig context to use")
	fmt.Fprintln(w, "  -n, --namespace string namespace scope (pod-based commands)")
	fmt.Fprintln(w, "  -A, --all-namespaces   list across all namespaces")
	fmt.Fprintln(w, "  --version              print version")
}
