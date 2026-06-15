# kubectl-klens Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A kubectl plugin (`kubectl klens <cmd>`) written in Go that bundles 9 read-only cluster-inspection commands, packaged as a krew plugin.

**Architecture:** Single binary `kubectl-klens`. `main.go` injects build vars and calls `cli.App.Run`. `internal/cli` parses global flags + dispatches subcommands. `internal/kube` builds the client-go clientset and holds shared table/format helpers. `internal/view` has one function per subcommand, each querying the API and rendering a `text/tabwriter` table. Tests use the client-go fake clientset (no live cluster).

**Tech Stack:** Go 1.26, `k8s.io/client-go` v0.35.1 (client-go, api, apimachinery), stdlib `flag` + `text/tabwriter`, goreleaser v2, GitHub Actions, krew manifest. Mirrors `github.com/PixiBixi/kubearch` conventions (staticcheck, `go test -race`).

---

## File Structure

| File | Responsibility |
|------|----------------|
| `go.mod` / `go.sum` | module `github.com/PixiBixi/kubectl-klens`, deps |
| `main.go` | build vars (ldflags), construct app, exit code |
| `internal/kube/flags.go` | `Flags` struct + `NamespaceScope()` |
| `internal/kube/client.go` | `Client(Flags)` → clientset (clientcmd) |
| `internal/kube/table.go` | `Table` (tabwriter wrapper), `Label()` |
| `internal/kube/*_test.go` | unit tests for helpers |
| `internal/view/view.go` | unexported shared helpers (`nodeStatus`, `qtyOrNone`, sort) |
| `internal/view/<cmd>.go` | one exported func per subcommand |
| `internal/view/<cmd>_test.go` | fake-clientset test per subcommand |
| `internal/cli/cli.go` | `App`, `Run`, command registry, usage |
| `internal/cli/cli_test.go` | dispatch tests (injected fake client) |
| `.goreleaser.yml` | cross-build + archives + checksums + changelog |
| `.github/workflows/ci.yml` | verify, build, vet, staticcheck, test -race |
| `.github/workflows/release.yml` | goreleaser on tag + krew-release-bot |
| `.krew.yaml` | krew manifest template |
| `Makefile` | build / lint / test / snapshot |
| `README.md`, `LICENSE`, `CHANGELOG.md`, `.gitignore`, `.pre-commit-config.yaml` | repo meta |

---

## Task 1: Module scaffold + `kube` package

**Files:**
- Create: `go.mod`, `internal/kube/flags.go`, `internal/kube/client.go`, `internal/kube/table.go`, `internal/kube/kube_test.go`

- [ ] **Step 1: Init module and fetch deps**

Run:
```bash
cd /Users/jeremy/Documents/perso/git/kubectl-klens
go mod init github.com/PixiBixi/kubectl-klens
go get k8s.io/client-go@v0.35.1 k8s.io/api@v0.35.1 k8s.io/apimachinery@v0.35.1
```
Edit `go.mod` so the first line `go` directive reads `go 1.26`.

- [ ] **Step 2: Write `internal/kube/flags.go`**

```go
package kube

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

// Flags holds the standard kubeconfig-related options shared by all commands.
type Flags struct {
	Kubeconfig    string
	Context       string
	Namespace     string
	AllNamespaces bool
}

// NamespaceScope returns the namespace to list in. Empty string means all
// namespaces. Default (no -n, no -A) is all namespaces, matching the original
// wiki one-liners. -A forces all; -n narrows.
func (f Flags) NamespaceScope() string {
	if f.AllNamespaces {
		return metav1.NamespaceAll
	}
	return f.Namespace
}
```

- [ ] **Step 3: Write `internal/kube/client.go`**

```go
package kube

import (
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
)

// Client builds a clientset from the default loading rules plus the explicit
// kubeconfig path and context override. Same pattern as kubearch.
func Client(f Flags) (kubernetes.Interface, error) {
	loadingRules := clientcmd.NewDefaultClientConfigLoadingRules()
	if f.Kubeconfig != "" {
		loadingRules.ExplicitPath = f.Kubeconfig
	}
	overrides := &clientcmd.ConfigOverrides{}
	if f.Context != "" {
		overrides.CurrentContext = f.Context
	}
	cfg, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(loadingRules, overrides).ClientConfig()
	if err != nil {
		return nil, err
	}
	return kubernetes.NewForConfig(cfg)
}
```

- [ ] **Step 4: Write `internal/kube/table.go`**

```go
package kube

import (
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
)

// Table is a thin wrapper over text/tabwriter for aligned columnar output.
type Table struct {
	w *tabwriter.Writer
}

// NewTable starts a table and writes the header row.
func NewTable(out io.Writer, headers ...string) *Table {
	w := tabwriter.NewWriter(out, 0, 8, 2, ' ', 0)
	fmt.Fprintln(w, strings.Join(headers, "\t"))
	return &Table{w: w}
}

// Row appends one data row.
func (t *Table) Row(cols ...string) {
	fmt.Fprintln(t.w, strings.Join(cols, "\t"))
}

// Flush renders the table to the underlying writer.
func (t *Table) Flush() error {
	return t.w.Flush()
}

// Label returns the value of key in labels, or "<none>" when absent/empty.
func Label(labels map[string]string, key string) string {
	if v, ok := labels[key]; ok && v != "" {
		return v
	}
	return "<none>"
}
```

