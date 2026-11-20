// What the browser draws: the props its component reads, the tree window, the hints and the footer.

package main

import (
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/wow-look-at-my/tml"
	"github.com/wow-look-at-my/tml/sema"
)

// browseHelp is the key reference the? popup shows.
var browseHelp = []string{
	"tree      ↑↓ pick · enter form · r run · ← close group · / filter · q quit",
	"form      type · tab next · enter run · ←→ cycle a choice · ctrl+o suggestions",
	"result    ↑↓ row · enter open · a actions · i inspect · ⌫ back · / filter · [ ] page",
	"anywhere  v view · ctrl+r run · pgup/pgdn scroll · esc back · ctrl+c quit",
}

// treeRows is how many tree rows fit in the top half of the screen.
func (m *browseModel) treeRows() int {
	return max(3, min(len(m.visible()), m.height/2-2))
}

// treeStart is the first tree row on screen, chosen so the selection stays visible.
func (m *browseModel) treeStart() int {
	vis := m.visible()
	rows := m.treeRows()
	pos := max(0, indexOfEntry(vis, m.cur))
	return max(0, min(pos-rows/2, len(vis)-rows))
}

func indexOfEntry(list []*browseEntry, e *browseEntry) int {
	for i, x := range list {
		if x == e {
			return i
		}
	}
	return -1
}

func (m *browseModel) hint() string {
	focused, _ := m.view.UI().Focused()
	f := m.field(focused)
	switch {
	case m.menu != nil:
		return "type to narrow · up/down · enter picks · esc closes"
	case m.confirming:
		return "tab picks · enter chooses · esc cancels"
	case focused == "filter":
		return "type to filter · up/down pick · enter opens · esc clears"
	case focused == "rowfilter":
		return "type to filter rows · enter keeps it · esc clears"
	case focused == "result" || focused == "output":
		return "enter open · a actions · ⌫ back · v view · ? help"
	case focused == "tree" || focused == "":
		return "enter form · r run · / filter · ? help · q quit"
	case f != nil && len(f.choices()) > 0:
		return "←/→ cycle · enter lists choices · tab next · esc tree"
	case f != nil && f.suggest() != nil:
		return "type to edit · ctrl+o suggestions · enter runs · esc tree"
	default:
		return "type to edit · tab next · enter runs · esc tree"
	}
}

// footer says what the keys do to the result on screen.
func (m *browseModel) footer(links []browseLink) string {
	f := m.top()
	if f == nil {
		return ""
	}
	var parts []string
	if f.view == viewData {
		l := f.level()
		switch {
		case l.kind == levelObject && l.child(f.entry) != nil:
			parts = append(parts, "enter inspect "+l.keys[l.sel])
		case len(links) > 0:
			parts = append(parts, "enter open "+links[0].label)
		case l.child(f.entry) != nil:
			parts = append(parts, "enter inspect")
		}
		if len(links) > 0 {
			parts = append(parts, fmt.Sprintf("a %d actions", len(links)))
		}
	}
	if len(f.levels) > 1 && f.view == viewData || len(m.frames) > 1 {
		parts = append(parts, "⌫ back")
	}
	if len(f.views()) > 1 {
		parts = append(parts, "v view")
	}
	if pf := f.entry.pageField(); pf != nil {
		parts = append(parts, fmt.Sprintf("[ ] page %d", f.page(pf)))
	}
	if f.entry.watch != "" {
		parts = append(parts, "refreshes every "+f.entry.watch)
	}
	return strings.Join(parts, " · ")
}

// paneTitle is the breadcrumb of the result on screen: the command, then each step into its data.
func (m *browseModel) paneTitle() string {
	f := m.top()
	if f == nil {
		return "Result"
	}
	crumbs := []string{f.entry.pathString()}
	if f.view == viewData {
		for _, l := range f.levels[1:] {
			crumbs = append(crumbs, l.label)
		}
	}
	title := strings.Join(crumbs, " › ")
	if n := len(m.frames) - 1; n > 0 {
		title = m.frames[n-1].entry.name() + " ‹ " + title
	}
	return title
}

// viewTabs names the views on offer and marks the one on screen.
func viewTabs(f *resultFrame) string {
	vs := f.views()
	if len(vs) < 2 {
		return ""
	}
	parts := make([]string, len(vs))
	for i, v := range vs {
		parts[i] = viewNames[v]
		if v == f.view {
			parts[i] = "[" + parts[i] + "]"
		}
	}
	return strings.Join(parts, " ")
}

func (m *browseModel) fieldProps(e *browseEntry) []map[string]sema.Value {
	out := make([]map[string]sema.Value, 0, len(e.fields))
	for _, f := range e.fields {
		label := f.label
		if f.required {
			label += " *"
		}
		hint := f.hint
		choice := len(f.choices()) > 0
		if choice {
			hint = strings.TrimSpace(hint + " ← " + strings.Join(f.choices(), " | ") + " →")
		} else if f.suggest() != nil {
			hint = strings.TrimSpace(hint + " (ctrl+o lists " + f.suggest().From + ")")
		}
		out = append(out, map[string]sema.Value{
			"id":      sema.StringValue(f.id),
			"label":   sema.StringValue(label),
			"hint":    sema.StringValue(hint),
			"text":    sema.BoolValue(!f.isBool()),
			"value":   sema.StringValue(f.value),
			"cursor":  sema.StringValue(strconv.Itoa(utf8.RuneCountInString(f.value))),
			"checked": sema.BoolValue(f.checked),
		})
	}
	return out
}

