package main

import _ "embed"

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wow-look-at-my/tml"
	"github.com/wow-look-at-my/tml/input"
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
	assert.Equal(t, []string{"repos ▾", "  list !", "files", "raw", "fail"}, labels, "list inherits confirm=, so it is marked")

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

	out := browseExec(cfg, []string{"repos", "list", "--limit=5", "--topic=a", "--topic=b", "acme"}, 80, 24)
	assert.Equal(t, 0, out.code)
	assert.Contains(t, out.text, "list acme 5 false a+b", "confirm is skipped, because the browser asked already")
	assert.Equal(t, out.text, out.raw, "a shell command prints its body as it is")

	out = browseExec(cfg, []string{"fail"}, 80, 24)
	assert.Equal(t, 3, out.code)
	assert.Contains(t, out.text, "broke")

	out = browseExec(cfg, []string{"repos", "list"}, 80, 24)
	assert.Equal(t, 1, out.code)
	assert.Contains(t, out.text, "Error:", "a cobra error lands in the output pane")
}

// browseHarness is the program for the model: it sends keys, and runs each command in place.
type browseHarness struct {
	t      *testing.T
	m      *browseModel
	runs   [][]string
	quit   bool
	copied []string
	// deferred queues work instead of running it, for a test that steps through it.
	deferred bool
	queue    []func() tml.Msg
}

func (h *browseHarness) Quit() { h.quit = true }

func (h *browseHarness) Copy(text string) { h.copied = append(h.copied, text) }

func (h *browseHarness) Go(work func() tml.Msg) {
	if h.deferred {
		h.queue = append(h.queue, work)
		return
	}
	h.m.update(h, work())
}

// step runs the oldest queued work and hands its message to the model.
func (h *browseHarness) step() {
	if len(h.queue) == 0 {
		return
	}
	work := h.queue[0]
	h.queue = h.queue[1:]
	h.m.update(h, work())
}

func newBrowseHarness(t *testing.T) *browseHarness {
	t.Helper()
	t.Serial()
	m, err := newBrowseModel(loadBrowseConfig(t))
	require.NoError(t, err)
	h := &browseHarness{t: t, m: m}
	m.host = h
	m.liveEvery = 0
	m.exec = func(argv []string, _, _ int) browseOutput {
		h.runs = append(h.runs, argv)
		text := "ran " + strings.Join(argv, " ") + "\n\tdone"
		return browseOutput{text: text, raw: text}
	}
	return h
}

func (h *browseHarness) focused() string {
	f, _ := h.m.view.UI().Focused()
	return f
}

