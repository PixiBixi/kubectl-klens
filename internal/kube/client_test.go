package kube

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

// writeKubeconfig drops a minimal kubeconfig pointing at host and returns its
// path, so restConfig can be exercised without touching the developer's own.
func writeKubeconfig(t *testing.T, host string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config")
	body := fmt.Sprintf(`apiVersion: v1
kind: Config
clusters:
- name: test
  cluster:
    server: %s
contexts:
- name: test
  context:
    cluster: test
    user: test
current-context: test
users:
- name: test
  user: {}
`, host)
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestNoClientSideThrottling locks QPS off. client-go defaults a
// kubeconfig-built config to QPS 5 / Burst 10, which for a one-shot CLI paging a
// large collection is hundreds of milliseconds of self-inflicted waiting per
// list. Restoring the default would be a silent, invisible regression: nothing
// fails, the command is just slower - hence a test rather than a comment.
func TestNoClientSideThrottling(t *testing.T) {
	cfg, err := restConfig(Flags{Kubeconfig: writeKubeconfig(t, "https://example.invalid")})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.QPS >= 0 {
		t.Errorf("QPS = %v, want negative (limiter disabled)", cfg.QPS)
	}
}

func TestRestConfigSetsTimeout(t *testing.T) {
	cfg, err := restConfig(Flags{
		Kubeconfig:     writeKubeconfig(t, "https://example.invalid"),
		RequestTimeout: 7 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Timeout != 7*time.Second {
		t.Errorf("Timeout = %v, want 7s", cfg.Timeout)
	}
}

// pagedPodServer serves a PodList in ChunkSize pages, handing out a continue
// token until totalPods is exhausted, and counts the requests it answered.
func pagedPodServer(t *testing.T, totalPods int, reqs *int) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*reqs++
		start := 0
		if cont := r.URL.Query().Get("continue"); cont != "" {
			_, _ = fmt.Sscanf(cont, "%d", &start)
		}
		end := min(start+ChunkSize, totalPods)
		list := &corev1.PodList{Items: make([]corev1.Pod, 0, end-start)}
		for i := start; i < end; i++ {
			list.Items = append(list.Items, corev1.Pod{
				Name: "pod-" + strconv.Itoa(i), Namespace: "default",
			})
		}
		if end < totalPods {
			list.Continue = strconv.Itoa(end)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(list)
	}))
}

// TestListPodsPagesWithoutThrottling walks a collection needing several pages
// against a real HTTP server, asserting both that paging is followed to the end
// and that the limiter is not pacing it. The pod count is fixed rather than
// scaled by ChunkSize: this walks 5 pages whatever the page size is, instead of
// also multiplying the JSON volume every time ChunkSize is tuned, which is what
// made this flaky on a busy machine once it was.
func TestListPodsPagesWithoutThrottling(t *testing.T) {
	const pageSpan = 500 // independent of ChunkSize: see the comment above
	total := pageSpan*4 + 1
	wantReqs := (total + ChunkSize - 1) / ChunkSize
	reqs := 0
	srv := pagedPodServer(t, total, &reqs)
	defer srv.Close()

	c, err := kubernetes.NewForConfig(&rest.Config{Host: srv.URL, QPS: -1})
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	pods, err := ListPods(context.Background(), c, Scope{}, metav1.ListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	elapsed := time.Since(start)

	if len(pods) != total {
		t.Fatalf("got %d pods, want %d", len(pods), total)
	}
	if reqs != wantReqs {
		t.Errorf("server answered %d requests, want %d pages", reqs, wantReqs)
	}
	// wantReqs-1 requests would sit behind the 5 QPS limiter, ~200ms each.
	if budget := time.Duration(wantReqs) * 300 * time.Millisecond; elapsed > budget {
		t.Errorf("paging took %v (budget %v), which looks rate-limited", elapsed, budget)
	}
}
