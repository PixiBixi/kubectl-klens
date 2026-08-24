package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"slices"
	"strings"
	"syscall"
	"text/tabwriter"

	"github.com/PixiBixi/kubectl-klens/internal/kube"
	"github.com/PixiBixi/kubectl-klens/internal/view"
)

// BuildInfo carries ldflags-injected version metadata.
type BuildInfo struct {
	Version, Commit, Date string
}

// RunFunc is the signature every subcommand implements.
type RunFunc func(ctx context.Context, c kube.Clients, f kube.Flags, args []string, out io.Writer) error

// Command is a registry entry. CurrentNSDefault means that, absent -n and -A,
// the command scopes to the current kubeconfig namespace instead of all
// namespaces.
type Command struct {
	Name             string
	Summary          string
	Run              RunFunc
	CurrentNSDefault bool
	// SortColumns lists the column names accepted by --sort (lowercased headers).
	// When non-empty the dispatcher registers --sort, validates the value, and
	// the command sorts its output by that column.
	SortColumns []string
}

// commands is the registry of every subcommand. Built once at init; callers
// only range over it, so sharing the slice is safe.
var commands = []Command{
	{Name: "nodes", Summary: "List nodes with GKE nodepool and instance-type", Run: view.Nodes, SortColumns: []string{"name", "status", "nodepool", "instance-type"}},
	{Name: "taints", Summary: "List taints of all nodes", Run: view.Taints, SortColumns: []string{"name", "taints"}},
	{Name: "capacity", Summary: "Show CPU/memory capacity and allocatable per node", Run: view.Capacity, SortColumns: []string{"name", "cpu_cap", "cpu_alloc", "mem_cap", "mem_alloc"}},
	{Name: "zones", Summary: "Show region and zone per node", Run: view.Zones, SortColumns: []string{"name", "region", "zone"}},
	{Name: "node-ips", Summary: "Show internal and external IP per node; a node name narrows it to that node", Run: view.NodeIPs, SortColumns: []string{"name", "internal-ip", "external-ip"}},
	{Name: "pods-per-node", Summary: "Count pods per node", Run: view.PodsPerNode, SortColumns: []string{"node", "pods"}},
	{Name: "max-pods", Summary: "Show pod ceiling (allocatable), current count, and free slots per node", Run: view.MaxPods, SortColumns: []string{"node", "maxpods", "used", "free"}},
	{Name: "node-conditions", Summary: "Show node readiness and memory/disk/pid pressure", Run: view.NodeConditions, SortColumns: []string{"name", "status", "memory", "disk", "pid"}},
	{Name: "reqlim", Summary: "Show requests/limits per container in the current namespace (-A for all; -A excludes kube-system)", Run: view.Reqlim, CurrentNSDefault: true, SortColumns: []string{"ns", "pod", "container", "kind", "req_cpu", "lim_cpu", "req_mem", "lim_mem"}},
	{Name: "no-limits", Summary: "List containers missing CPU/memory limits in the current namespace (-A for all; -A excludes kube-system)", Run: view.NoLimits, CurrentNSDefault: true, SortColumns: []string{"ns", "pod", "container", "kind", "missing"}},
	{Name: "no-requests", Summary: "List containers missing CPU/memory requests in the current namespace (-A for all; -A excludes kube-system)", Run: view.NoRequests, CurrentNSDefault: true, SortColumns: []string{"ns", "pod", "container", "kind", "missing"}},
	{Name: "images", Summary: "List images per container per pod in the current namespace (-A for all)", Run: view.Images, CurrentNSDefault: true, SortColumns: []string{"podname", "container", "kind", "pull", "image", "tag"}},
	{Name: "image-count", Summary: "Count image occurrences split by registry/image/tag across the cluster", Run: view.ImageCount, SortColumns: []string{"count", "registry", "image", "tag"}},
	{Name: "on-node", Summary: "List pods scheduled on a given node", Run: view.OnNode, SortColumns: []string{"ns", "pod", "status", "node"}},
	{Name: "restarts", Summary: "List containers that have restarted, with the crash reason, in the current namespace (-A for all)", Run: view.Restarts, CurrentNSDefault: true, SortColumns: []string{"ns", "pod", "container", "kind", "restarts", "state", "exit"}},
	{Name: "pvc", Summary: "List PVCs bound to a pod and node in the current namespace (-A for all)", Run: view.Pvc, CurrentNSDefault: true, SortColumns: []string{"ns", "pod", "node", "pvc"}},
	{Name: "default-sa", Summary: "List pods still using the default service account", Run: view.DefaultSA, SortColumns: []string{"ns", "pod"}},
	{Name: "privileged", Summary: "List containers with privileged/host security flags in the current namespace (-A for all)", Run: view.Privileged, CurrentNSDefault: true, SortColumns: []string{"ns", "pod", "container", "kind", "flags"}},
	{Name: "svc-fqdn", Summary: "Show in-cluster FQDN of services in the current namespace (-A for all)", Run: view.SvcFQDN, CurrentNSDefault: true, SortColumns: []string{"ns", "service", "fqdn"}},
	{Name: "pdb", Summary: "List PodDisruptionBudgets with a drain-safety verdict in the current namespace (-A for all)", Run: view.Pdb, CurrentNSDefault: true, SortColumns: []string{"ns", "name", "policy", "expected", "desired", "healthy", "allowed", "verdict"}},
	{Name: "pending", Summary: "List Pending pods with the synthesized blocking reason in the current namespace (-A for all)", Run: view.Pending, CurrentNSDefault: true, SortColumns: []string{"ns", "pod", "reason"}},
	{Name: "hpa", Summary: "List HorizontalPodAutoscalers with an autoscaling verdict in the current namespace (-A for all)", Run: view.Hpa, CurrentNSDefault: true, SortColumns: []string{"ns", "name", "ref", "min", "max", "current", "desired", "verdict"}},
	{Name: "spread", Summary: "Show replica placement across nodes/zones with a single-point-of-failure verdict in the current namespace (-A for all)", Run: view.Spread, CurrentNSDefault: true, SortColumns: []string{"ns", "workload", "replicas", "nodes", "zones", "verdict"}},
	{Name: "probes", Summary: "List containers' readiness/liveness/startup probes with a reliability verdict in the current namespace (-A for all; -A excludes kube-system)", Run: view.Probes, CurrentNSDefault: true, SortColumns: []string{"ns", "pod", "container", "readiness", "liveness", "startup", "verdict"}},
	{Name: "autoscaler", Summary: "Print the cluster-autoscaler status (kube-system)", Run: view.Autoscaler, SortColumns: []string{"nodegroup", "health", "ready", "target", "min", "max", "scaleup", "scaledown", "last-change"}},
	{Name: "secret", Summary: "Browse secrets interactively (pick secret, then key); args skip the pickers", Run: view.Secret, CurrentNSDefault: true},
}