// press sends each key as a program does: to the model first, then to the focus ring.
func (h *browseHarness) press(keys ...string) {
	for _, k := range keys {
		msg := input.ParseKey(k)
		if !h.m.update(h, msg) {
			for _, event := range h.m.view.UI().Update(msg) {
				h.m.update(h, event)
			}
		}
		h.m.frame()
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
	assert.Equal(t, []string{"repos", "list"}, m.cur.path)
	h.press("enter")
	assert.Equal(t, "arg-owner", h.focused(), "enter on a command with an empty required arg fills it in")

	h.typeText("acmx")
	h.press("backspace")
	h.typeText("e")
	assert.Equal(t, "acme", m.cur.fields[0].value)
	assert.Contains(t, m.frame(), "tool repos list acme")

	h.press("enter")
	assert.True(t, m.confirming, "repos asks for confirmation")
	assert.Contains(t, m.frame(), "Really?", "the popup shows the command's own prompt")
	assert.Empty(t, h.runs)
	h.press("esc")
	assert.False(t, m.confirming)
	assert.Empty(t, h.runs)

	h.press("enter")
	m.act("confirm")
	require.Len(t, h.runs, 1)
	assert.Equal(t, []string{"repos", "list", "acme"}, h.runs[0])
	assert.False(t, m.running)
	require.NotNil(t, m.top())
	assert.Equal(t, []string{"ran repos list acme", "    done"}, m.top().lines())
	assert.Equal(t, viewText, m.top().view, "output that is not JSON shows as text")
	frame := m.frame()
	assert.Contains(t, frame, "exit 0")
	assert.Contains(t, frame, "ran repos list acme")
}

func TestBrowseModelToggleAndErrors(t *testing.T) {
	h := newBrowseHarness(t)
	m := h.m

	h.press("down", "right")
	assert.Equal(t, "arg-owner", h.focused(), "the right arrow moves into the form")
	m.act("toggle:flag-archived")
	assert.True(t, m.field("flag-archived").checked)

	m.focus("tree")
	h.press("q")
	assert.True(t, h.quit)
	assert.Equal(t, 0, m.code)

	list := browseEntryAt(t, m.entries, "repos list")
	m.pick(list)
	list.fields[0].value = ""
	m.start(false)
	assert.Equal(t, "owner is required", m.status)
	assert.Equal(t, "arg-owner", h.focused(), "a missing input takes the focus")
	assert.Empty(t, h.runs)

	m.pick(m.entries[0])
	m.start(false)
	assert.Empty(t, h.runs, "a group does not run")
	assert.Contains(t, m.frame(), "Repositories.")

	m.pick(browseEntryAt(t, m.entries, "fail"))
	m.start(false)
	assert.Equal(t, [][]string{{"fail"}}, h.runs)

	m.runID++
	m.launched = &pendingRun{entry: m.cur, argv: []string{"fail"}}
	m.running = true
	m.finish(browseRunMsg{id: m.runID, line: "tool fail", out: browseOutput{text: "broke\n", raw: "broke\n", code: 3}})
	assert.Contains(t, m.status, "exit 3")
	assert.Contains(t, m.status, "tool fail")
	assert.Equal(t, "bad", m.statusStyle)
}

func TestBrowseModelEventsAndExits(t *testing.T) {
	h := newBrowseHarness(t)
	m := h.m

	assert.False(t, m.update(h, tml.Resize{Width: 90, Height: 20}), "a resize is not consumed")
	assert.Equal(t, 90, m.width)
	assert.Equal(t, 20, m.height)

	h.press("down", "right")
	assert.True(t, m.update(h, tml.Event{Kind: tml.Changed, ID: "arg-owner", Value: "zed"}))
	assert.Equal(t, "zed", m.field("arg-owner").value, "the field keeps an edit from the textbox")

	h.press("esc")
	assert.Equal(t, "tree", h.focused(), "esc in the form goes back to the tree")
	assert.False(t, h.quit)
	h.press("esc")
	assert.True(t, h.quit, "esc in the tree quits")

	assert.True(t, m.update(h, tml.Event{Kind: tml.Activated, ID: "tree", Y: 0}))
	assert.Equal(t, m.entries[0], m.cur, "a click on a tree row picks it")

	h.quit = false
	h.press("ctrl+c")
	assert.True(t, h.quit)
	assert.Equal(t, interruptExitCode, m.code)
}

func TestBrowseTreeWindowsTheSelection(t *testing.T) {
	h := newBrowseHarness(t)
	m := h.m
	m.height = 10
	assert.Equal(t, 3, m.treeRows())
	m.pick(browseEntryAt(t, m.entries, "fail"))
	assert.Equal(t, 2, m.treeStart())
	assert.NotContains(t, m.frame(), "repos ▾")
}

func TestBrowseTreeCollapsesAndFilters(t *testing.T) {
	h := newBrowseHarness(t)
	m := h.m

	h.press("left")
	assert.True(t, m.entries[0].collapsed, "left shuts a group")
	assert.Len(t, m.visible(), 4)
	assert.Contains(t, m.frame(), "repos ▸")
	h.press("enter")
	assert.False(t, m.entries[0].collapsed, "enter opens a shut group")

	h.press("down", "left")
	assert.Equal(t, m.entries[0], m.cur, "left on a child goes to its group")

	h.press("/")
	assert.Equal(t, "filter", h.focused())
	h.typeText("fa")
	assert.Equal(t, []string{"fail"}, entryPaths(m.visible()))
	assert.Equal(t, "fail", m.cur.pathString(), "the selection follows the first match")
	h.press("esc")
	assert.Empty(t, m.filter)
	assert.Equal(t, "tree", h.focused())

	h.press("/")
	h.typeText("list")
	assert.Equal(t, []string{"repos", "repos list"}, entryPaths(m.visible()), "a match keeps its group")
	h.press("enter")
	assert.Equal(t, "arg-owner", h.focused(), "enter from the filter opens what it found")
}

func entryPaths(list []*browseEntry) []string {
	out := make([]string, len(list))
	for i, e := range list {
		out[i] = e.pathString()
	}
	return out
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
