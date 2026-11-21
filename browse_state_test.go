package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBrowseStateRoundTrip(t *testing.T) {
	h := newDataHarness(t)
	path := filepath.Join(t.TempDir(), "state.json")
	h.m.attachState(path)

	issues := h.m.byPath["issues"]
	h.m.pick(issues)
	issues.fields[0].value = "zed"
	issues.fields[1].value = "s3cret"
	h.m.start(false)

	data, err := os.ReadFile(path)
	require.NoError(t, err, "a run saves the state")
	assert.Contains(t, string(data), `"owner": "zed"`)
	assert.NotContains(t, string(data), "s3cret", "an input named like a secret never reaches the disk")

	h2 := newDataHarness(t)
	h2.m.attachState(path)
	assert.Equal(t, "issues", h2.m.cur.pathString(), "the next session opens on the command the last one picked")
	assert.Equal(t, "zed", h2.m.cur.fields[0].value, "the form comes back")
	assert.Empty(t, h2.m.cur.fields[1].value)
	assert.Equal(t, "zed", h2.m.memory["owner"], "the shared values come back")
}

func TestBrowseStateThatDoesNotParse(t *testing.T) {
	h := newDataHarness(t)
	path := filepath.Join(t.TempDir(), "state.json")
	require.NoError(t, os.WriteFile(path, []byte("{not json"), 0o600))
	h.m.attachState(path)
	assert.Contains(t, h.m.status, "saved state not restored", "a broken file is reported, not dropped in silence")
	assert.Equal(t, "bad", h.m.statusStyle)
}

func TestStatePathSeparatesConfigs(t *testing.T) {
	t.Serial("sets XDG_CACHE_HOME, which os.UserCacheDir reads for every test in the process")
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	a, err := statePath(&Config{Name: "gh", Dir: "/one"})
	require.NoError(t, err)
	b, err := statePath(&Config{Name: "gh", Dir: "/two"})
	require.NoError(t, err)
	assert.NotEqual(t, a, b, "two configs with one name keep separate files")
	assert.True(t, strings.HasPrefix(filepath.Base(a), "gh-"))
}

// complete runs cobra's hidden completion command, exactly as a shell does on a tab.
func complete(t *testing.T, cfg *Config, words ...string) string {
	t.Helper()
	var buf bytes.Buffer
	root := newRoot(cfg)
	root.SetOut(&buf)
	root.SetErr(&bytes.Buffer{})
	root.SetArgs(append([]string{"__complete"}, words...))
	require.NoError(t, root.Execute())
	return buf.String()
}

func TestSuggestCompletion(t *testing.T) {
	t.Serial("a tab runs each suggest source in process, through the package's swapped I/O")
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	cfg, err := parseConfigXML([]byte(browseDataConfig))
	require.NoError(t, err)
	require.NoError(t, validate(cfg))

	out := complete(t, cfg, "repos", "view", "acme", "")
	assert.Contains(t, out, "api\n")
	assert.Contains(t, out, "web\n", "the source took owner from the command line")
	out = complete(t, cfg, "repos", "view", "acme", "w")
	assert.NotContains(t, out, "api")
	assert.Contains(t, out, "web")

	assert.NotContains(t, complete(t, cfg, "repos", "view", "", ""), "api", "a source that lacks a required input never runs")

	assert.NotContains(t, complete(t, cfg, "ping", "--repo", ""), "api", "ping has no owner, and nothing is saved")
	path, err := statePath(cfg)
	require.NoError(t, err)
	require.NoError(t, saveState(path, browseState{Memory: map[string]string{"owner": "acme"}}))
	assert.Contains(t, complete(t, cfg, "ping", "--repo", ""), "web", "the owner the tui saved fills the source")

	out = complete(t, cfg, "repos", "list", "--state", "")
	assert.Contains(t, out, "closed", "choices= keeps its own completion")
}