- [ ] **Step 5: Write `internal/kube/kube_test.go`**

```go
package kube

import (
	"bytes"
	"strings"
	"testing"
)

func TestLabel(t *testing.T) {
	m := map[string]string{"a": "1", "empty": ""}
	if got := Label(m, "a"); got != "1" {
		t.Errorf("got %q, want 1", got)
	}
	if got := Label(m, "empty"); got != "<none>" {
		t.Errorf("got %q, want <none>", got)
	}
	if got := Label(m, "missing"); got != "<none>" {
		t.Errorf("got %q, want <none>", got)
	}
}

func TestNamespaceScope(t *testing.T) {
	if got := (Flags{Namespace: "foo"}).NamespaceScope(); got != "foo" {
		t.Errorf("got %q, want foo", got)
	}
	if got := (Flags{Namespace: "foo", AllNamespaces: true}).NamespaceScope(); got != "" {
		t.Errorf("got %q, want empty (all)", got)
	}
	if got := (Flags{}).NamespaceScope(); got != "" {
		t.Errorf("got %q, want empty (all)", got)
	}
}

func TestTable(t *testing.T) {
	var buf bytes.Buffer
	tw := NewTable(&buf, "NAME", "VAL")
	tw.Row("a", "1")
	if err := tw.Flush(); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "NAME") || !strings.Contains(out, "a") {
		t.Fatalf("unexpected table:\n%s", out)
	}
}
```

- [ ] **Step 6: Run tests, then commit**

```bash
go test ./internal/kube/...   # expect PASS
git add go.mod go.sum internal/kube
git commit -m "feat(kube): client builder, flags and table helpers"
```

---

## Task 2: `view` shared helpers

**Files:**
- Create: `internal/view/view.go`

- [ ] **Step 1: Write `internal/view/view.go`**

```go
package view

import (
	corev1 "k8s.io/api/core/v1"
)

// nodeStatus returns Ready / NotReady / Unknown from a node's conditions.
func nodeStatus(n corev1.Node) string {
	for _, cond := range n.Status.Conditions {
		if cond.Type == corev1.NodeReady {
			if cond.Status == corev1.ConditionTrue {
				return "Ready"
			}
			return "NotReady"
		}
	}
	return "Unknown"
}

// qtyOrNone returns the string form of a resource quantity, or "none" if unset.
func qtyOrNone(rl corev1.ResourceList, name corev1.ResourceName) string {
	if q, ok := rl[name]; ok {
		return q.String()
	}
	return "none"
}
```

- [ ] **Step 2: Build, then commit**

```bash
go build ./internal/view/...   # expect success (helpers unused yet → keep until Task 3 uses them)
git add internal/view/view.go
git commit -m "feat(view): shared node/resource format helpers"
```

> Note: `go build` of an isolated package with unused unexported funcs succeeds (unused funcs are allowed; only unused imports/locals fail). They are consumed starting Task 3.

---

## Task 3: `nodes` subcommand

**Files:**
- Create: `internal/view/nodes.go`, `internal/view/nodes_test.go`

- [ ] **Step 1: Write the failing test `internal/view/nodes_test.go`**

```go
package view

import (
	"bytes"
	"context"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/PixiBixi/kubectl-klens/internal/kube"
)

func TestNodes(t *testing.T) {
	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "gke-pool-1-abc",
			Labels: map[string]string{
				"cloud.google.com/gke-nodepool":    "pool-1",
				"node.kubernetes.io/instance-type": "e2-standard-4",
			},
		},
		Status: corev1.NodeStatus{
			Conditions: []corev1.NodeCondition{{Type: corev1.NodeReady, Status: corev1.ConditionTrue}},
		},
	}
	c := fake.NewClientset(node)
	var buf bytes.Buffer
	if err := Nodes(context.Background(), c, kube.Flags{}, nil, &buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{"NAME", "gke-pool-1-abc", "Ready", "pool-1", "e2-standard-4"} {
		if !strings.Contains(out, want) {
			t.Fatalf("output missing %q:\n%s", want, out)
		}
	}
}
```

- [ ] **Step 2: Run, expect fail**

Run: `go test ./internal/view/ -run TestNodes`
Expected: build error `undefined: Nodes`.

- [ ] **Step 3: Write `internal/view/nodes.go`**

```go
package view

import (
	"context"
	"io"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/PixiBixi/kubectl-klens/internal/kube"
)

// Nodes lists nodes with their GKE nodepool and instance-type labels.
func Nodes(ctx context.Context, c kubernetes.Interface, f kube.Flags, args []string, out io.Writer) error {
	nodes, err := c.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return err
	}
	t := kube.NewTable(out, "NAME", "STATUS", "NODEPOOL", "INSTANCE-TYPE")
	for _, n := range nodes.Items {
		t.Row(
			n.Name,
			nodeStatus(n),
			kube.Label(n.Labels, "cloud.google.com/gke-nodepool"),
			kube.Label(n.Labels, "node.kubernetes.io/instance-type"),
		)
	}
	return t.Flush()
}
```

- [ ] **Step 4: Run, expect pass, commit**

```bash
go test ./internal/view/ -run TestNodes   # expect PASS
git add internal/view/nodes.go internal/view/nodes_test.go
git commit -m "feat(view): nodes subcommand (nodepool + instance-type)"
```

