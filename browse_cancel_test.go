package main

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBrowseCancelStopsTheRun(t *testing.T) {
	h := newBrowseHarness(t)
	h.deferred = true
	h.m.pick(browseEntryAt(t, h.m.entries, "fail"))
	h.m.start(false)
	require.True(t, h.m.running)
	assert.Contains(t, h.m.hint(), "ctrl+x cancels")
	h.press("ctrl+x")
	assert.Equal(t, "cancelling…", h.m.status)
	assert.Error(t, h.m.runContext().Err(), "the run's context is cancelled")
	h.step()
	assert.False(t, h.m.running)
	assert.Contains(t, h.m.status, "cancelled")
	assert.Empty(t, h.queue, "a cancelled run schedules nothing")
}

func TestBrowseShowsARunInProgress(t *testing.T) {
	h := newBrowseHarness(t)
	h.deferred = true
	h.m.pick(browseEntryAt(t, h.m.entries, "fail"))
	h.m.start(false)
	_, _ = h.m.live.Write([]byte("downloading 1 of 3\ndownloading 2 of 3\n"))
	frame := h.m.frame()
	assert.Contains(t, frame, "downloading 2 of 3", "the pane shows what the run printed so far")
	assert.Contains(t, frame, "2 lines so far")
	assert.Contains(t, frame, "running fail")

	h.m.liveEvery = time.Millisecond
	assert.True(t, h.m.liveTick(browseLiveMsg{id: h.m.runID}), "a tick of the run in progress repaints")
	require.Len(t, h.queue, 2, "and asks for the next one")
	assert.False(t, h.m.liveTick(browseLiveMsg{id: h.m.runID - 1}), "a tick from an older run does nothing")

	h.step()
	assert.False(t, h.m.running)
	assert.NotContains(t, h.m.frame(), "so far", "the finished result replaces the live view")
}

func TestLiveCopyOfARun(t *testing.T) {
	t.Serial("browseExecLive points execStdout at its own buffer for the length of the run")
	cfg, err := parseConfigXML([]byte(`<config name="x"><command name="two"><run>echo one; echo two</run></command></config>`))
	require.NoError(t, err)
	require.NoError(t, validate(cfg))
	live := &liveBuffer{}
	out := browseExecLive(context.Background(), live, cfg, []string{"two"}, 80, 24)
	assert.Equal(t, out.text, live.String(), "the live copy holds everything the run printed")
}

func TestCancelKillsARunningCommand(t *testing.T) {
	t.Serial("browseExecCtx sets runCtx, which every command in the process reads")
	cfg, err := parseConfigXML([]byte(`<config name="x"><command name="slow"><run>echo start; sleep 5; echo end</run></command></config>`))
	require.NoError(t, err)
	require.NoError(t, validate(cfg))
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	began := time.Now()
	out := browseExecCtx(ctx, cfg, []string{"slow"}, 80, 24)
	assert.Less(t, time.Since(began), 4*time.Second, "the cancel ends the run long before the sleep does")
	assert.NotEqual(t, 0, out.code)
	assert.Contains(t, out.text, "start")
	assert.NotContains(t, out.text, "end")
}
