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

// Command is a registry entry. CurrentNSDefault means that, absent -n and -A,
// the command scopes to the current kubeconfig namespace instead of all
// namespaces.
type Command struct {
	Name             string
	Summary          string
	Run              RunFunc
	CurrentNSDefault bool
}

func commands() []Command {
	return []Command{
		{Name: "nodes", Summary: "List nodes with GKE nodepool and instance-type", Run: view.Nodes},
		{Name: "taints", Summary: "List taints of all nodes", Run: view.Taints},
		{Name: "capacity", Summary: "Show CPU/memory capacity and allocatable per node", Run: view.Capacity},
		{Name: "zones", Summary: "Show region and zone per node", Run: view.Zones},
		{Name: "pods-per-node", Summary: "Count pods per node", Run: view.PodsPerNode},
		{Name: "reqlim", Summary: "Show requests/limits per container in the current namespace (-A for all; excludes kube-system)", Run: view.Reqlim, CurrentNSDefault: true},
		{Name: "images", Summary: "Count image occurrences across the cluster", Run: view.Images},
		{Name: "on-node", Summary: "List pods scheduled on a given node", Run: view.OnNode},
		{Name: "pvc", Summary: "List PVCs bound to a pod and node in the current namespace (-A for all)", Run: view.Pvc, CurrentNSDefault: true},
		{Name: "default-sa", Summary: "List pods still using the default service account", Run: view.DefaultSA},
		{Name: "svc-fqdn", Summary: "Show in-cluster FQDN of services in the current namespace (-A for all)", Run: view.SvcFQDN, CurrentNSDefault: true},
		{Name: "autoscaler", Summary: "Print the cluster-autoscaler status (kube-system)", Run: view.Autoscaler},
		{Name: "secret", Summary: "Decode and print a secret's data in the current namespace (-n to target another)", Run: view.Secret, CurrentNSDefault: true},
	}
}

// App wires dependencies so dispatch is testable with an injected client.
type App struct {
	Info      BuildInfo
	NewClient func(kube.Flags) (kubernetes.Interface, error)
	Namespace func(kube.Flags) (string, error)
	Out       io.Writer
	Err       io.Writer
}

// NewApp returns an App backed by the real kube client and os streams.
func NewApp(info BuildInfo) App {
	return App{Info: info, NewClient: kube.Client, Namespace: kube.CurrentNamespace, Out: os.Stdout, Err: os.Stderr}
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
	if cmd.CurrentNSDefault && !f.AllNamespaces && f.Namespace == "" {
		ns, err := a.Namespace(f)
		if err != nil {
			fmt.Fprintln(a.Err, "error: failed to resolve current namespace:", err)
			return 1
		}
		f.Namespace = ns
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
	fmt.Fprint(w, `klens — kubectl plugin for quick cluster inspection

Usage:
  kubectl klens <command> [flags]

`)
	tw := tabwriter.NewWriter(w, 0, 8, 2, ' ', 0)
	fmt.Fprintln(tw, "Commands:")
	for _, c := range commands() {
		fmt.Fprintf(tw, "  %s\t%s\n", c.Name, c.Summary)
	}
	fmt.Fprintln(tw, "\nFlags:")
	for _, fl := range []struct{ flag, help string }{
		{"--kubeconfig string", "path to the kubeconfig file"},
		{"--context string", "kubeconfig context to use"},
		{"-n, --namespace string", "namespace scope (pod-based commands)"},
		{"-A, --all-namespaces", "list across all namespaces"},
		{"--version", "print version"},
	} {
		fmt.Fprintf(tw, "  %s\t%s\n", fl.flag, fl.help)
	}
	tw.Flush()
}