---

## Task 4: `taints` subcommand

**Files:**
- Create: `internal/view/taints.go`, `internal/view/taints_test.go`

- [ ] **Step 1: Write the failing test `internal/view/taints_test.go`**

```go
package view

import (
	"bytes"
	"context"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/PixiBixi/kubectl-klens/internal/kube"
)

func TestTaints(t *testing.T) {
	tainted := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "n1"},
		Spec: corev1.NodeSpec{Taints: []corev1.Taint{
			{Key: "dedicated", Value: "gpu", Effect: corev1.TaintEffectNoSchedule},
		}},
	}
	clean := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "n2"}}
	c := fake.NewClientset(tainted, clean)
	var buf bytes.Buffer
	if err := Taints(context.Background(), c, kube.Flags{}, nil, &buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "dedicated=gpu:NoSchedule") {
		t.Fatalf("missing taint:\n%s", out)
	}
	if !strings.Contains(out, "<none>") {
		t.Fatalf("clean node should show <none>:\n%s", out)
	}
}
```

- [ ] **Step 2: Run, expect fail**

Run: `go test ./internal/view/ -run TestTaints`
Expected: `undefined: Taints`.

- [ ] **Step 3: Write `internal/view/taints.go`**

```go
package view

import (
	"context"
	"fmt"
	"io"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/PixiBixi/kubectl-klens/internal/kube"
)

// Taints lists each node's taints as key=value:effect, comma-joined.
func Taints(ctx context.Context, c kubernetes.Interface, f kube.Flags, args []string, out io.Writer) error {
	nodes, err := c.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return err
	}
	t := kube.NewTable(out, "NAME", "TAINTS")
	for _, n := range nodes.Items {
		var ts []string
		for _, taint := range n.Spec.Taints {
			ts = append(ts, fmt.Sprintf("%s=%s:%s", taint.Key, taint.Value, taint.Effect))
		}
		val := strings.Join(ts, ",")
		if val == "" {
			val = "<none>"
		}
		t.Row(n.Name, val)
	}
	return t.Flush()
}
```

- [ ] **Step 4: Run, expect pass, commit**

```bash
go test ./internal/view/ -run TestTaints   # expect PASS
git add internal/view/taints.go internal/view/taints_test.go
git commit -m "feat(view): taints subcommand"
```

---

## Task 5: `capacity` subcommand

**Files:**
- Create: `internal/view/capacity.go`, `internal/view/capacity_test.go`

- [ ] **Step 1: Write the failing test `internal/view/capacity_test.go`**

```go
package view

import (
	"bytes"
	"context"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/PixiBixi/kubectl-klens/internal/kube"
)

func TestCapacity(t *testing.T) {
	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "n1"},
		Status: corev1.NodeStatus{
			Capacity: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("4"),
				corev1.ResourceMemory: resource.MustParse("16Gi"),
			},
			Allocatable: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("3920m"),
				corev1.ResourceMemory: resource.MustParse("14Gi"),
			},
		},
	}
	c := fake.NewClientset(node)
	var buf bytes.Buffer
	if err := Capacity(context.Background(), c, kube.Flags{}, nil, &buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{"CPU_CAP", "n1", "4", "16Gi", "3920m", "14Gi"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q:\n%s", want, out)
		}
	}
}
```

- [ ] **Step 2: Run, expect fail**

Run: `go test ./internal/view/ -run TestCapacity`
Expected: `undefined: Capacity`.

- [ ] **Step 3: Write `internal/view/capacity.go`**

```go
package view

import (
	"context"
	"io"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/PixiBixi/kubectl-klens/internal/kube"
)

// Capacity shows CPU/memory capacity and allocatable per node.
func Capacity(ctx context.Context, c kubernetes.Interface, f kube.Flags, args []string, out io.Writer) error {
	nodes, err := c.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return err
	}
	t := kube.NewTable(out, "NAME", "CPU_CAP", "CPU_ALLOC", "MEM_CAP", "MEM_ALLOC")
	for _, n := range nodes.Items {
		cap, alloc := n.Status.Capacity, n.Status.Allocatable
		t.Row(
			n.Name,
			qtyOrNone(cap, corev1.ResourceCPU),
			qtyOrNone(alloc, corev1.ResourceCPU),
			qtyOrNone(cap, corev1.ResourceMemory),
			qtyOrNone(alloc, corev1.ResourceMemory),
		)
	}
	return t.Flush()
}
```

- [ ] **Step 4: Run, expect pass, commit**

```bash
go test ./internal/view/ -run TestCapacity   # expect PASS
git add internal/view/capacity.go internal/view/capacity_test.go
git commit -m "feat(view): capacity subcommand"
```

---

## Task 6: `zones` subcommand

**Files:**
- Create: `internal/view/zones.go`, `internal/view/zones_test.go`

- [ ] **Step 1: Write the failing test `internal/view/zones_test.go`**

```go
package view

import (
	"bytes"
	"context"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/PixiBixi/kubectl-klens/internal/kube"
)

func TestZones(t *testing.T) {
	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "n1",
			Labels: map[string]string{
				"topology.kubernetes.io/region": "us-west1",
				"topology.kubernetes.io/zone":   "us-west1-a",
			},
		},
	}
	c := fake.NewClientset(node)
	var buf bytes.Buffer
	if err := Zones(context.Background(), c, kube.Flags{}, nil, &buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{"REGION", "ZONE", "us-west1", "us-west1-a"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q:\n%s", want, out)
		}
	}
}
```

