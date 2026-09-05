package kube

import (
	"errors"
	"strconv"
	"sync"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// TestListAllFollowsContinueTokens drives listAll directly rather than through a
// fake clientset: the fake's tracker ignores Limit and Continue entirely, so it
// cannot exercise a multi-page walk. Paging is also the only part of this file
// with real logic, so testing it in isolation is the point.
func TestListAllFollowsContinueTokens(t *testing.T) {
	const total = 2300
	var gotOpts []metav1.ListOptions
	items, err := listAll(metav1.ListOptions{}, func(o metav1.ListOptions) ([]int, metav1.ListMeta, error) {
		gotOpts = append(gotOpts, o)
		start := 0
		if o.Continue != "" {
			var err error
			if start, err = strconv.Atoi(o.Continue); err != nil {
				return nil, metav1.ListMeta{}, err
			}
		}
		end := min(start+int(o.Limit), total)
		batch := make([]int, 0, end-start)
		for i := start; i < end; i++ {
			batch = append(batch, i)
		}
		var meta metav1.ListMeta
		if end < total {
			meta.Continue = strconv.Itoa(end)
			remaining := int64(total - end)
			meta.RemainingItemCount = &remaining
		}
		return batch, meta, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != total {
		t.Fatalf("got %d items, want %d", len(items), total)
	}
	for i, v := range items {
		if v != i {
			t.Fatalf("item %d = %d, want %d (pages stitched in the wrong order)", i, v, i)
		}
	}
	if want := (total + ChunkSize - 1) / ChunkSize; len(gotOpts) != want {
		t.Fatalf("made %d requests, want %d", len(gotOpts), want)
	}
	for i, o := range gotOpts {
		if o.Limit != ChunkSize {
			t.Errorf("request %d: Limit = %d, want %d", i, o.Limit, ChunkSize)
		}
		if i == 0 && o.Continue != "" {
			t.Errorf("first request must not carry a continue token, got %q", o.Continue)
		}
		if i > 0 && o.Continue == "" {
			t.Errorf("request %d: missing continue token", i)
		}
	}
}

// TestListAllPreservesCallerOptions checks the caller's selector survives paging:
// a pushed-down FieldSelector has to be repeated on every page, not just the
// first, or later pages would quietly widen the query.
func TestListAllPreservesCallerOptions(t *testing.T) {
	const selector = "status.phase=Pending"
	var gotOpts []metav1.ListOptions
	_, err := listAll(metav1.ListOptions{FieldSelector: selector}, func(o metav1.ListOptions) ([]int, metav1.ListMeta, error) {
		gotOpts = append(gotOpts, o)
		if len(gotOpts) < 3 {
			return []int{len(gotOpts)}, metav1.ListMeta{Continue: "more"}, nil
		}
		return []int{len(gotOpts)}, metav1.ListMeta{}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(gotOpts) != 3 {
		t.Fatalf("made %d requests, want 3", len(gotOpts))
	}
	for i, o := range gotOpts {
		if o.FieldSelector != selector {
			t.Errorf("request %d: FieldSelector = %q, want %q", i, o.FieldSelector, selector)
		}
	}
}

// TestListAllSinglePageDoesNotCopy pins the no-copy fast path: a collection that
// fits in one page must come back as the server's own slice, since these objects
// are large (a corev1.Pod is ~1.2 kB) and copying them all is pure waste.
func TestListAllSinglePageDoesNotCopy(t *testing.T) {
	src := []int{1, 2, 3}
	got, err := listAll(metav1.ListOptions{}, func(metav1.ListOptions) ([]int, metav1.ListMeta, error) {
		return src, metav1.ListMeta{}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(src) || &got[0] != &src[0] {
		t.Fatal("a single page should be returned without copying")
	}
}

func TestListAllPropagatesErrorMidWalk(t *testing.T) {
	boom := errors.New("boom")
	calls := 0
	got, err := listAll(metav1.ListOptions{}, func(metav1.ListOptions) ([]int, metav1.ListMeta, error) {
		calls++
		if calls == 2 {
			return nil, metav1.ListMeta{}, boom
		}
		return []int{calls}, metav1.ListMeta{Continue: "next"}, nil
	})
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want %v", err, boom)
	}
	if got != nil {
		t.Fatalf("a failed walk must not return partial results, got %v", got)
	}
}

// TestChunkSizeBounded pins the two properties ChunkSize has to keep: it must be
// set (an unlimited List makes the apiserver materialize the whole collection in
// one response) and it must stay within what an apiserver serves comfortably in
// a single page.
func TestChunkSizeBounded(t *testing.T) {
	if ChunkSize <= 0 || ChunkSize > 5000 {
		t.Fatalf("ChunkSize = %d, want a bound in (0, 5000]", ChunkSize)
	}
}

// TestListAllUsesChunkSize checks the constant actually reaches the request:
// dropping Limit would silently restore the unbounded List it exists to prevent.
func TestListAllUsesChunkSize(t *testing.T) {
	var got int64
	if _, err := listAll(metav1.ListOptions{}, func(o metav1.ListOptions) ([]int, metav1.ListMeta, error) {
		got = o.Limit
		return nil, metav1.ListMeta{}, nil
	}); err != nil {
		t.Fatal(err)
	}
	if got != ChunkSize {
		t.Fatalf("Limit = %d, want ChunkSize (%d)", got, ChunkSize)
	}
}

// TestListAllBoundsInFlight checks the semaphore actually caps concurrent
// requests. Without it the two fan-out layers multiply: a 10-list view under a
// 16-namespace glob would put 160 requests on the wire at once.
func TestListAllBoundsInFlight(t *testing.T) {
	var mu sync.Mutex
	cur, peak := 0, 0
	page := func(o metav1.ListOptions) ([]int, metav1.ListMeta, error) {
		mu.Lock()
		cur++
		peak = max(peak, cur)
		mu.Unlock()
		time.Sleep(2 * time.Millisecond) // hold the slot long enough to overlap
		mu.Lock()
		cur--
		mu.Unlock()
		return nil, metav1.ListMeta{}, nil
	}

	var wg sync.WaitGroup
	const callers = MaxInFlight * 4
	wg.Add(callers)
	for range callers {
		go func() {
			defer wg.Done()
			if _, err := listAll(metav1.ListOptions{}, page); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()

	if peak > MaxInFlight {
		t.Fatalf("peak concurrency %d, want at most MaxInFlight (%d)", peak, MaxInFlight)
	}
	// A bound that never binds would make the test vacuous.
	if peak < 2 {
		t.Fatalf("peak concurrency %d: the requests never overlapped, the test proves nothing", peak)
	}
}
