package view

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"regexp"
	"slices"
	"strconv"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/yaml"

	"github.com/PixiBixi/kubectl-klens/internal/kube"
)

// Autoscaler reads the cluster-autoscaler-status ConfigMap from kube-system and
// renders a cluster-wide summary line plus a per-nodegroup table. It accepts
// both the structured-YAML status (cluster-autoscaler 1.30+) and the older
// legacy text status, falling back to printing the raw value verbatim when
// neither format is recognized.
func Autoscaler(ctx context.Context, c kubernetes.Interface, f kube.Flags, args []string, out io.Writer) error {
	cm, err := c.CoreV1().ConfigMaps("kube-system").Get(ctx, "cluster-autoscaler-status", metav1.GetOptions{})
	if err != nil {
		return err
	}
	status, ok := cm.Data["status"]
	if !ok {
		return errors.New("configmap cluster-autoscaler-status has no \"status\" field")
	}
	return renderAutoscalerStatus(status, f.Sort, kube.NewPainter(f), out)
}

type caClusterWide struct {
	timestamp, health, scaleUp, scaleDown, ready, registered string
}

type caGroup struct {
	name, health, ready, target, min, max, scaleUp, scaleDown, lastChange string
}

// renderAutoscalerStatus parses the status into a normalized model and writes a
// summary line and nodegroup table, or echoes the input verbatim when neither
// the YAML nor the legacy text format is recognized.
func renderAutoscalerStatus(status, sortCol string, paint kube.Painter, out io.Writer) error {
	cw, groups, ok := parseAutoscalerStatus(status)
	if !ok {
		fmt.Fprintln(out, status)
		return nil
	}
	fmt.Fprintln(out, clusterWideSummary(cw, paint))
	if len(groups) == 0 {
		return nil
	}
	// Default ordering: most recently changed nodegroup first. An explicit
	// --sort (applied by SortBy at Flush) overrides this.
	slices.SortStableFunc(groups, func(a, b caGroup) int {
		return cmp.Or(
			cmp.Compare(b.lastChange, a.lastChange), // most recent first
			cmp.Compare(a.name, b.name),
		)
	})
	fmt.Fprintln(out)
	t := kube.NewTable(out, paint, "NODEGROUP", "HEALTH", "READY", "TARGET", "MIN", "MAX", "SCALEUP", "SCALEDOWN", "LAST-CHANGE")
	for _, g := range groups {
		t.Row(g.name, health(paint, dash(g.health)), dash(g.ready), dash(g.target), dash(g.min), dash(g.max),
			scaleState(paint, dash(g.scaleUp)), scaleState(paint, dash(g.scaleDown)), paint.Muted(dash(g.lastChange)))
	}
	t.SortBy(sortCol)
	return t.Flush()
}

func clusterWideSummary(cw caClusterWide, paint kube.Painter) string {
	var b strings.Builder
	b.WriteString("Cluster-wide: ")
	if cw.health != "" {
		b.WriteString(health(paint, cw.health))
	} else {
		b.WriteString("Unknown")
	}
	if cw.scaleUp != "" {
		b.WriteString("  scaleUp=" + scaleState(paint, cw.scaleUp))
	}
	if cw.scaleDown != "" {
		b.WriteString("  scaleDown=" + scaleState(paint, cw.scaleDown))
	}
	if cw.ready != "" {
		registered := cw.registered
		if registered == "" {
			registered = cw.ready
		}
		fmt.Fprintf(&b, "  (ready %s/%s)", cw.ready, registered)
	}
	if cw.timestamp != "" {
		b.WriteString("  @ " + cw.timestamp)
	}
	return b.String()
}

// parseAutoscalerStatus tries the structured-YAML format first, then the legacy
// text format. The bool reports whether either yielded something renderable.
func parseAutoscalerStatus(status string) (caClusterWide, []caGroup, bool) {
	if cw, groups, ok := parseYAMLStatus(status); ok {
		return cw, groups, true
	}
	cw, groups := parseLegacyStatus(status)
	return cw, groups, cw.health != "" || len(groups) > 0
}

// --- structured YAML format (cluster-autoscaler 1.30+) ---

type caYAMLStatus struct {
	Time             string        `json:"time"`
	AutoscalerStatus string        `json:"autoscalerStatus"`
	ClusterWide      caYAMLSection `json:"clusterWide"`
	NodeGroups       []caYAMLGroup `json:"nodeGroups"`
}

type caYAMLSection struct {
	Health    *caYAMLCondition `json:"health"`
	ScaleUp   *caYAMLCondition `json:"scaleUp"`
	ScaleDown *caYAMLCondition `json:"scaleDown"`
}

