// The browser's result history: one frame per run, with its data view, its text and its body.

package main

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/wow-look-at-my/api-cli/fields"
	"github.com/wow-look-at-my/tml"
)

// The views of a result.
const (
	viewData = iota
	viewText // what the command printed on a terminal
	viewRaw  // the body as it arrived, JSON indented
)

var viewNames = []string{"data", "text", "raw"}

// tableHeaderRows is the header and the rule above a borderless table's first row.
const tableHeaderRows = 2

// maxFrames caps the history, so a long session of links holds a bounded amount of output.
const maxFrames = 50

// resultFrame is one run's result and where the user is in it.
type resultFrame struct {
	entry   *browseEntry
	inputs  map[string]string
	carried map[string]string
	line    string
	code    int
	took    time.Duration

	text   []string
	raw    string
	data   any
	isJSON bool
	levels []*dataLevel
	view   int
	offset int
}

func (m *browseModel) top() *resultFrame {
	if len(m.frames) == 0 {
		return nil
	}
	return m.frames[len(m.frames)-1]
}

// finish turns a run's output into a frame and shows it.
func (m *browseModel) finish(msg browseRunMsg) {
	m.running = false
	run := m.launched
	m.launched = nil
	if run == nil || msg.id != m.runID {
		return
	}
	f := newFrame(run.entry, msg)
	if run.keep != nil {
		f.carry(run.keep)
	}
	if run.push {
		m.frames = append(m.frames, f)
		if len(m.frames) > maxFrames {
			m.frames = m.frames[len(m.frames)-maxFrames:]
		}
	} else {
		m.frames[len(m.frames)-1] = f
	}
	m.rowFilter, m.rowFiltering = "", false
	summary := fmt.Sprintf("exit %d · %s · %s", msg.out.code, roundTook(msg.took), msg.line)
	if l := f.level(); f.isJSON && l != nil && l.kind == levelTable {
		summary = fmt.Sprintf("exit %d · %s · %d rows · %s", msg.out.code, roundTook(msg.took), len(l.cells), msg.line)
	}
	if msg.out.code == 0 {
		m.setStatus(summary, "ok")
	} else {
		m.setStatus(summary, "bad")
	}
	if focused, _ := m.view.UI().Focused(); run.push || focused == "tree" || focused == "run" {
		m.focusResult()
	}
	m.schedule(f)
}

func roundTook(d time.Duration) string {
	if d < time.Second {
		return d.Round(time.Millisecond).String()
	}
	return d.Round(100 * time.Millisecond).String()
}

