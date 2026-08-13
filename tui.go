package main

import (
	"fmt"
	"io"
	"strings"
	"sync"
	"time"
)

// The download TUI: a live progress region (one line per in-flight transfer
// plus a totals line) sitting above a height-capped, self-scrolling log region
// that carries whatever the steps and the downloader wrote.
//
// It repaints in place with a handful of ANSI sequences rather than a terminal
// library, because the whole display is a fixed-height block of plain lines.

const (
	ansiUp        = "\x1b[%dA" // move cursor up N lines
	ansiClearLine = "\r\x1b[K"
	ansiHideCur   = "\x1b[?25l"
	ansiShowCur   = "\x1b[?25h"
	frameInterval = 100 * time.Millisecond
	minLogLines   = 3
)

// tui renders download progress above a scrolling log. It is an io.Writer: the
// step and downloader output channels are pointed at it, and every line they
// write lands in the log region instead of scrolling the progress display away.
type tui struct {
	out      io.Writer
	width    int
	logCap   int
	snapshot func() []*downloadItem

	mu      sync.Mutex
	logs    []string
	partial string
	painted int
	stopped bool

	stop chan struct{}
	done chan struct{}
}

// newTUI builds a renderer sized for the terminal. logLines of 0 auto-sizes the
// log region to min(15, half the terminal height), never below 3 lines.
func newTUI(out io.Writer, width, height, logLines int, snapshot func() []*downloadItem) *tui {
	if width <= 0 {
		width = 80
	}
	if height <= 0 {
		height = 24
	}
	if logLines <= 0 {
		logLines = min(maxLogLines, height/2)
	}
	if logLines < minLogLines {
		logLines = minLogLines
	}
	return &tui{
		out:      out,
		width:    width,
		logCap:   logLines,
		snapshot: snapshot,
		stop:     make(chan struct{}),
		done:     make(chan struct{}),
	}
}

// Start begins repainting until Stop. Painting from one goroutine keeps the
// frame consistent while workers mutate progress underneath it.
func (t *tui) Start() {
	fmt.Fprint(t.out, ansiHideCur)
	go func() {
		defer close(t.done)
		ticker := time.NewTicker(frameInterval)
		defer ticker.Stop()
		for {
			select {
			case <-t.stop:
				return
			case <-ticker.C:
				t.paint()
			}
		}
	}()
}

// Stop ends repainting, leaving the final frame on screen for the user to read.
func (t *tui) Stop() {
	t.mu.Lock()
	if t.stopped {
		t.mu.Unlock()
		return
	}
	t.stopped = true
	t.mu.Unlock()

	close(t.stop)
	<-t.done
	t.paint()
	fmt.Fprint(t.out, ansiShowCur)
}

// Write feeds output into the log region. Partial lines are held until their
// newline arrives, so a progress-writing child does not fragment the display.
func (t *tui) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.partial += strings.ReplaceAll(string(p), "\r", "")
	for {
		i := strings.IndexByte(t.partial, '\n')
		if i < 0 {
			break
		}
		t.appendLog(t.partial[:i])
		t.partial = t.partial[i+1:]
	}
	return len(p), nil
}

// logf adds one preformatted line. The queue's log hook points here.
func (t *tui) logf(format string, args ...any) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.appendLog(fmt.Sprintf(format, args...))
}

// appendLog pushes a line into the capped ring. Callers hold t.mu.
func (t *tui) appendLog(line string) {
	t.logs = append(t.logs, line)
	if over := len(t.logs) - t.logCap; over > 0 {
		t.logs = append(t.logs[:0], t.logs[over:]...)
	}
}

// paint redraws the frame in place: up to the top of the last frame, then one
// cleared line per row.
func (t *tui) paint() {
	lines := t.frame(time.Now())

	t.mu.Lock()
	defer t.mu.Unlock()
	var b strings.Builder
	if t.painted > 0 {
		fmt.Fprintf(&b, ansiUp, t.painted)
	}
	for _, line := range lines {
		b.WriteString(ansiClearLine)
		b.WriteString(clipDisplay(line, t.width))
		b.WriteByte('\n')
	}
	// A shorter frame than last time leaves stale rows below; clear them, then
	// come back up so the next repaint still starts at the frame's top.
	if extra := t.painted - len(lines); extra > 0 {
		for i := 0; i < extra; i++ {
			b.WriteString(ansiClearLine)
			b.WriteByte('\n')
		}
		fmt.Fprintf(&b, ansiUp, extra)
	}
	t.painted = len(lines)
	fmt.Fprint(t.out, b.String())
}

// frame renders one complete display: the progress region, a rule, then the log
// region padded to its full height so the block never changes size.
func (t *tui) frame(now time.Time) []string {
	lines := t.progressRegion(now)
	lines = append(lines, strings.Repeat("-", min(t.width, 60)))

	t.mu.Lock()
	logs := make([]string, len(t.logs))
	copy(logs, t.logs)
	t.mu.Unlock()

	for _, l := range logs {
		lines = append(lines, " "+l)
	}
	for i := len(logs); i < t.logCap; i++ {
		lines = append(lines, "")
	}
	return lines
}

