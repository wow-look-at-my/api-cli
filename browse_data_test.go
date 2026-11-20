package main

import _ "embed"

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wow-look-at-my/tml"
)

//go:embed testdata/browse-data-config.xml
var browseDataConfig string

// newDataHarness runs every command for real, so the data view reads what the command printed.
func newDataHarness(t *testing.T) *browseHarness {
	t.Helper()
	t.Serial()
	cfg, err := parseConfigXML([]byte(browseDataConfig))
	require.NoError(t, err)
	require.NoError(t, validate(cfg))
	m, err := newBrowseModel(cfg)
	require.NoError(t, err)
	h := &browseHarness{t: t, m: m}
	m.host = h
	m.exec = func(argv []string, w, ht int) browseOutput {
		h.runs = append(h.runs, argv)
		return browseExec(cfg, argv, w, ht)
	}
	m.update(h, tml.Resize{Width: 120, Height: 40})
	return h
}

// listAcme runs repos list for acme from the form.
func listAcme(h *browseHarness) {
	h.m.pick(browseEntryAt(h.t, h.m.entries, "repos list"))
	h.press("right")
	h.typeText("acme")
	h.press("enter")
	require.NotNil(h.t, h.m.top(), "status %q, focus %q, runs %v", h.m.status, h.focused(), h.runs)
}

func TestBrowseTreeHonoursHints(t *testing.T) {
	h := newDataHarness(t)
	assert.NotContains(t, entryPaths(h.m.entries), "internal", "a hidden command stays out of the tree")
	ping := browseEntryAt(t, h.m.entries, "ping")
	assert.Equal(t, "Ping the server", ping.label())
}

func TestBrowseDataViewOpensAtTheRecords(t *testing.T) {
	h := newDataHarness(t)
	listAcme(h)

	f := h.m.top()
	require.NotNil(t, f)
	assert.True(t, f.isJSON)
	assert.Equal(t, viewData, f.view)
	require.Len(t, f.levels, 2, "the envelope is one step back, and the records show")
	l := f.level()
	assert.Equal(t, "items", l.label)
	assert.Equal(t, levelTable, l.kind)
	assert.Equal(t, []string{"NAME", "STARS", "OWNER.LOGIN"}, l.columns, "identity first, a URL left out, a nested owner named by its login")
	assert.Equal(t, [][]string{{"api", "3", "acme"}, {"web", "9", "acme"}}, l.cells)
	assert.Equal(t, "result", h.focused())
	assert.Contains(t, h.m.status, "2 rows")

	frame := h.m.frame()
	assert.Contains(t, frame, "repos list › items")
	assert.Contains(t, frame, "enter open Open repo")
	assert.Contains(t, frame, "[data]")

	h.press("down")
	assert.Equal(t, 1, l.sel)
	h.press("v")
	assert.Equal(t, viewRaw, f.view, "a shell body prints as it is, so the views are data and raw")
	assert.Contains(t, strings.Join(f.lines(), "\n"), `"total": 2`, "raw indents the JSON")
	h.press("v")
	assert.Equal(t, viewData, f.view)

	h.press("left")
	assert.Len(t, f.levels, 1, "left steps back to the envelope")
	assert.Equal(t, levelObject, f.level().kind)
	assert.Equal(t, []string{"total", "items"}, f.level().keys, "scalars come before containers")
}

func TestBrowseFollowsALinkAndComesBack(t *testing.T) {
	h := newDataHarness(t)
	listAcme(h)

	links := h.m.links()
	require.NotEmpty(t, links)
	assert.Equal(t, "Open repo", links[0].label)
	assert.Equal(t, map[string]string{"owner": "acme", "repo": "api"}, links[0].values, "owner comes from owner.login, repo from the declared field")
	assert.Equal(t, "issues", links[1].target.pathString(), "an inferred link: issues takes an owner, and the record has one")
	assert.Equal(t, map[string]string{"owner": "acme"}, links[1].values)

	h.press("enter")
	require.Len(t, h.m.frames, 2, "a link pushes its result")
	assert.Equal(t, []string{"repos", "view", "acme", "api"}, h.runs[len(h.runs)-1])
	view := h.m.top()
	assert.Equal(t, "repos view", view.entry.pathString())
	assert.Equal(t, levelObject, view.level().kind)
	assert.Equal(t, []string{"name", "readme", "owner", "topics"}, view.level().keys)
	assert.Contains(t, h.m.frame(), "list ‹ repos view")

	// readme holds a couple of lines, so it opens; topics is a list.
	h.press("down")
	h.press("enter")
	require.Len(t, view.levels, 2)
	assert.Equal(t, []string{"one", "two"}, []string{view.level().cells[0][0], view.level().cells[1][0]})
	h.press("backspace")
	h.press("down", "down")
	h.press("i")
	assert.Equal(t, levelList, view.level().kind)
	assert.Equal(t, "topics", view.level().label)

	h.press("backspace", "backspace")
	require.Len(t, h.m.frames, 1, "back pops the link")
	assert.Equal(t, "repos list", h.m.cur.pathString(), "the tree follows the history")
	assert.Equal(t, "acme", h.m.cur.fields[0].value, "the form comes back as it ran")
}

