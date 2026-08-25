package cli

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"
	"time"
)

// clearScreen homes the cursor and erases the display, the two escapes watch(1)
// uses. Emitted only after the TTY check in Run, so it never reaches a pipe.
const clearScreen = "\x1b[H\x1b[2J"

// watch re-runs render every interval, redrawing the whole screen, until ctx is
// canceled. Each frame is built in a buffer and written in one call so a slow
// render can't leave a half-drawn table on screen.
//
// A failing render is reported and the loop continues: a transient apiserver
// error is not a reason to drop the user out of a watch they are using to follow
// something in flight. Ctrl-C is the way out, so cancellation returns nil and
// leaves the last frame on screen.
func watch(ctx context.Context, out io.Writer, interval time.Duration, header func() string, render func(io.Writer) error) error {
	tick := time.NewTicker(interval)
	defer tick.Stop()
	var frame bytes.Buffer
	for {
		if ctx.Err() != nil {
			return nil
		}
		frame.Reset()
		err := render(&frame)
		// An error caused by our own interrupt is not worth printing: the user
		// asked to stop, they did not hit a failure.
		if err != nil && ctx.Err() != nil {
			return nil
		}
		body := frame.String()
		if err != nil {
			body = "error: " + err.Error() + "\n"
		}
		fmt.Fprint(out, clearScreen, header(), "\n\n", body)
		select {
		case <-ctx.Done():
			return nil
		case <-tick.C:
		}
	}
}

// watchHeader is the status line above each frame. It echoes the invocation
// rather than just the command name so a watch left on a second screen still
// says which namespace and flags it is showing.
func watchHeader(interval time.Duration, args []string, now time.Time) string {
	return fmt.Sprintf("Every %s: klens %s   %s (Ctrl-C to stop)",
		interval, strings.Join(args, " "), now.Format("15:04:05"))
}

// wantsWatch reports whether the args ask for --watch, in any of the spellings
// the flag package accepts. Used only to reject the flag on a command that does
// not offer it, with a message that names the command.
func wantsWatch(args []string) bool {
	for _, a := range args {
		switch a {
		case "-w", "--w", "-watch", "--watch":
			return true
		}
		if strings.HasPrefix(a, "-watch=") || strings.HasPrefix(a, "--watch=") ||
			strings.HasPrefix(a, "-w=") || strings.HasPrefix(a, "--w=") {
			return true
		}
	}
	return false
}
