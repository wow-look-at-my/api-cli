package main

import (
	"bytes"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// staticItems builds a snapshot function over fixed items, so a frame can be
// rendered without running any transfers.
func staticItems(items ...*downloadItem) func() []*downloadItem {
	return func() []*downloadItem { return items }
}

func mkItem(state int32, dest string, done, total int64, started time.Time) *downloadItem {
	item := &downloadItem{}
	item.state.Store(state)
	item.name.Store(dest)
	item.done.Store(done)
	item.total.Store(total)
	if !started.IsZero() {
		item.start.Store(started.UnixNano())
	}
	return item
}

func TestNewTUI_FallsBackToEightyColumns(t *testing.T) {
	assert.Equal(t, 80, newTUI(&bytes.Buffer{}, 0, staticItems()).width)
	assert.Equal(t, 120, newTUI(&bytes.Buffer{}, 120, staticItems()).width)
}

func TestTUI_WriteQueuesWholeLines(t *testing.T) {
	tu := newTUI(&bytes.Buffer{}, 80, staticItems())

	_, err := tu.Write([]byte("one\ntwo\n"))
	require.NoError(t, err)
	_, err = tu.Write([]byte("thr"))
	require.NoError(t, err)
	assert.Equal(t, []string{"one", "two"}, tu.pending, "a partial line waits for its newline")

	_, err = tu.Write([]byte("ee\r\nfour\n"))
	require.NoError(t, err)
	assert.Equal(t, []string{"one", "two", "three", "four"}, tu.pending, "nothing is dropped before a paint")
}

func TestTUI_FrameIsOnlyTheSlotsAndTotals(t *testing.T) {
	now := time.Now()
	tu := newTUI(&bytes.Buffer{}, 100, staticItems(
		mkItem(dlActive, "/d/big.iso", 512, 2048, now.Add(-2*time.Second)),
		mkItem(dlQueued, "/d/later.iso", 0, -1, time.Time{}),
		mkItem(dlDone, "/d/done.iso", 100, 100, now.Add(-9*time.Second)),
	))
	tu.logf("downloaded %s (100 B)", "done.iso")

	lines := tu.frame(now)
	joined := strings.Join(lines, "\n")

	assert.Contains(t, lines[0], "1 active, 1 queued, 1 done")
	assert.Contains(t, joined, "big.iso", "an in-flight transfer holds a slot")
	assert.Contains(t, joined, " 25%", "512 of 2048 bytes")
	assert.Contains(t, joined, "TOTAL")
	assert.NotContains(t, joined, "done.iso", "a finished transfer holds no slot")
	assert.NotContains(t, joined, "downloaded", "a queued line is emitted above the block, never drawn in it")

	// header + the one active slot + TOTAL, and nothing else.
	assert.Len(t, lines, 3)
}

func TestTUI_FrameCountsFailures(t *testing.T) {
	tu := newTUI(&bytes.Buffer{}, 80, staticItems(mkItem(dlFailed, "/d/x", 0, -1, time.Now())))
	assert.Contains(t, tu.frame(time.Now())[0], "1 failed")
}

func TestTUI_PaintRewritesInPlace(t *testing.T) {
	var buf bytes.Buffer
	now := time.Now()
	tu := newTUI(&buf, 60, staticItems(mkItem(dlActive, "/d/a", 1, 2, now)))

	tu.paint()
	first := buf.String()
	assert.NotContains(t, first, "A\r", "the first frame has nothing to move up over")
	assert.Contains(t, first, ansiClearLine)
	painted := tu.painted

	buf.Reset()
	tu.paint()
	assert.Contains(t, buf.String(), "\x1b["+itoa(painted)+"A", "later frames redraw over the last one")
}

// The point of the whole design: a finished chunk is written once, above the
// slots, and the block redraws below it. So the line survives into the
// terminal's scrollback while the slots keep overwriting themselves.
func TestTUI_PaintEmitsQueuedLinesAboveTheSlots(t *testing.T) {
	var buf bytes.Buffer
	tu := newTUI(&buf, 60, staticItems(mkItem(dlActive, "/d/a", 1, 2, time.Now())))

	tu.paint()
	buf.Reset()

	tu.logf("downloaded %s (66.9 MiB)", "CHUNK_05.data.message")
	tu.paint()

	out := buf.String()
	msg := strings.Index(out, "downloaded CHUNK_05.data.message (66.9 MiB)")
	slot := strings.Index(out, "downloads: 1 active")
	require.Positive(t, msg, "the finished chunk is emitted")
	assert.Less(t, msg, slot, "it goes out above the slots")
	assert.Empty(t, tu.pending, "and is not held for the next frame")

	buf.Reset()
	tu.paint()
	assert.NotContains(t, buf.String(), "CHUNK_05", "a line already emitted is never redrawn")
}

func TestTUI_PaintClearsRowsAShorterFrameLeavesBehind(t *testing.T) {
	var buf bytes.Buffer
	item := mkItem(dlActive, "/d/a", 1, 2, time.Now())
	tu := newTUI(&buf, 60, staticItems(item))

	tu.paint()
	tall := tu.painted

	// The transfer finishes, so its slot goes away.
	item.state.Store(dlDone)
	buf.Reset()
	tu.paint()

	assert.Equal(t, tall-1, tu.painted)
	assert.Contains(t, buf.String(), "\x1b[1A", "the vacated row is cleared and the cursor comes back up")
}

// A block of idle slots after the last transfer says nothing the summary does
// not, so Stop takes it off the screen and leaves the emitted lines behind.
func TestTUI_StopClearsTheBlockAndFlushesTheLastLine(t *testing.T) {
	var buf bytes.Buffer
	tu := newTUI(&buf, 60, staticItems(mkItem(dlActive, "/d/a", 1, 2, time.Now())))

	tu.paint()
	_, err := tu.Write([]byte("wrote ./ITEM_001.asset"))
	require.NoError(t, err)
	buf.Reset()

	tu.Stop()

	assert.Contains(t, buf.String(), "wrote ./ITEM_001.asset", "a line with no newline still goes out")
	assert.NotContains(t, buf.String(), "downloads: 1 active", "the block is gone")
	assert.Zero(t, tu.painted)
}

func TestTUI_StopIsIdempotentAndRestoresTheCursor(t *testing.T) {
	var buf bytes.Buffer
	tu := newTUI(&buf, 60, staticItems())
	tu.Start()
	tu.Stop()
	tu.Stop()

	out := buf.String()
	assert.Contains(t, out, ansiHideCur)
	assert.Equal(t, 1, strings.Count(out, ansiShowCur), "a second Stop must not repaint")
}

func TestTUI_InterruptRestoresTheTerminal(t *testing.T) {
	// The signal goes to the whole process, and `tuiExit` is a package var.
	t.Serial()
	var buf bytes.Buffer
	tu := newTUI(&buf, 60, staticItems())

	exited := make(chan int, 1)
	prev := tuiExit
	tuiExit = func(code int) { exited <- code }
	t.Cleanup(func() { tuiExit = prev })

	tu.Start()
	t.Cleanup(tu.Stop)
	self, err := os.FindProcess(os.Getpid())
	require.NoError(t, err)
	require.NoError(t, self.Signal(os.Interrupt))

	select {
	case code := <-exited:
		assert.Equal(t, interruptExitCode, code)
	case <-time.After(5 * time.Second):
		t.Fatal("the interrupt never reached the handler")
	}
	out := buf.String()
	assert.Contains(t, out, ansiShowCur, "a cut-short run must not leave the cursor hidden")
	assert.Contains(t, out, "left as .part files")
}

// Notify fans a signal out to every display the process started, and one process
// runs many in turn (the MCP server serves one per tool call). A retired display
// that answered would write over the display that replaced it, and would end
// that run. Driving onSignal directly pins the decision, because reaching it
// through a real signal depends on which select case the runtime picks.
func TestTUI_ARetiredDisplayIgnoresTheSignal(t *testing.T) {
	// The signal goes to the whole process, and `tuiExit` is a package var.
	t.Serial()
	exited := make(chan int, 4)
	prev := tuiExit
	tuiExit = func(code int) { exited <- code }
	t.Cleanup(func() { tuiExit = prev })

	var retired bytes.Buffer
	old := newTUI(&retired, 60, staticItems())
	old.Start()
	old.Stop()
	before := retired.String()

	old.onSignal()

	assert.Equal(t, before, retired.String(), "a retired display writes nothing on a signal")
	assert.NotContains(t, retired.String(), "left as .part files")
	assert.Empty(t, exited, "a retired display must not end the run")

	var live bytes.Buffer
	cur := newTUI(&live, 60, staticItems())
	cur.Start()
	t.Cleanup(cur.Stop)

	cur.onSignal()

	assert.Contains(t, live.String(), ansiShowCur, "the live display puts the cursor back")
	assert.Contains(t, live.String(), "left as .part files")
	require.Len(t, exited, 1)
	assert.Equal(t, interruptExitCode, <-exited)
}

func TestProgressLine_DropsColumnsAsTheTerminalNarrows(t *testing.T) {
	p := itemProgress{Done: 512, Total: 2048, Fraction: 0.25, Speed: 1024, ETA: 3 * time.Second, HasETA: true}

	wide := progressLine("archive.tar.gz", p, planProgressLayout(100))
	assert.Contains(t, wide, "archive.tar.gz")
	assert.Contains(t, wide, " 25%")
	assert.Contains(t, wide, "512 B / 2.0 KiB")
	assert.Contains(t, wide, "1.0 KiB/s")
	assert.Contains(t, wide, "ETA 00:03")
	assert.Contains(t, wide, "[===")
	assert.LessOrEqual(t, displayWidth(wide), 100)

	at80 := progressLine("archive.tar.gz", p, planProgressLayout(78))
	assert.NotContains(t, at80, "[=", "at 80 columns the bar yields to the numbers it duplicates")
	assert.Contains(t, at80, "1.0 KiB/s")
	assert.Contains(t, at80, "ETA 00:03")

	narrow := progressLine("archive.tar.gz", p, planProgressLayout(30))
	assert.LessOrEqual(t, displayWidth(narrow), 30)
	assert.Contains(t, narrow, "25%", "the percentage is never dropped")
	assert.NotContains(t, narrow, "KiB/s", "the rate goes once the bar is already gone")

	tiny := progressLine("archive.tar.gz", p, planProgressLayout(12))
	assert.Contains(t, tiny, "25%")

	assert.NotEmpty(t, progressLine("x", p, progressLayout{}), "an unset layout still renders")

	assert.Equal(t, maxLabelWidth, planProgressLayout(300).label, "a wide terminal does not strand the numbers")
	assert.Equal(t, minLabelWidth, planProgressLayout(4).label)
}

// A KiB-range rate ("900.0 KiB/s") is wider than a MiB-range one, and a value
// that reshapes its own row would break the frame into ragged columns.
func TestProgressLine_ColumnsAlignAcrossRows(t *testing.T) {
	lay := planProgressLayout(98)
	rows := []string{
		progressLine("ubuntu-25.04.iso", itemProgress{Done: 1258291, Total: 6291456, Fraction: 0.2, Speed: 921600, ETA: 5 * time.Second, HasETA: true}, lay),
		progressLine("dataset-a.parquet", itemProgress{Done: 1887436, Total: 8388608, Fraction: 0.22, Speed: 1400000, ETA: 4 * time.Second, HasETA: true}, lay),
		progressLine("waiting.bin", itemProgress{Done: 0, Total: -1, Fraction: -1}, lay),
	}
	for _, row := range rows {
		assert.Equal(t, strings.Index(rows[0], "["), strings.Index(row, "["),
			"every row's bar starts in the same column: %q", row)
		assert.LessOrEqual(t, displayWidth(row), 98)
	}
	assert.Contains(t, rows[0], "900.0 KiB/s", "a wide rate keeps its own cell")
	assert.Contains(t, rows[1], "1.3 MiB/s", "and does not reshape its neighbour")
}

func TestProgressLine_UnknownTotal(t *testing.T) {
	line := progressLine("stream.bin", itemProgress{Done: 10, Total: -1, Fraction: -1}, planProgressLayout(90))
	assert.Contains(t, line, "?%")
	assert.Contains(t, line, "10 B / ?")
}

func TestAggregateProgress_FloorTotalStillShowsAPercentage(t *testing.T) {
	floor := aggregateProgress(downloadTotals{Bytes: 512, Total: 2048, Elapsed: 2 * time.Second})
	assert.InDelta(t, 0.25, floor.Fraction, 0.001, "a queued file of unknown size must not blank the percentage")
	assert.True(t, floor.TotalIsFloor)
	assert.False(t, floor.HasETA, "no ETA against a denominator that is still growing")
	assert.InDelta(t, 256.0, floor.Speed, 1)
	assert.Contains(t, sizesText(floor), "2.0 KiB+", "the + says the total is a floor")

	known := aggregateProgress(downloadTotals{Bytes: 512, Total: 2048, TotalKnown: true, Elapsed: 2 * time.Second})
	assert.True(t, known.HasETA)
	assert.NotContains(t, sizesText(known), "+")

	idle := aggregateProgress(downloadTotals{TotalKnown: true})
	assert.Equal(t, float64(-1), idle.Fraction)
	assert.Zero(t, idle.Speed)
}

func TestProgressBar(t *testing.T) {
	assert.Equal(t, "[=====     ]", progressBar(0.5, 10))
	assert.Equal(t, "[          ]", progressBar(-1, 10), "an unknown total draws no guess")
	assert.Equal(t, "[==========]", progressBar(2, 10), "a bar never overflows its width")
}

func TestHumanBytes(t *testing.T) {
	cases := map[int64]string{
		0: "0 B", 999: "999 B", 1024: "1.0 KiB",
		1536: "1.5 KiB", 1048576: "1.0 MiB", 3221225472: "3.0 GiB",
	}
	for in, want := range cases {
		assert.Equal(t, want, humanBytes(in), "humanBytes(%d)", in)
	}
}

func TestShortDuration(t *testing.T) {
	assert.Equal(t, "00:07", shortDuration(7*time.Second))
	assert.Equal(t, "02:05", shortDuration(125*time.Second))
	assert.Equal(t, "1:01:01", shortDuration(3661*time.Second))
	assert.Equal(t, "00:00", shortDuration(-time.Second))
}

func TestClipDisplay(t *testing.T) {
	assert.Equal(t, "short", clipDisplay("short", 10))
	assert.Equal(t, "abcd~", clipDisplay("abcdefghij", 5))
	assert.Equal(t, "", clipDisplay("abc", 0))
	assert.LessOrEqual(t, displayWidth(clipDisplay("１２３４５", 6)), 6, "wide runes are measured in columns")
}

// itoa keeps the paint assertions readable without pulling in strconv.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var digits []byte
	for ; n > 0; n /= 10 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
	}
	return string(digits)
}
