package view

import (
	"context"
	"fmt"
	"io"
	"sort"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/PixiBixi/kubectl-klens/internal/kube"
)

// Secret fetches a named secret and prints its decoded key/value pairs as a table.
func Secret(ctx context.Context, c kubernetes.Interface, f kube.Flags, args []string, out io.Writer) error {
	if len(args) < 1 || args[0] == "" {
		return fmt.Errorf("secret requires a name: kubectl klens secret <name>")
	}
	s, err := c.CoreV1().Secrets(f.NamespaceScope()).Get(ctx, args[0], metav1.GetOptions{})
	if err != nil {
		return err
	}
	keys := make([]string, 0, len(s.Data))
	for k := range s.Data {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	t := kube.NewTable(out, "KEY", "VALUE")
	for _, k := range keys {
		t.Row(k, string(s.Data[k]))
	}
	return t.Flush()
}
