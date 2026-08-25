package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"
)

func TestWatchRedrawsUntilCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var out bytes.Buffer
	frames := 0
	err := watch(ctx, &out, time.Millisecond, func() string { return "HEADER" }, func(w io.Writer) error {
		frames++
		fmt.Fprintf(w, "frame %d\n", frames)
		if frames == 3 {
			cancel()
		}
		return nil
	})
	if err != nil {
		t.Fatalf("watch returned %v, want nil", err)
	}
	if frames != 3 {
		t.Fatalf("rendered %d frames, want 3", frames)
	}
	got := out.String()
	if n := strings.Count(got, clearScreen); n != 3 {
		t.Errorf("cleared the screen %d times, want 3:\n%q", n, got)
	}
	for _, want := range []string{"HEADER", "frame 1", "frame 2", "frame 3"} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q:\n%s", want, got)
		}
	}
}

// A transient apiserver error must not end the watch: a 503 during a rollout is
// exactly the moment the user is staring at the screen.
func TestWatchSurvivesRenderError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var out bytes.Buffer
	calls := 0
	err := watch(ctx, &out, time.Millisecond, func() string { return "HEADER" }, func(w io.Writer) error {
		calls++
		if calls == 1 {
			return errors.New("boom")
		}
		fmt.Fprintln(w, "recovered")
		cancel()
		return nil
	})
	if err != nil {
		t.Fatalf("watch returned %v, want nil", err)
	}
	got := out.String()
	if !strings.Contains(got, "error: boom") {
		t.Errorf("transient error not reported:\n%s", got)
	}
	if !strings.Contains(got, "recovered") {
		t.Errorf("watch did not keep polling after the error:\n%s", got)
	}
}

// Ctrl-C is how a watch ends, so an interrupt must not surface as an error.
func TestWatchCancelDuringRenderIsSilent(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	var out bytes.Buffer
	err := watch(ctx, &out, time.Millisecond, func() string { return "HEADER" }, func(io.Writer) error {
		cancel()
		return context.Canceled
	})
	if err != nil {
		t.Fatalf("watch returned %v, want nil", err)
	}
	if strings.Contains(out.String(), "error") {
		t.Errorf("interrupt reported as an error:\n%s", out.String())
	}
}

func TestWatchCanceledBeforeFirstRender(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var out bytes.Buffer
	frames := 0
	err := watch(ctx, &out, time.Millisecond, func() string { return "HEADER" }, func(io.Writer) error {
		frames++
		return nil
	})
	if err != nil || frames != 0 || out.Len() != 0 {
		t.Fatalf("err=%v frames=%d out=%q, want nil/0/empty", err, frames, out.String())
	}
}

func TestWatchHeader(t *testing.T) {
	got := watchHeader(2*time.Second, []string{"pending", "-A"}, time.Date(2026, 8, 25, 14, 3, 22, 0, time.UTC))
	for _, want := range []string{"Every 2s", "klens pending -A", "14:03:22", "Ctrl-C"} {
		if !strings.Contains(got, want) {
			t.Errorf("header %q missing %q", got, want)
		}
	}
}
