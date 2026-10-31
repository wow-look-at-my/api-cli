package main

import _ "embed"

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

//go:embed testdata/browse-config.xml
var browseConfig string

func loadBrowseConfig(t *testing.T) *Config {
	t.Helper()
	cfg, err := parseConfigXML([]byte(browseConfig))
	require.NoError(t, err)
	require.NoError(t, validate(cfg))
	return cfg
}

func browseEntryAt(t *testing.T, entries []*browseEntry, path string) *browseEntry {
	t.Helper()
	for _, e := range entries {
		if strings.Join(e.path, " ") == path {
			return e
		}
	}
	t.Fatalf("no entry %q", path)
	return nil
}

func TestFlattenBrowse(t *testing.T) {
	entries := flattenBrowse(loadBrowseConfig(t))
	var labels []string
	for _, e := range entries {
		labels = append(labels, e.label())
	}
	assert.Equal(t, []string{"repos/", "  list", "files", "raw", "fail"}, labels)

	list := browseEntryAt(t, entries, "repos list")
	assert.Equal(t, "Really?", list.confirm, "confirm= is inherited")
	assert.True(t, list.runnable())
	assert.False(t, entries[0].runnable())
	var ids []string
	for _, f := range list.fields {
		ids = append(ids, f.id)
	}
	assert.Equal(t, []string{"arg-owner", "flag-limit", "flag-archived", "flag-topic"}, ids)
	assert.Equal(t, "rest", browseEntryAt(t, entries, "raw").fields[0].id)
}

func TestBrowseArgv(t *testing.T) {
	entries := flattenBrowse(loadBrowseConfig(t))
	list := browseEntryAt(t, entries, "repos list")

	_, err := list.argv()
	assert.EqualError(t, err, "owner is required")

	list.fields[0].value = "acme"
	argv, err := list.argv()
	require.NoError(t, err)
	assert.Equal(t, []string{"repos", "list", "acme"}, argv, "an empty field leaves the default to cobra")

	list.fields[1].value = "5"
	list.fields[2].checked = true
	list.fields[3].value = "go, cli"
	argv, err = list.argv()
	require.NoError(t, err)
	assert.Equal(t, []string{"repos", "list", "--limit=5", "--archived=true", "--topic=go", "--topic=cli", "acme"}, argv)

	list.fields[0].value = "-dash"
	argv, err = list.argv()
	require.NoError(t, err)
	assert.Equal(t, []string{"--", "-dash"}, argv[len(argv)-2:], "a dash-led positional goes after the separator")

	files := browseEntryAt(t, entries, "files")
	files.fields[0].value = "a b  c"
	argv, err = files.argv()
	require.NoError(t, err)
	assert.Equal(t, []string{"files", "a", "b", "c"}, argv)

	raw := browseEntryAt(t, entries, "raw")
	raw.fields[0].value = "-x y"
	argv, err = raw.argv()
	require.NoError(t, err)
	assert.Equal(t, []string{"raw", "-x", "y"}, argv)

	assert.Equal(t, "tool repos list 'a b' x", commandLine("tool", []string{"repos", "list", "a b", "x"}))
}

func TestBrowseExec(t *testing.T) {
	t.Serial()
	cfg := loadBrowseConfig(t)

	out, code := browseExec(cfg, []string{"repos", "list", "--limit=5", "--topic=a", "--topic=b", "acme"}, 80, 24)
	assert.Equal(t, 0, code)
	assert.Contains(t, out, "list acme 5 false a+b", "confirm is skipped, because the browser asked already")

	out, code = browseExec(cfg, []string{"fail"}, 80, 24)
	assert.Equal(t, 3, code)
	assert.Contains(t, out, "broke")

	out, code = browseExec(cfg, []string{"repos", "list"}, 80, 24)
	assert.Equal(t, 1, code)
	assert.Contains(t, out, "Error:", "a cobra error lands in the output pane")
}

func key(s string) tea.KeyPressMsg {
	switch s {
	case "enter":
		return tea.KeyPressMsg{Code: tea.KeyEnter}
	case "down":
		return tea.KeyPressMsg{Code: tea.KeyDown}
	case "up":
		return tea.KeyPressMsg{Code: tea.KeyUp}
	case "tab":
		return tea.KeyPressMsg{Code: tea.KeyTab}
	case "esc":
		return tea.KeyPressMsg{Code: tea.KeyEscape}
	case "backspace":
		return tea.KeyPressMsg{Code: tea.KeyBackspace}
	}
	r := []rune(s)[0]
	return tea.KeyPressMsg{Code: r, Text: s}
}