- [ ] **Step 2: Run, expect fail**

Run: `go test ./internal/view/ -run TestZones`
Expected: `undefined: Zones`.

- [ ] **Step 3: Write `internal/view/zones.go`**

```go
package view

import (
	"context"
	"io"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/PixiBixi/kubectl-klens/internal/kube"
)

// Zones shows the region and zone topology labels per node.
func Zones(ctx context.Context, c kubernetes.Interface, f kube.Flags, args []string, out io.Writer) error {
	nodes, err := c.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return err
	}
	t := kube.NewTable(out, "NAME", "REGION", "ZONE")
	for _, n := range nodes.Items {
		t.Row(
			n.Name,
			kube.Label(n.Labels, "topology.kubernetes.io/region"),
			kube.Label(n.Labels, "topology.kubernetes.io/zone"),
		)
	}
	return t.Flush()
}
```

- [ ] **Step 4: Run, expect pass, commit**

```bash
go test ./internal/view/ -run TestZones   # expect PASS
git add internal/view/zones.go internal/view/zones_test.go
git commit -m "feat(view): zones subcommand"
```

---

## Task 7: `pods-per-node` subcommand

**Files:**
- Create: `internal/view/podspernode.go`, `internal/view/podspernode_test.go`

- [ ] **Step 1: Write the failing test `internal/view/podspernode_test.go`**

```go
package view

import (
	"bytes"
	"context"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/PixiBixi/kubectl-klens/internal/kube"
)

func pod(name, ns, node string) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
		Spec:       corev1.PodSpec{NodeName: node},
	}
}

func TestPodsPerNode(t *testing.T) {
	c := fake.NewClientset(
		pod("a", "default", "n1"),
		pod("b", "default", "n1"),
		pod("c", "default", "n2"),
	)
	var buf bytes.Buffer
	if err := PodsPerNode(context.Background(), c, kube.Flags{}, nil, &buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	// n1 has 2 pods and must be listed before n2 (sorted desc by count).
	if !strings.Contains(out, "n1") || !strings.Contains(out, "2") {
		t.Fatalf("missing n1 count:\n%s", out)
	}
	if strings.Index(out, "n1") > strings.Index(out, "n2") {
		t.Fatalf("n1 (2 pods) should sort before n2 (1):\n%s", out)
	}
}
```

- [ ] **Step 2: Run, expect fail**

Run: `go test ./internal/view/ -run TestPodsPerNode`
Expected: `undefined: PodsPerNode`.

- [ ] **Step 3: Write `internal/view/podspernode.go`**

```go
package view

import (
	"context"
	"io"
	"sort"
	"strconv"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/PixiBixi/kubectl-klens/internal/kube"
)

// PodsPerNode counts pods grouped by node, sorted by count descending.
func PodsPerNode(ctx context.Context, c kubernetes.Interface, f kube.Flags, args []string, out io.Writer) error {
	pods, err := c.CoreV1().Pods(f.NamespaceScope()).List(ctx, metav1.ListOptions{})
	if err != nil {
		return err
	}
	counts := map[string]int{}
	for _, p := range pods.Items {
		node := p.Spec.NodeName
		if node == "" {
			node = "<unscheduled>"
		}
		counts[node]++
	}
	type entry struct {
		node string
		n    int
	}
	list := make([]entry, 0, len(counts))
	for node, n := range counts {
		list = append(list, entry{node, n})
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].n != list[j].n {
			return list[i].n > list[j].n
		}
		return list[i].node < list[j].node
	})
	t := kube.NewTable(out, "NODE", "PODS")
	for _, e := range list {
		t.Row(e.node, strconv.Itoa(e.n))
	}
	return t.Flush()
}
```

- [ ] **Step 4: Run, expect pass, commit**

```bash
go test ./internal/view/ -run TestPodsPerNode   # expect PASS
git add internal/view/podspernode.go internal/view/podspernode_test.go
git commit -m "feat(view): pods-per-node subcommand"
```

---

## Task 8: `reqlim` subcommand

**Files:**
- Create: `internal/view/reqlim.go`, `internal/view/reqlim_test.go`

- [ ] **Step 1: Write the failing test `internal/view/reqlim_test.go`**

```go
package view

import (
	"bytes"
	"context"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/PixiBixi/kubectl-klens/internal/kube"
)

func TestReqlim(t *testing.T) {
	app := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "app", Namespace: "prod"},
		Spec: corev1.PodSpec{Containers: []corev1.Container{{
			Name: "main",
			Resources: corev1.ResourceRequirements{
				Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("100m")},
				Limits:   corev1.ResourceList{corev1.ResourceMemory: resource.MustParse("256Mi")},
			},
		}}},
	}
	sys := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "kube-proxy", Namespace: "kube-system"},
		Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "kube-proxy"}}},
	}
	c := fake.NewClientset(app, sys)
	var buf bytes.Buffer
	if err := Reqlim(context.Background(), c, kube.Flags{}, nil, &buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "100m") || !strings.Contains(out, "256Mi") || !strings.Contains(out, "none") {
		t.Fatalf("missing values:\n%s", out)
	}
	if strings.Contains(out, "kube-proxy") {
		t.Fatalf("kube-system must be excluded:\n%s", out)
	}
}
```

