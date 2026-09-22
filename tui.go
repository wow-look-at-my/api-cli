package main

import (
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"
)

// The download TUI: a block of slots pinned to the bottom of the screen, a
// single per in-flight transfer plus a totals line, each repainted over its
// own previous line.
//
// Everything else -- a finished transfer, a step's output, a transport's stderr
// -- is written a single time, above the block, and scrolls away into the
// terminal's own scrollback. So a run reads as a growing list of what landed,
// with the live slots always at the bottom.
//
// It repaints with a handful of ANSI sequences rather than a terminal library,
// because the block is a few plain lines and the cursor never leaves it.

const (
	ansiUp        = "\x1b[%dA" // move cursor up N lines
	ansiClearLine = "\r\x1b[K"
	ansiHideCur   = "\x1b[?25l"
	ansiShowCur   = "\x1b[?25h"
	frameInterval = 100 * time.Millisecond
)

// tui renders the download slots. It is an io.Writer: the step and downloader
// output channels are pointed at it, so a line they write is emitted above the
// slots instead of scrolling them away.
type tui struct {
	out      io.Writer
	width    int
	snapshot func() []*downloadItem

	mu sync.Mutex
	// pending holds lines not yet emitted. paint writes them out and forgets
	// them: the terminal owns the scrollback, so nothing is kept to redraw.
	pending  []string
	partial  string
	painted  int
	started  bool
	stopped  bool
	finished bool

	stop chan struct{}
	done chan struct{}
}

// newTUI builds a renderer for a terminal of the given width.
func newTUI(out io.Writer, width int, snapshot func() []*downloadItem) *tui {
	if width <= 0 {
		width = 80
	}
	return &tui{
		out:      out,
		width:    width,
		snapshot: snapshot,
		stop:     make(chan struct{}),
		done:     make(chan struct{}),
	}
}

// Start begins repainting until Stop. Painting from a single goroutine
// keeps the frame consistent while workers mutate progress underneath it.
func (t *tui) Start() {
	t.mu.Lock()
	t.started = true
	t.mu.Unlock()

	fmt.Fprint(t.out, ansiHideCur)
	t.watchInterrupt()
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

// tuiExit ends the process after an interrupt. A var so the signal path is
// testable without taking the test binary down with it.
var tuiExit = os.Exit

const interruptExitCode = 130

// watchInterrupt puts the terminal back if the run is cut short. Without it a
// Ctrl-C leaves the cursor hidden, and the user's next shell prompt is invisible
// until they run `reset`.
func (t *tui) watchInterrupt() {
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	go func() {
		select {
		case <-t.stop:
			signal.Stop(sig)
		case <-sig:
			signal.Stop(sig)
			t.onSignal()
		}
	}()
}

// onSignal restores the terminal and ends the run. A display that already
// stopped does nothing instead.
//
// Notify fans a signal out to every display the process started, and both
// select cases above go ready together when Stop races the signal, so a
// retired display reaches this point. It owns no terminal any more. Answering
// would write over the display that replaced it, and would end that run.
func (t *tui) onSignal() {
	if t.isStopped() {
		return
	}
	t.Stop()
	fmt.Fprintln(t.out, "interrupted; partial transfers left as .part files")
	tuiExit(interruptExitCode)
}

// isStopped reports whether Stop already ran.
func (t *tui) isStopped() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.stopped
}

// Stop ends repainting. The last lines go out and the slots come off the
// screen: an idle block of finished slots says nothing the summary below it
// does not say better.
func (t *tui) Stop() {
	t.mu.Lock()
	if t.stopped {
		t.mu.Unlock()
		return
	}
	t.stopped = true
	started := t.started
	t.mu.Unlock()

	// Only a display that started has a painter to wait for. Waiting anyway
	// blocks forever, because nothing else ever closes done.
	if started {
		close(t.stop)
		<-t.done
	}

	t.mu.Lock()
	t.finished = true
	if t.partial != "" {
		t.appendLog(t.partial)
		t.partial = ""
	}
	t.mu.Unlock()

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

// logf adds a single preformatted line. The queue's log hook points here.
func (t *tui) logf(format string, args ...any) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.appendLog(fmt.Sprintf(format, args...))
}

