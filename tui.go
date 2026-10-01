package main

import (
	"embed"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/wow-look-at-my/tml"
	"github.com/wow-look-at-my/tml/sema"
)

// The download TUI is a tml.Live region pinned to the bottom of the screen:
// the counts, the running step, a row per in-flight transfer.

// Cursor controls for the watch painter.
const (
	ansiUp        = "\x1b[%dA"
	ansiClearLine = "\r\x1b[K"
	ansiHideCur   = "\x1b[?25l"
	ansiShowCur   = "\x1b[?25h"
)

//go:embed ui/downloads
var downloadsUI embed.FS

// downloadsView is the component the region draws, loaded once per process.
var downloadsView = sync.OnceValues(func() (*tml.View, error) {
	sub, err := fs.Sub(downloadsUI, "ui/downloads")
	if err != nil {
		return nil, err
	}
	return tml.Load(sub, "Downloads.tml", tml.Options{Dark: true})
})

// tui is the display for a single download session.
type tui struct {
	live     *tml.Live
	errOut   io.Writer
	snapshot func() []*downloadItem

	mu   sync.Mutex
	step *stepProgress
}

// tuiExit ends the process after an interrupt.
var tuiExit = os.Exit

const interruptExitCode = 130

// newTUI builds the region on out. errOut is where a failure of the display
// itself goes, once the region is off the screen.
func newTUI(out, errOut io.Writer, snapshot func() []*downloadItem) (*tui, error) {
	view, err := downloadsView()
	if err != nil {
		return nil, fmt.Errorf("download display: %w", err)
	}
	t := &tui{errOut: errOut, snapshot: snapshot}
	t.live = tml.NewLive(view, func() tml.Props { return t.props(time.Now()) }, tml.LiveOptions{
		Output: out,
		OnInterrupt: func() {
			fmt.Fprintln(out, "interrupted; partial transfers left as .part files")
			tuiExit(interruptExitCode)
		},
	})
	return t, nil
}

// Start shows the region.
func (t *tui) Start() error { return t.live.Start() }

// Stop prints the lines still pending and takes the region off the screen. An
// idle block of finished rows says nothing the summary below it does not say
// better.
func (t *tui) Stop() {
	if err := t.live.Stop(); err != nil && !errors.Is(err, tml.ErrInterrupted) {
		fmt.Fprintln(t.errOut, "error: download display:", err)
	}
}

// Write takes a child's output. A partial line waits for its newline.
func (t *tui) Write(p []byte) (int, error) { return t.live.Write(p) }

// logf prints a line above the region. The queue's log hook points here.
func (t *tui) logf(format string, args ...any) { t.live.Printf(format, args...) }

// setStep shows the running step in the region. nil removes the line.
func (t *tui) setStep(p *stepProgress) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.step = p
}

// props is a single frame of the region: the counts, the running step, a row
// per in-flight download, and the TOTAL row.
func (t *tui) props(now time.Time) tml.Props {
	items := t.snapshot()
	totals := tallyDownloads(items, now)

	head := fmt.Sprintf("downloads: %d active, %d queued, %d done", totals.Active, totals.Queued, totals.Done)
	headStyle := "head"
	if totals.Failed > 0 {
		head += fmt.Sprintf(", %d failed", totals.Failed)
		headStyle = "head.failed"
	}
	var rows []map[string]sema.Value
	for _, item := range items {
		if item.state.Load() != dlActive {
			continue
		}
		p := progressOf(item.shown(), item.total.Load(), time.Unix(0, item.start.Load()), now)
		rows = append(rows, progressRecord(item.label(), "active", p))
	}
	rows = append(rows, progressRecord("TOTAL", "total", aggregateProgress(totals)))

	status := ""
	t.mu.Lock()
	if t.step != nil {
		status = t.step.line()
	}
	t.mu.Unlock()
	return tml.Props{
		"head":      sema.StringValue(head),
		"headStyle": sema.StringValue(headStyle),
		"status":    sema.StringValue(status),
		"rows":      sema.RecordListValue(rows),
	}
}

// progressRecord is a row the Transfer template draws. A length the server
// never gave has no fraction, so the row hides its bar and percentage.
func progressRecord(label, state string, p itemProgress) map[string]sema.Value {
	r := map[string]sema.Value{
		"label": sema.StringValue(label),
		"state": sema.StringValue(state),
		"known": sema.BoolValue(p.Fraction >= 0),
		"sizes": sema.StringValue(sizesText(p)),
		"speed": sema.StringValue(speedText(p.Speed)),
		"eta":   sema.StringValue(etaText(p)),
	}
	if p.Fraction >= 0 {
		r["value"] = sema.StringValue(fmt.Sprintf("%.4f", min(p.Fraction, 1)))
		r["percent"] = sema.StringValue(percentText(p.Fraction))
	}
	return r
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

func percentText(fraction float64) string {
	return fmt.Sprintf("%3.0f%%", fraction*100)
}

// sizesText renders "downloaded / total". A total that is only a floor — some
// download in the tally never reported a length — is marked with "+" rather
// than presented as the finish line.
func sizesText(p itemProgress) string {
	if p.Waiting > 0 {
		return "waiting " + shortDuration(p.Waiting)
	}
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