- [ ] **Step 2: Run, expect fail**

Run: `go test ./internal/view/ -run TestReqlim`
Expected: `undefined: Reqlim`.

- [ ] **Step 3: Write `internal/view/reqlim.go`**

```go
package view

import (
	"context"
	"io"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/PixiBixi/kubectl-klens/internal/kube"
)

// Reqlim shows per-container requests/limits for all pods except kube-system.
func Reqlim(ctx context.Context, c kubernetes.Interface, f kube.Flags, args []string, out io.Writer) error {
	pods, err := c.CoreV1().Pods(f.NamespaceScope()).List(ctx, metav1.ListOptions{})
	if err != nil {
		return err
	}
	t := kube.NewTable(out, "NS", "POD", "CONTAINER", "REQ_CPU", "LIM_CPU", "REQ_MEM", "LIM_MEM")
	for _, p := range pods.Items {
		if p.Namespace == "kube-system" {
			continue
		}
		for _, ctr := range p.Spec.Containers {
			req, lim := ctr.Resources.Requests, ctr.Resources.Limits
			t.Row(
				p.Namespace, p.Name, ctr.Name,
				qtyOrNone(req, corev1.ResourceCPU),
				qtyOrNone(lim, corev1.ResourceCPU),
				qtyOrNone(req, corev1.ResourceMemory),
				qtyOrNone(lim, corev1.ResourceMemory),
			)
		}
	}
	return t.Flush()
}
```

- [ ] **Step 4: Run, expect pass, commit**

```bash
go test ./internal/view/ -run TestReqlim   # expect PASS
git add internal/view/reqlim.go internal/view/reqlim_test.go
git commit -m "feat(view): reqlim subcommand (requests/limits, excl kube-system)"
```

---

## Task 9: `images` subcommand

**Files:**
- Create: `internal/view/images.go`, `internal/view/images_test.go`

- [ ] **Step 1: Write the failing test `internal/view/images_test.go`**

```go
package view

import (
	"bytes"
	"context"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/PixiBixi/kubectl-klens/internal/kube"
)

func podImg(name, image string) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
		Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "c", Image: image}}},
	}
}

func TestImages(t *testing.T) {
	c := fake.NewClientset(
		podImg("a", "nginx:1.25"),
		podImg("b", "nginx:1.25"),
		podImg("c", "redis:7"),
	)
	var buf bytes.Buffer
	if err := Images(context.Background(), c, kube.Flags{}, nil, &buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "nginx:1.25") || !strings.Contains(out, "redis:7") {
		t.Fatalf("missing images:\n%s", out)
	}
	// nginx (2) must sort before redis (1).
	if strings.Index(out, "nginx:1.25") > strings.Index(out, "redis:7") {
		t.Fatalf("nginx (2) should sort before redis (1):\n%s", out)
	}
}
```

- [ ] **Step 2: Run, expect fail**

Run: `go test ./internal/view/ -run TestImages`
Expected: `undefined: Images`.

- [ ] **Step 3: Write `internal/view/images.go`**

```go
package view

import (
	"context"
	"io"
	"sort"
	"strconv"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/PixiBixi/kubectl-klens/internal/kube"
)

// Images counts container image occurrences across pods, sorted desc.
func Images(ctx context.Context, c kubernetes.Interface, f kube.Flags, args []string, out io.Writer) error {
	pods, err := c.CoreV1().Pods(f.NamespaceScope()).List(ctx, metav1.ListOptions{})
	if err != nil {
		return err
	}
	counts := map[string]int{}
	for _, p := range pods.Items {
		for _, ctr := range p.Spec.Containers {
			counts[ctr.Image]++
		}
	}
	type entry struct {
		image string
		n     int
	}
	list := make([]entry, 0, len(counts))
	for image, n := range counts {
		list = append(list, entry{image, n})
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].n != list[j].n {
			return list[i].n > list[j].n
		}
		return list[i].image < list[j].image
	})
	t := kube.NewTable(out, "COUNT", "IMAGE")
	for _, e := range list {
		t.Row(strconv.Itoa(e.n), e.image)
	}
	return t.Flush()
}
```

- [ ] **Step 4: Run, expect pass, commit**

```bash
go test ./internal/view/ -run TestImages   # expect PASS
git add internal/view/images.go internal/view/images_test.go
git commit -m "feat(view): images subcommand"
```

---

## Task 10: `on-node` subcommand

**Files:**
- Create: `internal/view/onnode.go`, `internal/view/onnode_test.go`

> The fake clientset does NOT honor `FieldSelector`. We pass the selector for
> server-side efficiency in real clusters AND filter client-side so the result
> is correct under the fake and against any server that ignores the selector.

- [ ] **Step 1: Write the failing test `internal/view/onnode_test.go`**