type caYAMLGroup struct {
	Name      string           `json:"name"`
	Health    *caYAMLCondition `json:"health"`
	ScaleUp   *caYAMLCondition `json:"scaleUp"`
	ScaleDown *caYAMLCondition `json:"scaleDown"`
}

type caYAMLCondition struct {
	Status              string `json:"status"`
	LastTransitionTime  string `json:"lastTransitionTime"`
	CloudProviderTarget int    `json:"cloudProviderTarget"`
	MinSize             int    `json:"minSize"`
	MaxSize             int    `json:"maxSize"`
	NodeCounts          struct {
		Registered struct {
			Total int `json:"total"`
			Ready int `json:"ready"`
		} `json:"registered"`
	} `json:"nodeCounts"`
}

func parseYAMLStatus(status string) (caClusterWide, []caGroup, bool) {
	var s caYAMLStatus
	if err := yaml.Unmarshal([]byte(status), &s); err != nil {
		return caClusterWide{}, nil, false
	}
	if s.AutoscalerStatus == "" && s.ClusterWide.Health == nil && len(s.NodeGroups) == 0 {
		return caClusterWide{}, nil, false
	}
	cw := caClusterWide{timestamp: s.Time}
	if h := s.ClusterWide.Health; h != nil {
		cw.health = h.Status
		cw.ready = strconv.Itoa(h.NodeCounts.Registered.Ready)
		cw.registered = strconv.Itoa(h.NodeCounts.Registered.Total)
	}
	if su := s.ClusterWide.ScaleUp; su != nil {
		cw.scaleUp = su.Status
	}
	if sd := s.ClusterWide.ScaleDown; sd != nil {
		cw.scaleDown = sd.Status
	}
	var groups []caGroup
	for _, ng := range s.NodeGroups {
		g := caGroup{name: shortName(ng.Name)}
		if h := ng.Health; h != nil {
			g.health = h.Status
			g.ready = strconv.Itoa(h.NodeCounts.Registered.Ready)
			g.target = strconv.Itoa(h.CloudProviderTarget)
			g.min = strconv.Itoa(h.MinSize)
			g.max = strconv.Itoa(h.MaxSize)
		}
		if su := ng.ScaleUp; su != nil {
			g.scaleUp = su.Status
		}
		if sd := ng.ScaleDown; sd != nil {
			g.scaleDown = sd.Status
		}
		g.lastChange = latestTransition(ng.Health, ng.ScaleUp, ng.ScaleDown)
		groups = append(groups, g)
	}
	return cw, groups, true
}

// --- legacy text format ---

var (
	caReady      = regexp.MustCompile(`\bready=(\d+)`)
	caTarget     = regexp.MustCompile(`\bcloudProviderTarget=(\d+)`)
	caMinSize    = regexp.MustCompile(`\bminSize=(\d+)`)
	caMaxSize    = regexp.MustCompile(`\bmaxSize=(\d+)`)
	caRegistered = regexp.MustCompile(`\bregistered=(\d+)`)
)

func parseLegacyStatus(status string) (caClusterWide, []caGroup) {
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
		cw = parseLegacyClusterWide(lines[cwStart:cwEnd])
	}
	cw.timestamp = autoscalerTimestamp(status)
	var groups []caGroup
	if ngStart >= 0 {
		groups = parseLegacyNodeGroups(lines[ngStart+1:])
	}
	return cw, groups
}

func parseLegacyClusterWide(lines []string) caClusterWide {
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

func parseLegacyNodeGroups(lines []string) []caGroup {
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
	for line := range strings.SplitSeq(status, "\n") {
		if _, after, ok := strings.Cut(line, marker); ok {
			return strings.TrimSuffix(strings.TrimSpace(after), ":")
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

// latestTransition returns the most recent lastTransitionTime among the given
// conditions (nil and empty skipped). The values are RFC3339 UTC strings of
// uniform shape, so lexical comparison orders them chronologically.
func latestTransition(conds ...*caYAMLCondition) string {
	var latest string
	for _, c := range conds {
		if c == nil || c.LastTransitionTime == "" {
			continue
		}
		if c.LastTransitionTime > latest {
			latest = c.LastTransitionTime
		}
	}
	return latest
}

func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// health colors a cluster-autoscaler health value: Healthy is good, anything
// else non-empty (e.g. Unhealthy) is bad. "-" and "" pass through.
func health(paint kube.Painter, status string) string {
	switch status {
	case "Healthy":
		return paint.OK(status)
	case "", "-":
		return status
	}
	return paint.Bad(status)
}

// scaleState colors a scale-up/scale-down state: in-progress activity is a
// warning; other states (NoActivity, NoCandidates, CandidatesPresent) are left
// plain.
func scaleState(paint kube.Painter, status string) string {
	if status == "InProgress" {
		return paint.Warn(status)
	}
	return status
}