// globalFlag is a flag shared by every subcommand. The globalFlags table is the
// single source for both registration (Run) and the help listing (usage), so
// adding a flag can't leave the two out of sync.
type globalFlag struct {
	usage    string // how it appears in --help, e.g. "-n, --namespace string"
	help     string
	register func(fs *flag.FlagSet, f *kube.Flags, help string)
}

var globalFlags = []globalFlag{
	{"--kubeconfig string", "path to the kubeconfig file",
		func(fs *flag.FlagSet, f *kube.Flags, h string) { fs.StringVar(&f.Kubeconfig, "kubeconfig", "", h) }},
	{"--context string", "kubeconfig context to use",
		func(fs *flag.FlagSet, f *kube.Flags, h string) { fs.StringVar(&f.Context, "context", "", h) }},
	{"-n, --namespace string", "namespace scope (pod-based commands)",
		func(fs *flag.FlagSet, f *kube.Flags, h string) {
			fs.StringVar(&f.Namespace, "namespace", "", h)
			fs.StringVar(&f.Namespace, "n", "", h)
		}},
	{"-A, --all-namespaces", "list across all namespaces",
		func(fs *flag.FlagSet, f *kube.Flags, h string) {
			fs.BoolVar(&f.AllNamespaces, "all-namespaces", false, h)
			fs.BoolVar(&f.AllNamespaces, "A", false, h)
		}},
	{"--color string", "colorize output: auto|always|never (default auto)",
		func(fs *flag.FlagSet, f *kube.Flags, h string) { fs.StringVar(&f.ColorMode, "color", "", h) }},
	{"--request-timeout duration", "per-request timeout, 0 for none (default 1m0s)",
		func(fs *flag.FlagSet, f *kube.Flags, h string) {
			fs.DurationVar(&f.RequestTimeout, "request-timeout", kube.DefaultRequestTimeout, h)
		}},
}