```go
package view

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"k8s.io/client-go/kubernetes/fake"

	"github.com/PixiBixi/kubectl-klens/internal/kube"
)

func TestOnNodeRequiresArg(t *testing.T) {
	c := fake.NewClientset()
	var buf bytes.Buffer
	err := OnNode(context.Background(), c, kube.Flags{}, nil, &buf)
	if err == nil || !strings.Contains(err.Error(), "requires a node") {
		t.Fatalf("expected node-required error, got %v", err)
	}
}

func TestOnNodeFilters(t *testing.T) {
	c := fake.NewClientset(
		pod("a", "default", "n1"),
		pod("b", "default", "n2"),
	)
	var buf bytes.Buffer
	if err := OnNode(context.Background(), c, kube.Flags{}, []string{"n1"}, &buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "a") {
		t.Fatalf("pod a (on n1) should be listed:\n%s", out)
	}
	if strings.Contains(out, "\nb\t") || strings.Contains(out, " b ") {
		t.Fatalf("pod b (on n2) must not be listed:\n%s", out)
	}
}
```

- [ ] **Step 2: Run, expect fail**

Run: `go test ./internal/view/ -run TestOnNode`
Expected: `undefined: OnNode`.

- [ ] **Step 3: Write `internal/view/onnode.go`**

```go
package view

import (
	"context"
	"fmt"
	"io"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/client-go/kubernetes"

	"github.com/PixiBixi/kubectl-klens/internal/kube"
)

// OnNode lists pods scheduled on the given node.
func OnNode(ctx context.Context, c kubernetes.Interface, f kube.Flags, args []string, out io.Writer) error {
	if len(args) < 1 || args[0] == "" {
		return fmt.Errorf("on-node requires a node name: kubectl klens on-node <node>")
	}
	node := args[0]
	pods, err := c.CoreV1().Pods(f.NamespaceScope()).List(ctx, metav1.ListOptions{
		FieldSelector: fields.OneTermEqualSelector("spec.nodeName", node).String(),
	})
	if err != nil {
		return err
	}
	t := kube.NewTable(out, "NS", "POD", "STATUS", "NODE")
	for _, p := range pods.Items {
		if p.Spec.NodeName != node {
			continue // defensive: fake clientset ignores FieldSelector
		}
		t.Row(p.Namespace, p.Name, string(p.Status.Phase), p.Spec.NodeName)
	}
	return t.Flush()
}
```

- [ ] **Step 4: Run, expect pass, commit**

```bash
go test ./internal/view/ -run TestOnNode   # expect PASS
git add internal/view/onnode.go internal/view/onnode_test.go
git commit -m "feat(view): on-node subcommand"
```

---

## Task 11: `pvc` subcommand

**Files:**
- Create: `internal/view/pvc.go`, `internal/view/pvc_test.go`

- [ ] **Step 1: Write the failing test `internal/view/pvc_test.go`**

```go
package view

import (
	"bytes"
	"context"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/PixiBixi/kubectl-klens/internal/kube"
)

func TestPvc(t *testing.T) {
	withPVC := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "db", Namespace: "data"},
		Spec: corev1.PodSpec{
			NodeName: "n1",
			Volumes: []corev1.Volume{{
				Name: "store",
				VolumeSource: corev1.VolumeSource{
					PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: "db-data"},
				},
			}},
		},
	}
	noPVC := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "data"},
		Spec: corev1.PodSpec{
			NodeName: "n1",
			Volumes:  []corev1.Volume{{Name: "tmp", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}}},
		},
	}
	c := fake.NewClientset(withPVC, noPVC)
	var buf bytes.Buffer
	if err := Pvc(context.Background(), c, kube.Flags{}, nil, &buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "db-data") || !strings.Contains(out, "n1") {
		t.Fatalf("missing pvc binding:\n%s", out)
	}
	if strings.Contains(out, "web") {
		t.Fatalf("pod without a PVC must not appear:\n%s", out)
	}
}
```

- [ ] **Step 2: Run, expect fail**

Run: `go test ./internal/view/ -run TestPvc`
Expected: `undefined: Pvc`.

- [ ] **Step 3: Write `internal/view/pvc.go`**

```go
package view

import (
	"context"
	"io"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/PixiBixi/kubectl-klens/internal/kube"
)

// Pvc lists PVCs bound to a pod together with the pod's node.
func Pvc(ctx context.Context, c kubernetes.Interface, f kube.Flags, args []string, out io.Writer) error {
	pods, err := c.CoreV1().Pods(f.NamespaceScope()).List(ctx, metav1.ListOptions{})
	if err != nil {
		return err
	}
	t := kube.NewTable(out, "NS", "POD", "NODE", "PVC")
	for _, p := range pods.Items {
		for _, vol := range p.Spec.Volumes {
			if vol.PersistentVolumeClaim != nil {
				t.Row(p.Namespace, p.Name, p.Spec.NodeName, vol.PersistentVolumeClaim.ClaimName)
			}
		}
	}
	return t.Flush()
}
```

- [ ] **Step 4: Run, expect pass, commit**

```bash
go test ./internal/view/ -run TestPvc   # expect PASS
git add internal/view/pvc.go internal/view/pvc_test.go
git commit -m "feat(view): pvc subcommand"
```

---

## Task 12: CLI dispatch

**Files:**
- Create: `internal/cli/cli.go`, `internal/cli/cli_test.go`

- [ ] **Step 1: Write the failing test `internal/cli/cli_test.go`**

```go
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
```

- [ ] **Step 2: Run, expect fail**

Run: `go test ./internal/cli/`
Expected: build error `undefined: App` / `undefined: commands`.

- [ ] **Step 3: Write `internal/cli/cli.go`**

```go
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
```

