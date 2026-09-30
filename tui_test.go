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

// frameAt renders the region's props at a width, the way the live region lays
// out a frame.
func frameAt(t *testing.T, tu *tui, width int, now time.Time) string {
	t.Helper()
	view, err := downloadsView()
	require.NoError(t, err)
	out, err := view.RenderFit(tu.props(now), width, 40)
	require.NoError(t, err)
	return stripANSI(out)
}

func testTUI(t *testing.T, out *bytes.Buffer, items ...*downloadItem) *tui {
	t.Helper()
	tu, err := newTUI(out, out, staticItems(items...))
	require.NoError(t, err)
	return tu
}

func TestTUI_FrameIsOnlyTheSlotsAndTotals(t *testing.T) {
	now := time.Now()
	tu := testTUI(t, &bytes.Buffer{},
		mkItem(dlActive, "/d/big.iso", 512, 2048, now.Add(-2*time.Second)),
		mkItem(dlQueued, "/d/later.iso", 0, -1, time.Time{}),
		mkItem(dlDone, "/d/done.iso", 100, 100, now.Add(-9*time.Second)),
	)

	frame := frameAt(t, tu, 100, now)
	lines := strings.Split(frame, "\n")
	assert.Contains(t, lines[0], "1 active, 1 queued, 1 done")
	assert.Contains(t, frame, "big.iso", "an in-flight transfer holds a row")
	assert.Contains(t, frame, " 25%", "512 of 2048 bytes")
	assert.Contains(t, frame, "TOTAL")
	assert.NotContains(t, frame, "done.iso", "a finished transfer holds no row")
	assert.Len(t, lines, 3, "the head, the active row and TOTAL")
}

func TestTUI_FrameMarksFailures(t *testing.T) {
	tu := testTUI(t, &bytes.Buffer{}, mkItem(dlFailed, "/d/x", 0, -1, time.Now()))
	props := tu.props(time.Now())
	assert.Contains(t, props["head"].String(), "1 failed")
	assert.Equal(t, "head.failed", props["headStyle"].String(), "a failure turns the head red")
}

// The block carries the running step as its own line, and drops it when the
// steps end.
func TestTUI_ShowsTheRunningStep(t *testing.T) {
	tu := testTUI(t, &bytes.Buffer{})
	tu.setStep(&stepProgress{Step: "listing", Index: 2, Total: 5, Item: "b", Attempt: 3, Attempts: 9})
	assert.Contains(t, frameAt(t, tu, 100, time.Now()), "listing 2/5 b  attempt 3/9")
	tu.setStep(nil)
	assert.NotContains(t, frameAt(t, tu, 100, time.Now()), "listing")
}

// A narrow terminal drops the bar before the numbers it repeats. The rows keep
// their columns in line and never paint past the edge.
func TestTUI_ColumnsGiveWayAsTheTerminalNarrows(t *testing.T) {
	now := time.Now()
	tu := testTUI(t, &bytes.Buffer{},
		mkItem(dlActive, "/d/archive.tar.gz", 512, 2048, now.Add(-2*time.Second)),
		mkItem(dlActive, "/d/stream.bin", 10, -1, now.Add(-time.Second)),
	)

	wide := frameAt(t, tu, 120, now)
	assert.Contains(t, wide, " 25%")
	assert.Contains(t, wide, "512 B / 2.0 KiB")
	assert.Contains(t, wide, "KiB/s")
	assert.Contains(t, wide, "10 B / ?", "an unknown length still reports what it knows")

	narrow := frameAt(t, tu, 50, now)
	assert.Contains(t, narrow, "25%", "the percentage outlasts the bar")
	assert.NotContains(t, narrow, "KiB/s", "the rate goes early")
	for _, line := range strings.Split(narrow, "\n") {
		assert.LessOrEqual(t, displayWidth(line), 50, "%q", line)
	}
}

func TestProgressRecord_UnknownLengthHidesTheBar(t *testing.T) {
	unknown := progressRecord("s", "active", itemProgress{Done: 10, Total: -1, Fraction: -1})
	assert.False(t, unknown["known"].Bool())
	assert.NotContains(t, unknown, "percent")

	known := progressRecord("s", "active", itemProgress{Done: 5, Total: 10, Fraction: 0.5})
	assert.True(t, known["known"].Bool())
	assert.Equal(t, "0.5000", known["value"].String())
	assert.Equal(t, " 50%", known["percent"].String())
}

// The region prints queued lines above itself and comes off the screen at
// Stop. A partial line with no newline still goes out.
func TestTUI_StopFlushesTheLines(t *testing.T) {
	var buf bytes.Buffer
	tu := testTUI(t, &buf, mkItem(dlActive, "/d/a", 1, 2, time.Now()))
	require.NoError(t, tu.Start())

	tu.logf("downloaded %s (66.9 MiB)", "CHUNK_05.data.message")
	_, err := tu.Write([]byte("wrote ./ITEM_001.asset"))
	require.NoError(t, err)
	tu.Stop()
	tu.Stop()

	out := stripANSI(buf.String())
	assert.Contains(t, out, "downloaded CHUNK_05.data.message (66.9 MiB)")
	assert.Contains(t, out, "wrote ./ITEM_001.asset")
	assert.Less(t, strings.Index(out, "CHUNK_05"), strings.Index(out, "ITEM_001"), "lines keep their order")
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

	blind := aggregateProgress(downloadTotals{Bytes: 2048, Total: 2048, Queued: 9, Elapsed: 2 * time.Second})
	assert.Equal(t, float64(-1), blind.Fraction)
	assert.InDelta(t, 1024.0, blind.Speed, 1, "the rate is still a fact")
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