// App wires dependencies so dispatch is testable with an injected client.
type App struct {
	Info      BuildInfo
	NewClient func(kube.Flags) (kube.Clients, error)
	Namespace func(kube.Flags) (string, error)
	Out       io.Writer
	Err       io.Writer
}

// NewApp returns an App backed by the real kube client and os streams.
func NewApp(info BuildInfo) App {
	return App{Info: info, NewClient: kube.NewClients, Namespace: kube.CurrentNamespace, Out: os.Stdout, Err: os.Stderr}
}

// Run parses args, dispatches the subcommand, and returns a process exit code.
func (a App) Run(args []string) int {
	if len(args) > 0 && args[0] == "__complete" {
		return a.complete(args[1:])
	}
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
	case "completion":
		return a.completionInstall(args[1:])
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
	for _, gf := range globalFlags {
		gf.register(fs, &f, gf.help)
	}
	if len(cmd.SortColumns) > 0 {
		fs.StringVar(&f.Sort, "sort", "", "sort by column: "+strings.Join(cmd.SortColumns, "|"))
	}
	if err := fs.Parse(args[1:]); err != nil {
		return 1
	}
	if f.Sort != "" && !slices.Contains(cmd.SortColumns, f.Sort) {
		fmt.Fprintf(a.Err, "error: invalid --sort %q for %s (want %s)\n", f.Sort, cmd.Name, strings.Join(cmd.SortColumns, "|"))
		return 1
	}
	switch f.ColorMode {
	case "", "auto", "always", "never":
	default:
		fmt.Fprintf(a.Err, "error: invalid --color %q (want auto|always|never)\n", f.ColorMode)
		return 1
	}
	f.Color = kube.ResolveColor(f.ColorMode, a.Out)
	client, err := a.NewClient(f)
	if err != nil {
		fmt.Fprintln(a.Err, "error: failed to build kubernetes clients:", err)
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
	// Ctrl-C cancels in-flight requests instead of leaving them to be reaped by
	// process exit, so a long cluster-wide list stops the moment the user asks.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	err = cmd.Run(ctx, client, f, fs.Args(), a.Out)
	var timeout interface{ Timeout() bool }
	switch {
	case err == nil:
		return 0

	// Only our own signal handler cancels this context, so a cancellation means
	// the user interrupted: not a failure, and worth the conventional SIGINT exit
	// code rather than a generic one.
	case ctx.Err() != nil || errors.Is(err, context.Canceled):
		fmt.Fprintln(a.Err, "canceled")
		return 130

	// A timeout has to name itself and its escape hatch. --request-timeout
	// defaults to a bound, so a user on a cluster big enough to hit it would
	// otherwise get an opaque failure with no hint that a flag controls it.
	case errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &timeout) && timeout.Timeout()):
		fmt.Fprintf(a.Err, "error: request timed out after %s; raise it or pass --request-timeout=0 to disable\n", f.RequestTimeout)
		return 1

	default:
		fmt.Fprintln(a.Err, "error:", err)
		return 1
	}
}

// lookup resolves a subcommand by name, accepting singular or plural forms
// (e.g. "image" and "images") by toggling a trailing "s".
func lookup(name string) (Command, bool) {
	for _, c := range commands {
		if c.Name == name {
			return c, true
		}
	}
	alt := name + "s"
	if before, ok := strings.CutSuffix(name, "s"); ok {
		alt = before
	}
	for _, c := range commands {
		if c.Name == alt {
			return c, true
		}
	}
	return Command{}, false
}

func (a App) usage(w io.Writer) {
	fmt.Fprint(w, `klens - kubectl plugin for quick cluster inspection

Usage:
  kubectl klens <command> [flags]

`)
	tw := tabwriter.NewWriter(w, 0, 8, 2, ' ', 0)
	fmt.Fprintln(tw, "Commands:")
	for _, c := range commands {
		fmt.Fprintf(tw, "  %s\t%s\n", c.Name, c.Summary)
	}
	fmt.Fprintln(tw, "\nFlags:")
	for _, gf := range globalFlags {
		fmt.Fprintf(tw, "  %s\t%s\n", gf.usage, gf.help)
	}
	fmt.Fprintf(tw, "  %s\t%s\n", "--version", "print version")
	fmt.Fprintln(tw, "\nOther:")
	fmt.Fprintf(tw, "  %s\t%s\n", "completion install", "install the shell-completion shim on your PATH")
	tw.Flush()
}