// browseHarness drives the model with keys, and runs each command it asks for in place.
type browseHarness struct {
	t    *testing.T
	m    *browseModel
	runs [][]string
}

func newBrowseHarness(t *testing.T) *browseHarness {
	t.Helper()
	t.Serial()
	m, err := newBrowseModel(loadBrowseConfig(t))
	require.NoError(t, err)
	h := &browseHarness{t: t, m: m}
	m.exec = func(argv []string, _, _ int) (string, int) {
		h.runs = append(h.runs, argv)
		return "ran " + strings.Join(argv, " ") + "\n\tdone", 0
	}
	return h
}

func (h *browseHarness) press(keys ...string) {
	for _, k := range keys {
		_, cmd := h.m.Update(key(k))
		h.drain(cmd)
		h.m.frame()
	}
}

// drain runs the commands a key produced, except the spinner's ticker.
func (h *browseHarness) drain(cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	switch msg := cmd().(type) {
	case tea.BatchMsg:
		for _, c := range msg {
			h.drain(c)
		}
	case browseRunMsg:
		h.m.Update(msg)
	}
}

func (h *browseHarness) typeText(s string) {
	for _, r := range s {
		h.press(string(r))
	}
}

func TestBrowseModelFillAndRun(t *testing.T) {
	h := newBrowseHarness(t)
	m := h.m

	h.press("down")
	assert.Equal(t, []string{"repos", "list"}, m.entry().path)
	h.press("enter")
	focused, _ := m.view.UI().Focused()
	assert.Equal(t, "arg-owner", focused)

	h.typeText("acmx")
	h.press("backspace")
	h.typeText("e")
	assert.Equal(t, "acme", m.entry().fields[0].value)
	assert.Contains(t, m.frame(), "tool repos list acme")

	h.press("enter")
	assert.True(t, m.confirming, "repos asks for confirmation")
	assert.Empty(t, h.runs)
	h.press("esc")
	assert.False(t, m.confirming)
	assert.Empty(t, h.runs)

	h.press("enter")
	h.drain(m.act("confirm"))
	require.Len(t, h.runs, 1)
	assert.Equal(t, []string{"repos", "list", "acme"}, h.runs[0])
	assert.False(t, m.running)
	assert.Equal(t, []string{"ran repos list acme", "    done"}, m.output)
	frame := m.frame()
	assert.Contains(t, frame, "exit 0")
	assert.Contains(t, frame, "ran repos list acme")
}

func TestBrowseModelToggleAndErrors(t *testing.T) {
	h := newBrowseHarness(t)
	m := h.m

	h.press("down", "enter")
	h.drain(m.act("toggle:flag-archived"))
	assert.True(t, m.field("flag-archived").checked)

	m.view.UI().Focus("tree")
	h.press("q")
	assert.Equal(t, 0, m.code)

	m.pick(1)
	m.entry().fields[0].value = ""
	h.drain(m.start())
	assert.Equal(t, "owner is required", m.status)
	assert.Empty(t, h.runs)

	m.pick(0)
	assert.Nil(t, m.start(), "a group does not run")
	assert.Contains(t, m.frame(), "A group.")

	m.pick(4)
	h.drain(m.start())
	assert.Equal(t, [][]string{{"fail"}}, h.runs)

	m.finish(browseRunMsg{line: "tool fail", output: "broke\n", code: 3})
	assert.Equal(t, "exit 3  tool fail", m.status)
	assert.Equal(t, "bad", m.statusStyle)
}

func TestBrowseTreeWindowsTheSelection(t *testing.T) {
	h := newBrowseHarness(t)
	m := h.m
	m.height = 10
	assert.Equal(t, 3, m.treeRows())
	m.pick(4)
	assert.Equal(t, 2, m.treeStart())
	assert.NotContains(t, m.frame(), "repos/")
}

func TestTUISubcommand(t *testing.T) {
	cfg := loadBrowseConfig(t)
	code, _, _ := execCmdFull(t, cfg, "tui")
	assert.Equal(t, 1, code, "tui refuses to start without a terminal")
	assert.Equal(t, 2, run([]string{"tui"}, &strings.Builder{}), "tui needs a config like any other command")

	bad, err := parseConfigXML([]byte(`<config name="x"><command name="tui"><run>true</run></command></config>`))
	require.NoError(t, err)
	assert.ErrorContains(t, validate(bad), `name "tui" is reserved for a built-in subcommand`)
}
