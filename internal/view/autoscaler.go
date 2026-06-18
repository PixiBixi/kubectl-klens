package view

import (
	"context"
	"fmt"
	"io"
	"regexp"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/PixiBixi/kubectl-klens/internal/kube"
)

// Autoscaler reads the cluster-autoscaler-status ConfigMap from kube-system and
// renders a cluster-wide summary line plus a per-nodegroup table. If the status
// field is not in the recognized legacy text format, it falls back to printing
// the raw value verbatim.
func Autoscaler(ctx context.Context, c kubernetes.Interface, f kube.Flags, args []string, out io.Writer) error {
	cm, err := c.CoreV1().ConfigMaps("kube-system").Get(ctx, "cluster-autoscaler-status", metav1.GetOptions{})
	if err != nil {
		return err
	}
	status, ok := cm.Data["status"]
	if !ok {
		return fmt.Errorf("configmap cluster-autoscaler-status has no \"status\" field")
	}
	renderAutoscalerStatus(status, out)
	return nil
}

type caClusterWide struct {
	health, scaleUp, scaleDown, ready, registered string
}

type caGroup struct {
	name, health, ready, target, min, max, scaleUp, scaleDown string
}

var (
	caReady      = regexp.MustCompile(`\bready=(\d+)`)
	caTarget     = regexp.MustCompile(`\bcloudProviderTarget=(\d+)`)
	caMinSize    = regexp.MustCompile(`\bminSize=(\d+)`)
	caMaxSize    = regexp.MustCompile(`\bmaxSize=(\d+)`)
	caRegistered = regexp.MustCompile(`\bregistered=(\d+)`)
)

// renderAutoscalerStatus parses the legacy text status and writes a summary
// line and nodegroup table, or echoes the input verbatim when it parses to
// nothing recognizable (e.g. the newer structured-YAML format).
func renderAutoscalerStatus(status string, out io.Writer) {
	cw, groups := parseAutoscalerStatus(status)
	if cw.health == "" && len(groups) == 0 {
		fmt.Fprintln(out, status)
		return
	}
	fmt.Fprintln(out, clusterWideSummary(cw, autoscalerTimestamp(status)))
	if len(groups) == 0 {
		return
	}
	fmt.Fprintln(out)
	t := kube.NewTable(out, "NODEGROUP", "HEALTH", "READY", "TARGET", "MIN", "MAX", "SCALEUP", "SCALEDOWN")
	for _, g := range groups {
		t.Row(g.name, dash(g.health), dash(g.ready), dash(g.target), dash(g.min), dash(g.max), dash(g.scaleUp), dash(g.scaleDown))
	}
	t.Flush()
}

func clusterWideSummary(cw caClusterWide, timestamp string) string {
	var b strings.Builder
	b.WriteString("Cluster-wide: ")
	if cw.health != "" {
		b.WriteString(cw.health)
	} else {
		b.WriteString("Unknown")
	}
	if cw.scaleUp != "" {
		b.WriteString("  scaleUp=" + cw.scaleUp)
	}
	if cw.scaleDown != "" {
		b.WriteString("  scaleDown=" + cw.scaleDown)
	}
	if cw.ready != "" {
		registered := cw.registered
		if registered == "" {
			registered = cw.ready
		}
		fmt.Fprintf(&b, "  (ready %s/%s)", cw.ready, registered)
	}
	if timestamp != "" {
		b.WriteString("  @ " + timestamp)
	}
	return b.String()
}

// parseAutoscalerStatus splits the status into its Cluster-wide and NodeGroups
// sections and extracts the health/scale states and counts from each.
func parseAutoscalerStatus(status string) (caClusterWide, []caGroup) {
	lines := strings.Split(status, "\n")
	cwStart, ngStart := -1, -1
	for i, line := range lines {
		switch strings.TrimSpace(line) {
		case "Cluster-wide:":
			cwStart = i
		case "NodeGroups:":
			ngStart = i
		}
	}
	cwEnd := len(lines)
	if ngStart >= 0 {
		cwEnd = ngStart
	}
	var cw caClusterWide
	if cwStart >= 0 {
		cw = parseClusterWide(lines[cwStart:cwEnd])
	}
	var groups []caGroup
	if ngStart >= 0 {
		groups = parseNodeGroups(lines[ngStart+1:])
	}
	return cw, groups
}

func parseClusterWide(lines []string) caClusterWide {
	var cw caClusterWide
	for _, line := range lines {
		t := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(t, "Health:"):
			cw.health = fieldAfter(t, "Health:")
			cw.ready = submatch(caReady, t)
			cw.registered = submatch(caRegistered, t)
		case strings.HasPrefix(t, "ScaleUp:"):
			cw.scaleUp = fieldAfter(t, "ScaleUp:")
		case strings.HasPrefix(t, "ScaleDown:"):
			cw.scaleDown = fieldAfter(t, "ScaleDown:")
		}
	}
	return cw
}

func parseNodeGroups(lines []string) []caGroup {
	var groups []caGroup
	var cur caGroup
	started := false
	flush := func() {
		if started {
			groups = append(groups, cur)
		}
	}
	for _, line := range lines {
		t := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(t, "Name:"):
			flush()
			cur = caGroup{name: shortName(fieldAfter(t, "Name:"))}
			started = true
		case !started:
			// Skip anything before the first Name: line.
		case strings.HasPrefix(t, "Health:"):
			cur.health = fieldAfter(t, "Health:")
			cur.ready = submatch(caReady, t)
			cur.target = submatch(caTarget, t)
			cur.min = submatch(caMinSize, t)
			cur.max = submatch(caMaxSize, t)
		case strings.HasPrefix(t, "ScaleUp:"):
			cur.scaleUp = fieldAfter(t, "ScaleUp:")
		case strings.HasPrefix(t, "ScaleDown:"):
			cur.scaleDown = fieldAfter(t, "ScaleDown:")
		}
	}
	flush()
	return groups
}

// autoscalerTimestamp pulls the time off the "Cluster-autoscaler status at
// <time>:" header line, if present.
func autoscalerTimestamp(status string) string {
	const marker = "status at "
	for _, line := range strings.Split(status, "\n") {
		if i := strings.Index(line, marker); i >= 0 {
			return strings.TrimSuffix(strings.TrimSpace(line[i+len(marker):]), ":")
		}
	}
	return ""
}

// fieldAfter returns the first whitespace-delimited token following a label
// prefix, e.g. fieldAfter("Health:  Healthy (...)", "Health:") == "Healthy".
func fieldAfter(line, label string) string {
	fields := strings.Fields(strings.TrimPrefix(line, label))
	if len(fields) == 0 {
		return ""
	}
	return fields[0]
}

func submatch(re *regexp.Regexp, s string) string {
	if m := re.FindStringSubmatch(s); m != nil {
		return m[1]
	}
	return ""
}

// shortName trims a GKE instance-group URL down to its final path segment.
func shortName(name string) string {
	if i := strings.LastIndex(name, "/"); i >= 0 {
		return name[i+1:]
	}
	return name
}

func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
