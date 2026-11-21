package main

import _ "embed"

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wow-look-at-my/tml"
)

//go:embed testdata/live-component.xml
var liveComponent string

// liveDecl is a screen whose list selection and log scroll the program keeps.
func liveDecl(t *testing.T) *TML {
	t.Helper()
	t.Serial("sets configDir, which every <tml> src resolves against")
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "live.tml"), []byte(liveComponent), 0o600))
	prev := configDir
	configDir = dir
	t.Cleanup(func() { configDir = prev })
	return &TML{Src: "live.tml", Props: []TMLProp{
		{Name: "jobs", From: "jobs"},
		{Name: "lines", From: "lines"},
		{Name: "picked", Select: "jobs"},
		{Name: "offset", Scroll: "log"},
	}}
}

func liveData() map[string]any {
	return map[string]any{"jobs": []any{"build", "test"}, "lines": []any{"1", "2", "3", "4", "5", "6"}}
}

func TestTMLLiveKeepsSelectionAndScroll(t *testing.T) {
	decl := liveDecl(t)
	assert.True(t, decl.interactive())
	live, err := newTMLLive(decl)
	require.NoError(t, err)
	live.parsed, live.ctx = liveData(), map[string]any{}
	require.NoError(t, live.redraw())
	assert.Equal(t, "0", live.props["picked"].String(), "a selection starts at the first row")
	_, err = live.view.Render(live.props, 40, 12)
	require.NoError(t, err)

	require.True(t, live.view.UI().Focus("jobs"))
	assert.True(t, live.key("down", 12))
	assert.True(t, live.key("down", 12))
	require.NoError(t, live.redraw())
	assert.Equal(t, "1", live.props["picked"].String(), "the selection stops at the last row")

	changed, refresh := live.event(tml.Event{Kind: tml.Activated, ID: "jobs", Y: 0})
	assert.True(t, changed)
	assert.False(t, refresh)
	assert.Equal(t, 0, live.state["select:jobs"], "a click picks the row under it")

	changed, _ = live.event(tml.Event{Kind: tml.Scrolled, ID: "log", Delta: 2})
	assert.True(t, changed)
	require.NoError(t, live.redraw())
	assert.Equal(t, "2", live.props["offset"].String(), "the wheel scrolls the region the prop names")

	_, refresh = live.event(tml.Event{Kind: tml.Activated, ID: "again", Action: "refresh"})
	assert.True(t, refresh, "action=refresh runs the leaf again")

	require.True(t, live.view.UI().Focus("again"))
	assert.True(t, live.key("pgdown", 12), "the page keys scroll the first scroll region")
	assert.False(t, live.key("x", 12))
}

func TestTMLLiveOneShotFrameHoldsZero(t *testing.T) {
	decl := liveDecl(t)
	props, err := tmlProps(decl, liveData(), map[string]any{}, nil)
	require.NoError(t, err)
	assert.Equal(t, "0", props["picked"].String())
	assert.Equal(t, "0", props["offset"].String())
	assert.False(t, (&TML{Props: []TMLProp{{Name: "a", From: "b"}}}).interactive())
}

func TestTMLLiveProgramDrawsTheComponent(t *testing.T) {
	t.Serial("the watch program sets exitCode, and the screen's src resolves against configDir")
	decl := liveDecl(t)
	prev := exitCode
	t.Cleanup(func() { exitCode = prev })
	runs := 0
	body := func() error {
		runs++
		if runs == 3 {
			return errors.New("the api is down")
		}
		if tmlTap != nil {
			tmlTap(liveData(), map[string]any{})
		}
		return nil
	}
	keys, out, done := startTMLWith(t, body, decl)
	require.Eventually(t, func() bool { return strings.Contains(out.String(), "build") }, 5*time.Second, 5*time.Millisecond, "the component draws the run's data")
	require.Eventually(t, func() bool { return strings.Contains(out.String(), "the api is down") }, 5*time.Second, 5*time.Millisecond, "a failed run shows its error in place of the component")
	_, err := keys.Write([]byte("q"))
	require.NoError(t, err)
	require.NoError(t, waitTMLProgram(t, done))
}

func TestTMLLiveMissingComponentFailsLoud(t *testing.T) {
	decl := liveDecl(t)
	decl.Src = "nope.tml"
	err := runTMLWith(time.Millisecond, func() error { return nil }, decl, tml.ProgramOptions{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "nope.tml")
}