- [ ] **Step 4: Run, expect pass, commit**

```bash
go test ./internal/cli/   # expect PASS
git add internal/cli
git commit -m "feat(cli): subcommand dispatch, flags and usage"
```

---

## Task 13: `main.go` wiring

**Files:**
- Create: `main.go`, `main_test.go`

- [ ] **Step 1: Write `main.go`**

```go
package main

import (
	"os"

	"github.com/PixiBixi/kubectl-klens/internal/cli"
)

var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	app := cli.NewApp(cli.BuildInfo{Version: version, Commit: commit, Date: date})
	os.Exit(app.Run(os.Args[1:]))
}
```

- [ ] **Step 2: Write `main_test.go` (compile smoke test)**

```go
package main

import "testing"

// TestBuildVarsDefault guards against accidental removal of the ldflags vars.
func TestBuildVarsDefault(t *testing.T) {
	if version == "" || commit == "" || date == "" {
		t.Fatal("build vars must have non-empty defaults")
	}
}
```

- [ ] **Step 3: Build + run full test suite, commit**

```bash
go build -o /dev/null .        # expect success
go test ./...                  # expect PASS
git add main.go main_test.go
git commit -m "feat: wire main entrypoint"
```

---

## Task 14: Release tooling (goreleaser, workflows, krew manifest, Makefile)

**Files:**
- Create: `.goreleaser.yml`, `.github/workflows/ci.yml`, `.github/workflows/release.yml`, `.krew.yaml`, `Makefile`

- [ ] **Step 1: Write `.goreleaser.yml`**

```yaml
version: 2

before:
  hooks:
    - go mod tidy
    - go mod verify

builds:
  - id: kubectl-klens
    binary: kubectl-klens
    env:
      - CGO_ENABLED=0
    goos: [linux, darwin]
    goarch: [amd64, arm64]
    ldflags:
      - -s -w
      - -X main.version={{.Version}}
      - -X main.commit={{.Commit}}
      - -X main.date={{.Date}}

archives:
  - id: default
    formats: [tar.gz]
    name_template: >-
      {{ .ProjectName }}_
      {{- .Version }}_
      {{- title .Os }}_
      {{- if eq .Arch "amd64" }}x86_64
      {{- else }}{{ .Arch }}{{ end }}
    files:
      - README.md
      - LICENSE*

checksum:
  name_template: 'checksums.txt'

snapshot:
  version_template: "{{ incpatch .Version }}-next"

changelog:
  sort: asc
  filters:
    exclude: ['^docs:', '^test:', '^chore:', 'typo']
  groups:
    - title: Features
      regexp: '^feat'
      order: 0
    - title: Bug Fixes
      regexp: '^fix'
      order: 1
    - title: Others
      order: 999

release:
  github:
    owner: PixiBixi
    name: kubectl-klens
  name_template: "{{.ProjectName}} {{.Version}}"
  draft: false
  prerelease: auto
  mode: replace
```

- [ ] **Step 2: Write `.github/workflows/ci.yml`**

```yaml
name: CI

on:
  push:
    branches: [main, master]
  pull_request:
    branches: [main, master]

jobs:
  test:
    name: Test
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version: '1.26'
      - name: Verify dependencies
        run: go mod verify
      - name: Build
        run: go build -v ./...
      - name: go vet
        run: go vet ./...
      - name: Install staticcheck
        run: go install honnef.co/go/tools/cmd/staticcheck@latest
      - name: staticcheck
        run: staticcheck ./...
      - name: Tests
        run: go test -race ./...
```

- [ ] **Step 3: Write `.github/workflows/release.yml`**

```yaml
name: Release

on:
  push:
    tags:
      - 'v*'

permissions:
  contents: write

jobs:
  goreleaser:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
        with:
          fetch-depth: 0
      - uses: actions/setup-go@v5
        with:
          go-version: stable
      - name: Run GoReleaser
        uses: goreleaser/goreleaser-action@v6
        with:
          distribution: goreleaser
          version: '~> v2'
          args: release --clean
        env:
          GITHUB_TOKEN: ${{ secrets.GITHUB_TOKEN }}

      # Phase 2 — upstream to krew-index. Disabled until we decide to publish.
      # Enable by removing `if: false` and adding a KREW_RELEASE_BOT token if needed.
      - name: Update krew-index
        if: false
        uses: rajatjindal/krew-release-bot@v0.0.46
```

- [ ] **Step 4: Write `.krew.yaml`**

```yaml
apiVersion: krew.googlecontainertools.github.com/v1alpha2
kind: Plugin
metadata:
  name: klens
spec:
  version: "{{ .TagName }}"
  homepage: https://github.com/PixiBixi/kubectl-klens
  shortDescription: Quick read-only cluster inspection shortcuts
  description: |
    klens bundles frequently-used read-only inspection commands: node pools,
    taints, capacity, zones, pods-per-node, requests/limits, image counts,
    pods on a node, and PVC bindings.
  platforms:
    - selector:
        matchLabels:
          os: darwin
          arch: amd64
      {{addURIAndSha "https://github.com/PixiBixi/kubectl-klens/releases/download/{{ .TagName }}/kubectl-klens_{{ .TagName }}_Darwin_x86_64.tar.gz" .TagName }}
      bin: kubectl-klens
    - selector:
        matchLabels:
          os: darwin
          arch: arm64
      {{addURIAndSha "https://github.com/PixiBixi/kubectl-klens/releases/download/{{ .TagName }}/kubectl-klens_{{ .TagName }}_Darwin_arm64.tar.gz" .TagName }}
      bin: kubectl-klens
    - selector:
        matchLabels:
          os: linux
          arch: amd64
      {{addURIAndSha "https://github.com/PixiBixi/kubectl-klens/releases/download/{{ .TagName }}/kubectl-klens_{{ .TagName }}_Linux_x86_64.tar.gz" .TagName }}
      bin: kubectl-klens
    - selector:
        matchLabels:
          os: linux
          arch: arm64
      {{addURIAndSha "https://github.com/PixiBixi/kubectl-klens/releases/download/{{ .TagName }}/kubectl-klens_{{ .TagName }}_Linux_arm64.tar.gz" .TagName }}
      bin: kubectl-klens
```

