package main

import (
	"bytes"
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

func TestNewTUI_SizesTheLogRegion(t *testing.T) {
	cases := []struct{ height, logLines, want int }{
		{height: 50, logLines: 0, want: 15}, // capped at 15
		{height: 20, logLines: 0, want: 10}, // half the terminal
		{height: 4, logLines: 0, want: 3},   // never below the floor
		{height: 50, logLines: 4, want: 4},  // explicit wins
	}
	for _, c := range cases {
		got := newTUI(&bytes.Buffer{}, 100, c.height, c.logLines, staticItems())
		assert.Equal(t, c.want, got.logCap, "height=%d logLines=%d", c.height, c.logLines)
	}

	def := newTUI(&bytes.Buffer{}, 0, 0, 0, staticItems())
	assert.Equal(t, 80, def.width, "a size-less writer falls back to 80 columns")
}

func TestTUI_WriteFillsTheLogRing(t *testing.T) {
	tu := newTUI(&bytes.Buffer{}, 80, 24, 3, staticItems())

	_, err := tu.Write([]byte("one\ntwo\n"))
	require.NoError(t, err)
	_, err = tu.Write([]byte("thr"))
	require.NoError(t, err)
	assert.Equal(t, []string{"one", "two"}, tu.logs, "a partial line waits for its newline")

	_, err = tu.Write([]byte("ee\r\nfour\nfive\n"))
	require.NoError(t, err)
	assert.Equal(t, []string{"three", "four", "five"}, tu.logs, "the region scrolls at its cap")
}

func TestTUI_FrameShowsActiveDownloadsAndTotals(t *testing.T) {
	now := time.Now()
	tu := newTUI(&bytes.Buffer{}, 100, 24, 4, staticItems(
		mkItem(dlActive, "/d/big.iso", 512, 2048, now.Add(-2*time.Second)),
		mkItem(dlQueued, "/d/later.iso", 0, -1, time.Time{}),
		mkItem(dlDone, "/d/done.iso", 100, 100, now.Add(-9*time.Second)),
	))
	tu.logf("downloading %s", "big.iso")

	lines := tu.frame(now)
	joined := strings.Join(lines, "\n")

	assert.Contains(t, lines[0], "1 active, 1 queued, 1 done")
	assert.Contains(t, joined, "big.iso")
	assert.Contains(t, joined, " 25%", "512 of 2048 bytes")
	assert.Contains(t, joined, "TOTAL")
	assert.Contains(t, joined, "downloading big.iso", "the log region carries the downloader's own lines")

	// header + 1 active + TOTAL + rule + the full log region, always.
	assert.Len(t, lines, 4+tu.logCap)
}

func TestTUI_FrameCountsFailures(t *testing.T) {
	tu := newTUI(&bytes.Buffer{}, 80, 24, 3, staticItems(mkItem(dlFailed, "/d/x", 0, -1, time.Now())))
	assert.Contains(t, tu.frame(time.Now())[0], "1 failed")
}

func TestTUI_PaintRewritesInPlace(t *testing.T) {
	var buf bytes.Buffer
	now := time.Now()
	tu := newTUI(&buf, 60, 24, 3, staticItems(mkItem(dlActive, "/d/a", 1, 2, now)))

	tu.paint()
	first := buf.String()
	assert.NotContains(t, first, "A\r", "the first frame has nothing to move up over")
	assert.Contains(t, first, ansiClearLine)
	painted := tu.painted

	buf.Reset()
	tu.paint()
	assert.Contains(t, buf.String(), "\x1b["+itoa(painted)+"A", "later frames redraw over the last one")
}

func TestTUI_PaintClearsRowsAShorterFrameLeavesBehind(t *testing.T) {
	var buf bytes.Buffer
	item := mkItem(dlActive, "/d/a", 1, 2, time.Now())
	tu := newTUI(&buf, 60, 24, 3, staticItems(item))

	tu.paint()
	tall := tu.painted

	// The transfer finishes, so its progress line goes away.
	item.state.Store(dlDone)
	buf.Reset()
	tu.paint()

	assert.Equal(t, tall-1, tu.painted)
	assert.Contains(t, buf.String(), "\x1b[1A", "the vacated row is cleared and the cursor comes back up")
}

func TestTUI_StopIsIdempotentAndRestoresTheCursor(t *testing.T) {
	var buf bytes.Buffer
	tu := newTUI(&buf, 60, 24, 3, staticItems())
	tu.Start()
	tu.Stop()
	tu.Stop()

	out := buf.String()
	assert.Contains(t, out, ansiHideCur)
	assert.Equal(t, 1, strings.Count(out, ansiShowCur), "a second Stop must not repaint")
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