// newFrame reads a run's output. A body that parses as JSON gets the data view.
func newFrame(e *browseEntry, msg browseRunMsg) *resultFrame {
	f := &resultFrame{
		entry:   e,
		inputs:  e.snapshot(),
		carried: e.inputs(),
		line:    msg.line,
		code:    msg.out.code,
		took:    msg.took,
		text:    browseLines(msg.out.text),
		raw:     msg.out.raw,
		view:    viewText,
	}
	trimmed := strings.TrimSpace(f.raw)
	if (strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[")) && json.Valid([]byte(trimmed)) {
		f.isJSON = true
		f.data = parseInput(trimmed, "json")
		f.levels = rootLevels(f.data, e)
		// A <tml> screen is what its author wants seen first.
		if !e.node.TML.Defined() {
			f.view = viewData
		}
	}
	return f
}

// rootLevels is the levels a result opens at. An envelope opens at its records, with the envelope one step back.
func rootLevels(data any, e *browseEntry) []*dataLevel {
	ctx := map[string]any{"data": data}
	if p := rootPath(data, e); p != "" {
		if v, ok := fields.Lookup(data, p); ok {
			return []*dataLevel{newLevel(e.name(), data, nil, ctx), newLevel(p, v, declaredColumns(e), ctx)}
		}
	}
	return []*dataLevel{newLevel(e.name(), data, declaredColumns(e), ctx)}
}

// carry keeps where the user was in old, for a refresh of the same command.
func (f *resultFrame) carry(old *resultFrame) {
	f.view = old.view
	f.offset = old.offset
	for i, l := range f.levels {
		if i < len(old.levels) && old.levels[i].label == l.label {
			l.sel = min(old.levels[i].sel, len(l.cells)-1)
		}
	}
}

func (f *resultFrame) level() *dataLevel {
	if len(f.levels) == 0 {
		return nil
	}
	return f.levels[len(f.levels)-1]
}

// views is the views this result offers. Text that matches the body adds nothing, so it is left out.
func (f *resultFrame) views() []int {
	if !f.isJSON {
		return []int{viewText}
	}
	if strings.TrimSpace(strings.Join(f.text, "\n")) == strings.TrimSpace(f.raw) {
		return []int{viewData, viewRaw}
	}
	return []int{viewData, viewText, viewRaw}
}

// lines is what the text and raw views draw.
func (f *resultFrame) lines() []string {
	if f.view == viewRaw && f.isJSON {
		pretty, err := json.MarshalIndent(f.data, "", "  ")
		if err == nil {
			return browseLines(string(pretty))
		}
	}
	if f.view == viewRaw {
		return browseLines(f.raw)
	}
	return f.text
}

// cycleView moves to the next view this result offers.
func (m *browseModel) cycleView() {
	f := m.top()
	if f == nil {
		return
	}
	vs := f.views()
	next := vs[0]
	for i, v := range vs {
		if v == f.view {
			next = vs[(i+1)%len(vs)]
		}
	}
	f.view = next
	f.offset = 0
	m.focusResult()
}

// focusResult moves the keyboard to whatever the result pane draws.
func (m *browseModel) focusResult() {
	if f := m.top(); f != nil && f.view == viewData {
		m.focus("result")
		return
	}
	m.focus("output")
}

// schedule asks for the next run of a command that declares watch=.
func (m *browseModel) schedule(f *resultFrame) {
	if f.entry.watch == "" {
		return
	}
	// watch= passed validateWatch at load time, so the browser schedules it as written.
	interval, err := parseWatchLength("watch=", f.entry.watch)
	if err != nil || interval <= 0 {
		return
	}
	id := m.runID
	m.host.Go(func() tml.Msg {
		time.Sleep(interval)
		return browseTickMsg{id: id}
	})
}

// tick reruns the shown command, unless anything happened since the run that scheduled it.
func (m *browseModel) tick(msg browseTickMsg) {
	f := m.top()
	if f == nil || msg.id != m.runID || m.running || m.menu != nil || m.confirming {
		return
	}
	argv, err := f.entry.argvWith(f.inputs)
	if err != nil {
		return
	}
	m.launch(&pendingRun{entry: f.entry, argv: argv, keep: f})
}

// argvWith is the command line for the inputs a frame recorded, whatever the form holds now.
func (e *browseEntry) argvWith(inputs map[string]string) ([]string, error) {
	saved := e.snapshot()
	e.restore(inputs)
	defer e.restore(saved)
	return e.argv()
}

// visibleRows is the rows of the current level that pass the row filter.
func (m *browseModel) visibleRows(l *dataLevel) []int { return l.filtered(m.rowFilter) }

// moveRow moves the selection within the filtered rows.
func (m *browseModel) moveRow(delta int) {
	f := m.top()
	if f == nil || f.view != viewData {
		if f != nil {
			m.scrollText(delta)
		}
		return
	}
	l := f.level()
	rows := m.visibleRows(l)
	if len(rows) == 0 {
		return
	}
	pos := indexOf(rows, l.sel)
	if pos < 0 {
		pos = 0
	} else {
		pos = max(0, min(pos+delta, len(rows)-1))
	}
	l.sel = rows[pos]
}

func (m *browseModel) scrollText(delta int) {
	f := m.top()
	limit := max(0, len(f.lines())-m.paneRows())
	f.offset = max(0, min(f.offset+delta, limit))
}

func indexOf(rows []int, v int) int {
	for i, r := range rows {
		if r == v {
			return i
		}
	}
	return -1
}

// clickRow selects a row of the result table by its position on screen. A second click on the selected row opens it.
func (m *browseModel) clickRow(screenRow int) {
	f := m.top()
	if f == nil || f.view != viewData || screenRow < 0 {
		return
	}
	l := f.level()
	rows := m.visibleRows(l)
	start, _ := m.rowWindow(l)
	if i := start + screenRow; i < len(rows) {
		if l.sel == rows[i] {
			m.activate()
			return
		}
		l.sel = rows[i]
	}
}

// paneRows is how many rows the result pane shows: what the last frame gave it, or a third of the screen before the first frame.
func (m *browseModel) paneRows() int {
	if t, ok := m.view.UI().Target("pane"); ok && t.Rect.H > 0 {
		chrome := 4 // the border, the status line and the footer
		if m.rowFiltering || m.rowFilter != "" {
			chrome++
		}
		return max(1, t.Rect.H-chrome)
	}
	return max(3, m.height/3)
}

// rowWindow is the first filtered row on screen and the number shown, chosen so the selection stays in view.
func (m *browseModel) rowWindow(l *dataLevel) (start, n int) {
	rows := m.visibleRows(l)
	n = max(1, m.paneRows()-tableHeaderRows)
	pos := max(0, indexOf(rows, l.sel))
	start = max(0, min(pos-n/2, len(rows)-n))
	return start, n
}

// drill steps into the selected row.
func (m *browseModel) drill() bool {
	f := m.top()
	if f == nil || f.view != viewData {
		return false
	}
	child := f.level().child(f.entry)
	if child == nil {
		return false
	}
	if child.label == "" {
		child.label = fmt.Sprintf("[%d]", f.level().sel)
	}
	f.levels = append(f.levels, child)
	m.rowFilter, m.rowFiltering = "", false
	return true
}

// back steps out of the data, then back through the history. Going back to an earlier frame restores the form it ran from.
func (m *browseModel) back() bool {
	f := m.top()
	if f == nil {
		return false
	}
	m.rowFilter, m.rowFiltering = "", false
	if f.view == viewData && len(f.levels) > 1 {
		f.levels = f.levels[:len(f.levels)-1]
		return true
	}
	if len(m.frames) < 2 {
		return false
	}
	m.frames = m.frames[:len(m.frames)-1]
	prev := m.top()
	prev.entry.restore(prev.inputs)
	m.reveal(prev.entry)
	m.cur = prev.entry
	m.setStatus("back to "+prev.line, "dim")
	m.focusResult()
	return true
}

// links is what the current record can open.
func (m *browseModel) links() []browseLink {
	f := m.top()
	if f == nil || f.view != viewData {
		return nil
	}
	return m.linksFor(f.entry, f.level().record(), f.carried)
}

// activate is enter on a result row: step into a nested value, or follow the first link, or step into the record.
func (m *browseModel) activate() {
	f := m.top()
	if f == nil {
		return
	}
	if f.view != viewData {
		return
	}
	l := f.level()
	if l.kind == levelObject && m.drill() {
		return
	}
	if links := m.links(); len(links) > 0 {
		m.follow(links[0])
		return
	}
	m.drill()
}

// follow opens a link: it fills the target's form, selects it, and runs it on top of the history.
// A link that leaves a required input empty stops at the form, with that input focused.
func (m *browseModel) follow(l browseLink) {
	if m.running {
		m.setStatus("a run is still going", "dim")
		return
	}
	t := l.target
	for i := range t.fields {
		f := &t.fields[i]
		if v, ok := l.values[f.name()]; ok {
			f.set(v)
		}
	}
	m.reveal(t)
	m.cur = t
	if miss := t.missing(); len(miss) > 0 {
		m.setStatus("fill in "+miss[0].label+" to open "+t.pathString(), "bad")
		m.focus(miss[0].id)
		return
	}
	m.start(true)
}

// pageField is the input that pages a command's result: an int flag named page. A command without one does not page.
func (e *browseEntry) pageField() *browseField {
	for i := range e.fields {
		f := &e.fields[i]
		if f.flag != nil && f.flag.Type == "int" && f.flag.Name == "page" {
			return f
		}
	}
	return nil
}

// page is the page a frame shows: the value it ran with, or the flag's default.
func (f *resultFrame) page(pf *browseField) int {
	if n, err := strconv.Atoi(strings.TrimSpace(f.inputs[pf.id])); err == nil {
		return n
	}
	if n, ok := pf.flag.Default.(int); ok {
		return n
	}
	return 1
}

// turnPage reruns the command on screen one page on or back, in place.
func (m *browseModel) turnPage(delta int) {
	f := m.top()
	if f == nil || m.running {
		return
	}
	pf := f.entry.pageField()
	if pf == nil {
		m.setStatus(f.entry.pathString()+" declares no int flag named page", "dim")
		return
	}
	// A list that starts at the first page says so with its default.
	floor := 1
	if d, ok := pf.flag.Default.(int); ok && d == 0 {
		floor = 0
	}
	next := max(floor, f.page(pf)+delta)
	f.entry.restore(f.inputs)
	pf.value = strconv.Itoa(next)
	m.cur = f.entry
	m.reveal(f.entry)
	m.start(false)
}

// browseLines splits output into the rows the output pane draws. A tab has no
// width of its own in a cell grid, so it becomes spaces.
func browseLines(s string) []string {
	s = strings.ReplaceAll(strings.TrimRight(s, "\n"), "\r", "")
	if s == "" {
		return nil
	}
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = strings.ReplaceAll(l, "\t", "    ")
	}
	return lines
}