// progressRegion renders the counts header, one line per in-flight download,
// and the aggregate line.
func (t *tui) progressRegion(now time.Time) []string {
	items := t.snapshot()
	totals := tallyDownloads(items, now)

	head := fmt.Sprintf("downloads: %d active, %d queued, %d done", totals.Active, totals.Queued, totals.Done)
	if totals.Failed > 0 {
		head += fmt.Sprintf(", %d failed", totals.Failed)
	}
	lines := []string{head}

	for _, item := range items {
		if item.state.Load() != dlActive {
			continue
		}
		p := progressOf(item.done.Load(), item.total.Load(), time.Unix(0, item.start.Load()), now)
		lines = append(lines, "  "+progressLine(item.label(), p, t.width-2))
	}

	agg := itemProgress{Done: totals.Bytes, Total: totals.Total, Fraction: -1}
	if totals.TotalKnown && totals.Total > 0 {
		agg.Fraction = float64(totals.Bytes) / float64(totals.Total)
	}
	if totals.Elapsed > 0 && totals.Bytes > 0 {
		agg.Speed = float64(totals.Bytes) / totals.Elapsed.Seconds()
		if totals.TotalKnown && totals.Total > totals.Bytes && agg.Speed > 0 {
			agg.ETA = time.Duration(float64(totals.Total-totals.Bytes) / agg.Speed * float64(time.Second))
			agg.HasETA = true
		}
	}
	return append(lines, "  "+progressLine("TOTAL", agg, t.width-2))
}

// segment is one column of a progress line. Lower priority is dropped first
// when the terminal is too narrow for the whole line.
type segment struct {
	text string
	prio int
}

// progressLine lays out one download as label + columns, dropping the least
// important columns until it fits. The label keeps whatever room is left.
func progressLine(label string, p itemProgress, width int) string {
	if width < 12 {
		width = 12
	}
	segs := []segment{
		{progressBar(p.Fraction, 14), 3},
		{percentText(p.Fraction), 9},
		{sizesText(p.Done, p.Total), 5},
		{speedText(p.Speed), 1},
		{etaText(p), 2},
	}
	const minLabel = 8
	for len(segs) > 1 && minLabel+2+segmentWidth(segs) > width {
		segs = dropLowest(segs)
	}
	tail := joinSegments(segs)
	labelW := width - 2 - displayWidth(tail)
	if labelW < minLabel {
		labelW = minLabel
	}
	return padRight(labelW, clipDisplay(label, labelW)) + "  " + tail
}

func segmentWidth(segs []segment) int { return displayWidth(joinSegments(segs)) }

func joinSegments(segs []segment) string {
	parts := make([]string, 0, len(segs))
	for _, s := range segs {
		parts = append(parts, s.text)
	}
	return strings.Join(parts, "  ")
}

func dropLowest(segs []segment) []segment {
	worst := 0
	for i, s := range segs {
		if s.prio < segs[worst].prio {
			worst = i
		}
	}
	return append(segs[:worst:worst], segs[worst+1:]...)
}

// progressBar draws a fixed-width bar. A negative fraction means the total is
// unknown, which the bar shows as empty rather than guessing at a position.
func progressBar(fraction float64, width int) string {
	filled := 0
	if fraction > 0 {
		filled = int(fraction * float64(width))
		if filled > width {
			filled = width
		}
	}
	return "[" + strings.Repeat("=", filled) + strings.Repeat(" ", width-filled) + "]"
}

func percentText(fraction float64) string {
	if fraction < 0 {
		return "  ?%"
	}
	return fmt.Sprintf("%3.0f%%", fraction*100)
}

func sizesText(done, total int64) string {
	right := "?"
	if total > 0 {
		right = humanBytes(total)
	}
	return fmt.Sprintf("%9s / %-9s", humanBytes(done), right)
}

func speedText(speed float64) string {
	if speed <= 0 {
		return fmt.Sprintf("%10s", "")
	}
	return fmt.Sprintf("%10s", humanBytes(int64(speed))+"/s")
}

func etaText(p itemProgress) string {
	if !p.HasETA {
		return fmt.Sprintf("%-9s", "")
	}
	return fmt.Sprintf("%-9s", "ETA "+shortDuration(p.ETA))
}

// humanBytes formats a byte count in binary units, the size a download tool is
// expected to report.
func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for x := n / unit; x >= unit && exp < 4; x /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTP"[exp])
}

// shortDuration renders an ETA as mm:ss, or h:mm:ss once it passes an hour.
func shortDuration(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	total := int(d.Seconds())
	h, m, s := total/3600, (total/60)%60, total%60
	if h > 0 {
		return fmt.Sprintf("%d:%02d:%02d", h, m, s)
	}
	return fmt.Sprintf("%02d:%02d", m, s)
}

// clipDisplay truncates s to w display columns, marking the cut with an
// ellipsis. Width is measured in columns, not bytes, so wide characters and
// ANSI colors in a child's output do not skew the layout.
func clipDisplay(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if displayWidth(s) <= w {
		return s
	}
	var b strings.Builder
	used := 0
	for _, r := range s {
		rw := displayWidth(string(r))
		if used+rw > w-1 {
			break
		}
		b.WriteRune(r)
		used += rw
	}
	return b.String() + "~"
}
