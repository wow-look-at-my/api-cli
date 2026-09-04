package main

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"
)

// The watch loop: re-run a leaf on an interval and repaint its output in place,
// the way watch(1) does. A frame is one whole run of the leaf -- steps, entry,
// request and formatter -- captured into a buffer instead of the terminal, then
// painted over the frame before it.
//
// The repaint uses the same ANSI sequences as the download display. It needs no
// terminal library, because a frame is a fixed-height block of plain lines.

// watchMinInterval is the floor on --watch. A shorter interval repaints faster
// than a terminal can draw, and it hammers whatever the leaf calls.
const watchMinInterval = 100 * time.Millisecond

// parseWatchInterval reads the --watch value. A bare number is seconds, so
// `--watch 2` reads the way watch(1)'s `-n 2` does. Anything else is a Go
// duration.
func parseWatchInterval(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		secs, ferr := strconv.ParseFloat(s, 64)
		if ferr != nil {
			return 0, fmt.Errorf("--watch %q: want a duration (2s, 500ms) or a number of seconds", s)
		}
		d = time.Duration(secs * float64(time.Second))
	}
	if d < watchMinInterval {
		return 0, fmt.Errorf("--watch %q: interval must be at least %s", s, watchMinInterval)
	}
	return d, nil
}

// watchInterval reports the interval this invocation asked for. Zero means the
// leaf runs one time.
func watchInterval(c *cobra.Command) (time.Duration, error) {
	v, _ := c.Root().PersistentFlags().GetString("watch")
	return parseWatchInterval(v)
}

// runWatch repaints body's output every interval until the user interrupts it.
// title heads each frame, next to the interval and the time of the frame.
//
// The leaf writes to the terminal through execStdout and execStderr. A frame
// swaps both for one buffer, so a step's diagnostic lands in the frame beside
// the output it explains rather than scrolling the display away.
func runWatch(title string, every time.Duration, body func() error) error {
	tty, width, height := stdoutSize()
	out := execStdout
	p := &watchPainter{out: out, width: width, height: height, tty: tty}

	stop := make(chan struct{})
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sig)
	go func() {
		<-sig
		close(stop)
	}()

	if tty {
		// The frames are the display. Formatting decides on the real terminal
		// behind the buffer, not on the buffer.
		ttyOverride = &termSize{width: width, height: height}
		defer func() { ttyOverride = nil }()
		fmt.Fprint(out, ansiHideCur)
		defer fmt.Fprint(out, ansiShowCur)
	}

	return watchLoop(p, title, every, stop, body)
}

// watchLoop is runWatch without the terminal and signal setup, so a test drives
// it with its own stop channel.
func watchLoop(p *watchPainter, title string, every time.Duration, stop <-chan struct{}, body func() error) error {
	for {
		var buf bytes.Buffer
		err := captureInto(&buf, body)
		if err != nil {
			fmt.Fprintln(&buf, "error:", err)
		}
		p.paint(watchHeader(title, every, time.Now()), buf.String())

		select {
		case <-stop:
			p.finish()
			exitCode = interruptExitCode
			return nil
		case <-time.After(every):
		}
	}
}

// captureInto points the leaf's output channels at w for one frame.
func captureInto(w io.Writer, body func() error) error {
	prevOut, prevErr := execStdout, execStderr
	execStdout, execStderr = w, w
	defer func() { execStdout, execStderr = prevOut, prevErr }()
	return body()
}

// watchHeader is the frame's first line: what is running, how often, and when
// this frame was drawn.
func watchHeader(title string, every time.Duration, now time.Time) string {
	return fmt.Sprintf("every %s: %s    %s", every, title, now.Format("15:04:05"))
}

// watchPainter draws one frame over the last one. Without a terminal it appends
// frames instead, so a redirected watch reads as a log rather than a pile of
// escape sequences.
type watchPainter struct {
	out     io.Writer
	width   int
	height  int
	tty     bool
	painted int
}

// paint draws the header, a rule, and the body. The block is clipped to the
// terminal: a wrapped line would break the count this repaint moves the cursor
// by, and the frame would walk down the screen.
func (p *watchPainter) paint(header, bodyText string) {
	lines := p.frame(header, bodyText)
	if !p.tty {
		fmt.Fprintln(p.out, strings.Join(lines, "\n"))
		return
	}

	var b strings.Builder
	if p.painted > 0 {
		fmt.Fprintf(&b, ansiUp, p.painted)
	}
	for _, line := range lines {
		b.WriteString(ansiClearLine)
		b.WriteString(clipDisplay(line, p.width))
		b.WriteByte('\n')
	}
	// A shorter frame than the last one leaves stale rows below it. Clear them,
	// then come back up so the next repaint still starts at the frame's top.
	if extra := p.painted - len(lines); extra > 0 {
		for range extra {
			b.WriteString(ansiClearLine)
			b.WriteByte('\n')
		}
		fmt.Fprintf(&b, ansiUp, extra)
	}
	p.painted = len(lines)
	fmt.Fprint(p.out, b.String())
}

// frame assembles the lines of one frame, clipped to the terminal height. A
// clipped frame says how many lines it dropped, because a table that silently
// loses its tail reads as an API that returned fewer records.
func (p *watchPainter) frame(header, bodyText string) []string {
	lines := []string{header, ""}
	body := strings.Split(strings.TrimRight(bodyText, "\n"), "\n")
	if len(body) == 1 && body[0] == "" {
		body = nil
	}
	lines = append(lines, body...)
	if !p.tty {
		return lines
	}
	if room := p.height - 1; room > 3 && len(lines) > room {
		dropped := len(lines) - room
		lines = lines[:room]
		lines[room-1] = fmt.Sprintf("... %d more line(s)", dropped+1)
	}
	return lines
}

// finish leaves the last frame on screen and puts the cursor below it.
func (p *watchPainter) finish() {
	if !p.tty {
		return
	}
	fmt.Fprintln(p.out)
}