// appendLog queues a line for the next paint. Callers hold t.mu.
func (t *tui) appendLog(line string) {
	t.pending = append(t.pending, line)
}

// paint moves up to the top of the block, emits whatever has queued since the
// last frame, and redraws the slots underneath it.
//
// The queued lines land on rows the old block occupied, so they cost no extra
// scrolling, and the block that follows pushes them up into the scrollback for
// good. That is the whole trick: a single cursor movement per frame, and a
// finished transfer is written exactly a single time.
func (t *tui) paint() {
	lines := t.frame(time.Now())

	t.mu.Lock()
	defer t.mu.Unlock()
	if t.finished {
		lines = nil
	}
	var b strings.Builder
	if t.painted > 0 {
		fmt.Fprintf(&b, ansiUp, t.painted)
	}
	for _, line := range t.pending {
		writeRow(&b, line, t.width)
	}
	for _, line := range lines {
		writeRow(&b, line, t.width)
	}
	// A block shorter than its predecessor leaves stale rows below. Clear them,
	// then come back up so the next repaint still starts at the block's top.
	if extra := t.painted - len(t.pending) - len(lines); extra > 0 {
		for i := 0; i < extra; i++ {
			b.WriteString(ansiClearLine)
			b.WriteByte('\n')
		}
		fmt.Fprintf(&b, ansiUp, extra)
	}
	t.pending = t.pending[:0]
	t.painted = len(lines)
	fmt.Fprint(t.out, b.String())
}

// writeRow emits a single line over whatever the row held before.
func writeRow(b *strings.Builder, line string, width int) {
	b.WriteString(ansiClearLine)
	b.WriteString(clipDisplay(line, width))
	b.WriteByte('\n')
}

// progressRow is a single line of the block before it is laid out. The frame
// holds every row because the columns it keeps depend on all of them.
type progressRow struct {
	label string
	p     itemProgress
}

// frame renders the block: the counts header, a single slot per in-flight
// download, and the aggregate line.
func (t *tui) frame(now time.Time) []string {
	items := t.snapshot()
	totals := tallyDownloads(items, now)

	head := fmt.Sprintf("downloads: %d active, %d queued, %d done", totals.Active, totals.Queued, totals.Done)
	if totals.Failed > 0 {
		head += fmt.Sprintf(", %d failed", totals.Failed)
	}
	var rows []progressRow
	for _, item := range items {
		if item.state.Load() != dlActive {
			continue
		}
		p := progressOf(item.done.Load(), item.total.Load(), time.Unix(0, item.start.Load()), now)
		rows = append(rows, progressRow{item.label(), p})
	}
	rows = append(rows, progressRow{"TOTAL", aggregateProgress(totals)})

	fractions := false
	for _, r := range rows {
		fractions = fractions || r.p.Fraction >= 0
	}
	lay := planProgressLayout(t.width-2, fractions)

	lines := []string{head}
	for _, r := range rows {
		lines = append(lines, "  "+progressLine(r.label, r.p, lay))
	}
	return lines
}

// aggregateProgress turns a tally into the TOTAL row's numbers. An unreported
// length makes the denominator a floor, which the row marks with a "+" beside a
// percentage the known lengths still support.
func aggregateProgress(t downloadTotals) itemProgress {
	p := itemProgress{Done: t.Bytes, Total: t.Total, Fraction: -1, TotalIsFloor: !t.TotalKnown}
	if t.Total > 0 && (t.TotalKnown || t.Total > t.Bytes) {
		p.Fraction = float64(t.Bytes) / float64(t.Total)
	}
	if t.Elapsed <= 0 || t.Bytes <= 0 {
		return p
	}
	p.Speed = float64(t.Bytes) / t.Elapsed.Seconds()
	if t.TotalKnown && t.Total > t.Bytes && p.Speed > 0 {
		p.ETA = time.Duration(float64(t.Total-t.Bytes) / p.Speed * float64(time.Second))
		p.HasETA = true
	}
	return p
}

