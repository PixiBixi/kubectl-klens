package kube

import (
	"context"
	"fmt"
	"path"
	"slices"
	"strings"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// Scope is the resolved set of namespaces a command lists in. The zero value
// means every namespace, which is the single cluster-wide List the API server
// serves best; a non-empty set is fanned out one List per namespace.
//
// Callers get a Scope from Flags.Scope, never by building one from a raw -n
// value: a -n carrying a glob has to be expanded against the cluster first (see
// ResolveScope), and an unexpanded pattern would silently list a namespace
// literally named "be-*".
type Scope struct {
	names []string
}

// NamespaceSet returns a Scope over the given namespaces. Passing none is the
// cluster-wide scope.
func NamespaceSet(names ...string) Scope {
	return Scope{names: names}
}

// All reports whether the scope covers every namespace.
func (s Scope) All() bool { return len(s.names) == 0 }

// Len returns how many namespaces the scope names; 0 means all of them.
func (s Scope) Len() int { return len(s.names) }

// Names returns the namespaces in the scope, empty for cluster-wide. The slice
// is owned by the Scope; callers must not mutate it.
func (s Scope) Names() []string { return s.names }

// One returns the single namespace in the scope. It reports false for a
// cluster-wide scope and for a glob that matched several namespaces, which is
// what a Get (as opposed to a List) has to refuse.
func (s Scope) One() (string, bool) {
	if len(s.names) != 1 {
		return "", false
	}
	return s.names[0], true
}

// globChars are the path.Match metacharacters that turn a -n value from a
// namespace name into a pattern.
const globChars = "*?["

// IsPattern reports whether a -n value is a glob rather than a literal name.
// A namespace name is a DNS label, so none of these characters can appear in
// one: any occurrence is unambiguously a pattern.
func IsPattern(value string) bool { return strings.ContainsAny(value, globChars) }

// ResolveScope expands f.Namespace into f.Namespaces, validating it against the
// cluster. It is the only place a -n value is checked, and it is strict on
// purpose: silently printing an empty table for a typo'd namespace is the
// failure mode this replaces.
//
// A literal name costs one Get. A glob costs one namespace List, which needs
// cluster-wide list rights on namespaces - that is the price of the feature,
// and the error says so rather than falling back to an empty result.
func ResolveScope(ctx context.Context, c kubernetes.Interface, f *Flags) error {
	if f.AllNamespaces || f.Namespace == "" {
		return nil
	}
	if !IsPattern(f.Namespace) {
		if _, err := c.CoreV1().Namespaces().Get(ctx, f.Namespace, metav1.GetOptions{}); err != nil {
			if apierrors.IsNotFound(err) {
				// A regexp with no glob metacharacter ("zz.+", "^be") never
				// reaches the pattern branch: it is looked up as a literal name
				// and reported missing, which is true but unhelpful.
				return fmt.Errorf("namespace %q not found%s", f.Namespace, globHint(f.Namespace))
			}
			return fmt.Errorf("cannot verify namespace %q: %w", f.Namespace, err)
		}
		f.Namespaces = []string{f.Namespace}
		return nil
	}
	if _, err := path.Match(f.Namespace, ""); err != nil {
		return fmt.Errorf("invalid namespace pattern %q: %w", f.Namespace, err)
	}
	all, err := ListNamespaces(ctx, c, metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("cannot expand namespace pattern %q: %w", f.Namespace, err)
	}
	matched := make([]string, 0, 8)
	for i := range all {
		if ok, _ := path.Match(f.Namespace, all[i].Name); ok {
			matched = append(matched, all[i].Name)
		}
	}
	if len(matched) == 0 {
		return fmt.Errorf("no namespace matches %q%s", f.Namespace, globHint(f.Namespace))
	}
	slices.Sort(matched)
	f.Namespaces = matched
	return nil
}

// globHint explains a pattern that reads like a regexp. It is the mistake -n
// invites: path.Match treats "." as a literal, so "be.*" looks for namespaces
// whose name starts with "be." and matches nothing, with no hint as to why.
func globHint(pattern string) string {
	if !regexpish(pattern) {
		return ""
	}
	return fmt.Sprintf("; -n takes a shell glob, not a regexp (%q is literal) - did you mean %q?",
		".", asGlob(pattern))
}

func regexpish(pattern string) bool {
	return strings.Contains(pattern, ".*") || strings.Contains(pattern, ".+") ||
		strings.HasPrefix(pattern, "^") || strings.HasSuffix(pattern, "$")
}

// asGlob rewrites the regexp constructs globHint recognises into their glob
// equivalent, so the error can name the pattern the user meant.
func asGlob(pattern string) string {
	pattern = strings.TrimPrefix(pattern, "^")
	pattern = strings.TrimSuffix(pattern, "$")
	pattern = strings.ReplaceAll(pattern, ".*", "*")
	return strings.ReplaceAll(pattern, ".+", "?*")
}