- [ ] **Step 5: Write `Makefile`**

```makefile
.PHONY: build lint test snapshot clean

build:
	go build -ldflags "-s -w" -o kubectl-klens .

lint:
	go vet ./...
	staticcheck ./...

test:
	go test -race ./...

snapshot:
	goreleaser release --snapshot --clean

clean:
	rm -f kubectl-klens
	rm -rf dist/
```

- [ ] **Step 6: Validate config locally, commit**

```bash
goreleaser check                       # expect: config is valid
goreleaser release --snapshot --clean  # expect: dist/ archives built
git add .goreleaser.yml .github/workflows .krew.yaml Makefile
git commit -m "ci: goreleaser, CI/release workflows, krew manifest, Makefile"
```

---

## Task 15: Repo meta + final verification

**Files:**
- Create: `README.md`, `LICENSE`, `CHANGELOG.md`, `.gitignore`, `.pre-commit-config.yaml`

- [ ] **Step 1: Write `.gitignore`**

```gitignore
/kubectl-klens
/dist/
*.tar.gz
checksums.txt
```

- [ ] **Step 2: Write `LICENSE`**

MIT License, copyright `2026 Jeremy Delgado`. (Copy the standard MIT text.)

- [ ] **Step 3: Write `CHANGELOG.md`**

```markdown
# Changelog

All notable changes are generated from conventional commits by GoReleaser on
each tagged release. See the GitHub Releases page.
```

- [ ] **Step 4: Write `README.md`**

````markdown
# kubectl-klens

A kubectl plugin for quick, read-only cluster inspection. One dispatcher,
nine shortcuts.

## Install (krew, personal)

```bash
kubectl krew install --manifest=.krew.yaml --archive=dist/kubectl-klens_<ver>_<os>_<arch>.tar.gz
```

Or download a release archive, extract `kubectl-klens` onto your `PATH`, and
invoke it as `kubectl klens`.

## Usage

```bash
kubectl klens nodes            # nodes + GKE nodepool + instance-type
kubectl klens taints           # taints per node
kubectl klens capacity         # CPU/mem capacity + allocatable
kubectl klens zones            # region/zone per node
kubectl klens pods-per-node    # pod count per node
kubectl klens reqlim           # requests/limits per container (excl kube-system)
kubectl klens images           # image occurrence counts
kubectl klens on-node <node>   # pods on a node
kubectl klens pvc              # PVCs bound to pod + node
```

Flags: `--kubeconfig`, `--context`, `-n/--namespace`, `-A/--all-namespaces`,
`--version`.

## Development

```bash
make test      # go test -race ./...
make lint      # go vet + staticcheck
make build     # local binary
make snapshot  # goreleaser dry-run
```
````

- [ ] **Step 5: Write `.pre-commit-config.yaml`**

```yaml
repos:
  - repo: https://github.com/dnephin/pre-commit-golang
    rev: v0.5.1
    hooks:
      - id: go-fmt
      - id: go-vet
```

- [ ] **Step 6: Final full verification**

```bash
go mod tidy
go vet ./...
go install honnef.co/go/tools/cmd/staticcheck@latest && staticcheck ./...
go test -race ./...        # expect all PASS
go build -o kubectl-klens . && ./kubectl-klens --help   # prints usage
```

- [ ] **Step 7: Commit**

```bash
git add README.md LICENSE CHANGELOG.md .gitignore .pre-commit-config.yaml
git commit -m "docs: README, license, changelog, gitignore, pre-commit"
```

---

## Self-Review

- **Spec coverage:** distribution (Task 14 krew + release), stack/client-go (Tasks 1,3-11), stdlib flag dispatch (Task 12), tabwriter (Task 1), all 9 subcommands (Tasks 3-11), global flags + behavior (Task 12), fake-clientset tests (every view + cli task), staticcheck/test-race CI (Task 14), goreleaser archives + changelog + krew manifest (Task 14), versioning ldflags (Tasks 13,14). No gaps.
- **Placeholder scan:** LICENSE references "standard MIT text" — acceptable (well-known boilerplate). No TBD/TODO in code.
- **Type consistency:** every `view.*` func matches `RunFunc` `(ctx, kubernetes.Interface, kube.Flags, []string, io.Writer) error`; registry in Task 12 references exactly the funcs defined in Tasks 3-11; `kube.Flags`, `kube.NewTable`, `kube.Label`, `qtyOrNone`, `nodeStatus`, `pod()` helper used consistently.
