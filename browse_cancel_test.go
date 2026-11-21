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

func TestCancelKillsARunningCommand(t *testing.T) {
	t.Serial("runs a command in process, which swaps the package's I/O")
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