// Every column has a fixed width so the rows form real columns.
//
// prio orders what goes when the terminal cannot fit them all. The bar goes
// earliest despite being the eye-catching column: it is the a single thing here
// the percentage beside it already says.
const (
	colBar = iota
	colPct
	colSizes
	colSpeed
	colETA
)

var progressColumns = []struct{ kind, width, prio int }{
	{colBar, 16, 1},
	{colPct, 4, 9},
	{colSizes, 24, 5},
	{colSpeed, 11, 2},
	{colETA, 9, 3},
}

// The label column takes the room the columns leave, within these bounds. The
// cap keeps a wide terminal from pushing the numbers half a screen away from
// the names they belong to.
const (
	minLabelWidth = 8
	maxLabelWidth = 32
)

// progressLayout is the column arrangement for a single frame. It is computed
// a single time and used for every row, because a layout decided per row
// would let a single wide value knock that row's columns out of line with its neighbours'.
type progressLayout struct {
	label int
	keep  []int
}

// planProgressLayout picks the columns for a single frame. A frame where no
// row has a known length drops the bar and the percentage outright: a column
// of empty cells is room the file names can use.
func planProgressLayout(width int, fractions bool) progressLayout {
	if width < minLabelWidth+2 {
		width = minLabelWidth + 2
	}
	keep := make([]int, 0, len(progressColumns))
	for i, col := range progressColumns {
		if !fractions && (col.kind == colBar || col.kind == colPct) {
			continue
		}
		keep = append(keep, i)
	}
	tail := func() int {
		w := 0
		for _, i := range keep {
			w += progressColumns[i].width + 2
		}
		return w - 2
	}
	for len(keep) > 1 && minLabelWidth+2+tail() > width {
		worst := 0
		for k, i := range keep {
			if progressColumns[i].prio < progressColumns[keep[worst]].prio {
				worst = k
			}
		}
		keep = append(keep[:worst:worst], keep[worst+1:]...)
	}
	label := min(max(width-2-tail(), minLabelWidth), maxLabelWidth)
	return progressLayout{label: label, keep: keep}
}

// progressLine renders a single row into the frame's layout. Each column is
// clipped and padded to its declared width, so an unexpectedly wide value costs
// its own cell and never the alignment.
func progressLine(label string, p itemProgress, lay progressLayout) string {
	if lay.label == 0 {
		lay = planProgressLayout(80, p.Fraction >= 0)
	}
	parts := make([]string, 0, len(lay.keep)+1)
	parts = append(parts, padRight(lay.label, clipDisplay(label, lay.label)))
	for _, i := range lay.keep {
		col := progressColumns[i]
		parts = append(parts, padRight(col.width, clipDisplay(columnText(col.kind, p), col.width)))
	}
	return strings.TrimRight(strings.Join(parts, "  "), " ")
}

func columnText(kind int, p itemProgress) string {
	// A transfer the server gave no length for has no fraction to draw. The bar
	// and the percentage stay empty, and the row still reports bytes and rate.
	if p.Fraction < 0 && (kind == colBar || kind == colPct) {
		return ""
	}
	switch kind {
	case colBar:
		return progressBar(p.Fraction, 14)
	case colPct:
		return percentText(p.Fraction)
	case colSizes:
		return sizesText(p)
	case colSpeed:
		return speedText(p.Speed)
	default:
		return etaText(p)
	}
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
	return fmt.Sprintf("%3.0f%%", fraction*100)
}

// sizesText renders "downloaded / total". A total that is only a floor — some
// download in the tally never reported a length — is marked with "+" rather
// than presented as the finish line.
func sizesText(p itemProgress) string {
	right := "?"
	if p.Total > 0 {
		right = humanBytes(p.Total)
		if p.TotalIsFloor {
			right += "+"
		}
	}
	return fmt.Sprintf("%10s / %s", humanBytes(p.Done), right)
}

func speedText(speed float64) string {
	if speed <= 0 {
		return ""
	}
	return humanBytes(int64(speed)) + "/s"
}

func etaText(p itemProgress) string {
	if !p.HasETA {
		return ""
	}
	return "ETA " + shortDuration(p.ETA)
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

// shortDuration renders an ETA as mm:ss, or h:mm:ss a single time it passes an hour.
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