func TestBrowseActionsMenu(t *testing.T) {
	h := newDataHarness(t)
	listAcme(h)

	h.press("a")
	require.NotNil(t, h.m.menu)
	assert.Equal(t, []string{"Open repo", "issues", "inspect this row"}, h.m.menu.items)
	h.typeText("iss")
	assert.Equal(t, []int{1}, h.m.menu.shown())
	h.press("enter")
	assert.Nil(t, h.m.menu)
	assert.Equal(t, []string{"issues", "acme"}, h.runs[len(h.runs)-1])
	l := h.m.top().level()
	assert.Equal(t, []string{"NUMBER", "TITLE"}, l.columns, "a <fields> declaration names the columns")
	assert.Equal(t, [][]string{{"1", "broken for acme"}}, l.cells)
	assert.Equal(t, viewData, h.m.top().view)
	assert.Contains(t, h.m.top().views(), viewText, "<fields> prints differently from the body, so text is a view of its own")
}

func TestBrowseRowFilter(t *testing.T) {
	h := newDataHarness(t)
	listAcme(h)
	h.press("/")
	assert.Equal(t, "rowfilter", h.focused())
	h.typeText("we")
	l := h.m.top().level()
	assert.Equal(t, []int{1}, h.m.visibleRows(l))
	assert.Equal(t, 1, l.sel, "the selection moves onto a row that passes")
	h.press("enter")
	assert.Equal(t, "result", h.focused())
	assert.Equal(t, "we", h.m.rowFilter, "enter keeps the filter")
	h.press("esc")
	assert.Empty(t, h.m.rowFilter)
}

func TestBrowseChoiceField(t *testing.T) {
	h := newDataHarness(t)
	h.m.pick(browseEntryAt(t, h.m.entries, "repos list"))
	h.m.focus("flag-state")
	f := h.m.field("flag-state")
	h.press("right")
	assert.Equal(t, "open", f.value)
	h.press("right", "right")
	assert.Equal(t, "all", f.value)
	h.press("right")
	assert.Equal(t, "", f.value, "the cycle passes through the default")
	h.press("left")
	assert.Equal(t, "all", f.value)
	h.typeText("x")
	assert.Equal(t, "all", f.value, "a choice field takes no typing")

	h.press("enter")
	require.NotNil(t, h.m.menu)
	assert.Equal(t, []string{"(default)", "open", "closed", "all"}, h.m.menu.items)
	h.typeText("clo")
	h.press("enter")
	assert.Equal(t, "closed", f.value)
	assert.Equal(t, "flag-state", h.focused())
}

func TestBrowseSuggestions(t *testing.T) {
	h := newDataHarness(t)
	h.m.pick(browseEntryAt(t, h.m.entries, "repos view"))
	h.press("right")
	h.typeText("acme")
	h.press("tab")
	assert.Equal(t, "arg-repo", h.focused())
	h.press("ctrl+o")
	assert.Equal(t, []string{"repos", "list", "acme"}, h.runs[len(h.runs)-1], "the source takes owner from the form")
	require.NotNil(t, h.m.menu)
	assert.Equal(t, []string{"api", "web"}, h.m.menu.items)
	h.press("down", "enter")
	assert.Equal(t, "web", h.m.field("arg-repo").value)
}

func TestBrowseMemoryAndAutorun(t *testing.T) {
	h := newDataHarness(t)
	h.m.pick(browseEntryAt(t, h.m.entries, "issues"))
	h.press("right")
	h.typeText("zed")
	h.press("enter")
	assert.Equal(t, []string{"issues", "zed"}, h.runs[len(h.runs)-1])

	runs := len(h.runs)
	h.m.focus("tree")
	h.m.pick(browseEntryAt(t, h.m.entries, "repos list"))
	assert.Equal(t, "zed", h.m.cur.fields[0].value, "owner was typed elsewhere, so it is filled in here")
	require.Len(t, h.runs, runs+1, "an autorun command with every input filled runs when it is picked")
	assert.Equal(t, []string{"repos", "list", "zed"}, h.runs[runs])
}

func TestBrowseClickOnARow(t *testing.T) {
	h := newDataHarness(t)
	listAcme(h)
	l := h.m.top().level()
	h.m.update(h, tml.Event{Kind: tml.Activated, ID: "result", Y: tableHeaderRows + 1})
	assert.Equal(t, 1, l.sel)
	h.m.update(h, tml.Event{Kind: tml.Activated, ID: "result", Y: tableHeaderRows + 1})
	require.Len(t, h.m.frames, 2, "a second click on the selected row opens it")
	assert.Equal(t, []string{"repos", "view", "acme", "web"}, h.runs[len(h.runs)-1])
}

func TestBrowseHelpPopup(t *testing.T) {
	h := newDataHarness(t)
	h.press("?")
	assert.True(t, h.m.help)
	assert.Contains(t, h.m.frame(), "ctrl+o suggestions")
	h.press("x")
	assert.False(t, h.m.help)
}

func TestBrowseWatchSchedulesARefresh(t *testing.T) {
	h := newBrowseHarness(t)
	e := browseEntryAt(t, h.m.entries, "fail")
	e.watch = "1ms"
	h.m.pick(e)
	h.deferred = true
	h.m.start(false)
	h.step() // the run
	require.Len(t, h.queue, 1, "a finished run of a watched command schedules the next")
	h.step() // the tick
	h.step() // the refresh
	assert.Equal(t, [][]string{{"fail"}, {"fail"}}, h.runs)
	assert.Len(t, h.m.frames, 1, "a refresh replaces the frame")

	h.m.runID++
	h.step()
	assert.Empty(t, h.queue, "a tick from an older run does nothing")
}