// Props is what the program draws the screen from.
func (m *browseModel) Props() tml.Props {
	e := m.cur
	vis := m.visible()
	start, rows := m.treeStart(), m.treeRows()
	labels := make([]string, 0, rows)
	for _, t := range vis[start:min(start+rows, len(vis))] {
		labels = append(labels, t.label())
	}
	line := ""
	if e.runnable() {
		if argv, err := e.argv(); err == nil {
			line = commandLine(m.name, argv)
		} else {
			line = err.Error()
		}
	}
	style := m.statusStyle
	if style == "" {
		style = "dim"
	}
	description := e.node.Description
	if !e.runnable() && description == "" {
		description = "A group. Pick one of its commands on the left."
	}
	p := tml.Props{
		"title":        sema.StringValue(m.name),
		"hint":         sema.StringValue(m.hint()),
		"tree":         sema.ListValue(labels),
		"selected":     sema.StringValue(strconv.Itoa(indexOfEntry(vis, e) - start)),
		"filter":       sema.StringValue(m.filter),
		"filtering":    sema.BoolValue(m.filtering || m.filter != ""),
		"path":         sema.StringValue(e.pathString()),
		"description":  sema.StringValue(description),
		"runnable":     sema.BoolValue(e.runnable()),
		"fields":       sema.RecordListValue(m.fieldProps(e)),
		"line":         sema.StringValue(line),
		"running":      sema.BoolValue(m.running),
		"status":       sema.StringValue(m.status),
		"statusStyle":  sema.StringValue(style),
		"confirming":   sema.BoolValue(m.confirming),
		"confirmText":  sema.StringValue(m.confirmText()),
		"help":         sema.BoolValue(m.help),
		"helpLines":    sema.ListValue(browseHelp),
		"rowFilter":    sema.StringValue(m.rowFilter),
		"rowFiltering": sema.BoolValue(m.rowFiltering || m.rowFilter != ""),
	}
	m.paneProps(p)
	m.menuProps(p)
	return p
}

func (m *browseModel) confirmText() string {
	if m.pending == nil {
		return ""
	}
	prompt := strings.TrimSpace(m.pending.entry.confirm)
	if prompt == "" || strings.Contains(prompt, "{{") {
		prompt = m.pending.entry.pathString() + " asks for confirmation before it runs."
	}
	return prompt
}

// paneProps fills the result pane: a table for the data view, lines for the others, a placeholder before the first run.
func (m *browseModel) paneProps(p tml.Props) {
	f := m.top()
	p["paneTitle"] = sema.StringValue(m.paneTitle())
	p["isTable"] = sema.BoolValue(false)
	p["isText"] = sema.BoolValue(false)
	p["views"] = sema.StringValue("")
	p["footer"] = sema.StringValue("")
	p["columns"] = sema.ListValue(nil)
	p["rows"] = sema.ListValue(nil)
	p["collapse"] = sema.ListValue(nil)
	p["rowSel"] = sema.StringValue("-1")
	p["output"] = sema.ListValue(nil)
	p["empty"] = sema.StringValue("")
	if f == nil {
		p["empty"] = sema.StringValue("Nothing run yet. Pick a command and press enter.")
		return
	}
	p["views"] = sema.StringValue(viewTabs(f))
	if f.view != viewData {
		lines := f.lines()
		if len(lines) == 0 {
			p["empty"] = sema.StringValue("(no output)")
		} else {
			n := m.paneRows()
			off := max(0, min(f.offset, len(lines)-1))
			p["isText"] = sema.BoolValue(true)
			p["output"] = sema.ListValue(lines[off:min(off+n, len(lines))])
		}
		p["footer"] = sema.StringValue(m.footer(nil))
		return
	}
	l := f.level()
	links := m.links()
	p["footer"] = sema.StringValue(m.footer(links))
	rows := m.visibleRows(l)
	if len(rows) == 0 {
		p["empty"] = sema.StringValue("(no rows)")
		if m.rowFilter != "" {
			p["empty"] = sema.StringValue("(no rows match " + strconv.Quote(m.rowFilter) + ")")
		}
		return
	}
	start, n := m.rowWindow(l)
	shown := make([]string, 0, n)
	for _, r := range rows[start:min(start+n, len(rows))] {
		shown = append(shown, strings.Join(l.cells[r], "|"))
	}
	p["isTable"] = sema.BoolValue(true)
	p["columns"] = sema.ListValue(l.columns)
	p["rows"] = sema.ListValue(shown)
	p["collapse"] = sema.ListValue(l.collapse)
	p["rowSel"] = sema.StringValue(strconv.Itoa(indexOf(rows, l.sel) - start))
}

func (m *browseModel) menuProps(p tml.Props) {
	mn := m.menu
	p["menu"] = sema.BoolValue(mn != nil)
	p["menuTitle"] = sema.StringValue("")
	p["menuItems"] = sema.ListValue(nil)
	p["menuSel"] = sema.StringValue("0")
	p["menuFilter"] = sema.StringValue("")
	if mn == nil {
		return
	}
	shown := mn.shown()
	start := m.menuStart()
	items := make([]string, 0, m.menuRows())
	for _, i := range shown[start:min(start+m.menuRows(), len(shown))] {
		items = append(items, mn.items[i])
	}
	filter := "type to narrow"
	if mn.filter != "" {
		filter = "› " + mn.filter
	}
	p["menuTitle"] = sema.StringValue(mn.title)
	p["menuItems"] = sema.ListValue(items)
	p["menuSel"] = sema.StringValue(strconv.Itoa(mn.sel - start))
	p["menuFilter"] = sema.StringValue(filter)
}
