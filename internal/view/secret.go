package view

import (
	"context"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/manifoldco/promptui"
	"golang.org/x/term"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/duration"
	"k8s.io/client-go/kubernetes"

	"github.com/PixiBixi/kubectl-klens/internal/kube"
)

// Secret browses secrets. With a terminal it opens interactive pickers; when
// the output is piped it falls back to plain listings. Args short-circuit the
// pickers:
//
//	secret               pick a secret, then a key (list secrets when piped)
//	secret <name>        pick a key of <name> (list its keys when piped)
//	secret <name> <key>  print the decoded value of <key>
//	secret <name> all    print all decoded key/value pairs
func Secret(ctx context.Context, c kubernetes.Interface, f kube.Flags, args []string, out io.Writer) error {
	switch {
	case len(args) >= 2:
		s, err := getSecret(ctx, c, f.NamespaceScope(), args[0])
		if err != nil {
			return err
		}
		return emitValue(out, s, args[1])

	case len(args) == 1 && args[0] != "":
		s, err := getSecret(ctx, c, f.NamespaceScope(), args[0])
		if err != nil {
			return err
		}
		if !isTTY(out) {
			return emitKeys(out, s)
		}
		key, err := pickKey(s)
		if err != nil {
			return err
		}
		return emitValue(out, s, key)

	default:
		if !isTTY(out) {
			return listSecrets(ctx, c, f, out)
		}
		s, err := pickSecret(ctx, c, f)
		if err != nil {
			return err
		}
		key, err := pickKey(s)
		if err != nil {
			return err
		}
		return emitValue(out, s, key)
	}
}

func getSecret(ctx context.Context, c kubernetes.Interface, ns, name string) (*corev1.Secret, error) {
	return c.CoreV1().Secrets(ns).Get(ctx, name, metav1.GetOptions{})
}

func listSecrets(ctx context.Context, c kubernetes.Interface, f kube.Flags, out io.Writer) error {
	list, err := c.CoreV1().Secrets(f.NamespaceScope()).List(ctx, metav1.ListOptions{})
	if err != nil {
		return err
	}
	sortSecrets(list.Items)
	t := kube.NewTable(out, "NS", "NAME", "TYPE", "KEYS", "AGE")
	for _, s := range list.Items {
		age := duration.HumanDuration(time.Since(s.CreationTimestamp.Time))
		t.Row(s.Namespace, s.Name, string(s.Type), strconv.Itoa(len(s.Data)), age)
	}
	return t.Flush()
}

func emitKeys(out io.Writer, s *corev1.Secret) error {
	t := kube.NewTable(out, "KEY", "BYTES")
	for _, k := range sortedKeys(s.Data) {
		t.Row(k, strconv.Itoa(len(s.Data[k])))
	}
	return t.Flush()
}

func emitValue(out io.Writer, s *corev1.Secret, key string) error {
	if key == "all" {
		t := kube.NewTable(out, "KEY", "VALUE")
		for _, k := range sortedKeys(s.Data) {
			t.Row(k, string(s.Data[k]))
		}
		return t.Flush()
	}
	v, ok := s.Data[key]
	if !ok {
		return fmt.Errorf("key %q not found in secret %q", key, s.Name)
	}
	fmt.Fprintln(out, string(v))
	return nil
}

func pickSecret(ctx context.Context, c kubernetes.Interface, f kube.Flags) (*corev1.Secret, error) {
	list, err := c.CoreV1().Secrets(f.NamespaceScope()).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	if len(list.Items) == 0 {
		return nil, fmt.Errorf("no secrets found in the current scope")
	}
	sortSecrets(list.Items)
	allNS := f.NamespaceScope() == ""
	items := make([]string, len(list.Items))
	for i, s := range list.Items {
		if allNS {
			items[i] = s.Namespace + "/" + s.Name
		} else {
			items[i] = s.Name
		}
	}
	idx, _, err := (&promptui.Select{
		Label:    "Select secret",
		Items:    items,
		Size:     15,
		Searcher: substringSearcher(items),
	}).Run()
	if err != nil {
		return nil, err
	}
	return &list.Items[idx], nil
}

func pickKey(s *corev1.Secret) (string, error) {
	keys := sortedKeys(s.Data)
	if len(keys) == 0 {
		return "", fmt.Errorf("secret %q has no data", s.Name)
	}
	if len(keys) == 1 {
		return keys[0], nil
	}
	items := append([]string{"all"}, keys...)
	_, choice, err := (&promptui.Select{
		Label:    fmt.Sprintf("Key in %q", s.Name),
		Items:    items,
		Size:     15,
		Searcher: substringSearcher(items),
	}).Run()
	return choice, err
}

// substringSearcher backs promptui's '/' filter: case-insensitive substring
// match against the visible item labels.
func substringSearcher(items []string) func(input string, index int) bool {
	return func(input string, index int) bool {
		return strings.Contains(strings.ToLower(items[index]), strings.ToLower(strings.TrimSpace(input)))
	}
}

func sortSecrets(items []corev1.Secret) {
	sort.Slice(items, func(i, j int) bool {
		if items[i].Namespace != items[j].Namespace {
			return items[i].Namespace < items[j].Namespace
		}
		return items[i].Name < items[j].Name
	})
}

func sortedKeys(m map[string][]byte) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func isTTY(w io.Writer) bool {
	f, ok := w.(*os.File)
	return ok && term.IsTerminal(int(f.Fd()))
}
