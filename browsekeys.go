// The keys the browser keeps for itself. Everything else falls through to the focus ring.

package main

import (
	"unicode"
	"unicode/utf8"

	"github.com/wow-look-at-my/tml"
	"github.com/wow-look-at-my/tml/input"
)

// key handles one key. A popup takes every key while it is open. Typing goes to the text that has focus.
func (m *browseModel) key(msg tml.Key) bool {
	k := msg.String()
	if k == "ctrl+c" {
		m.code = interruptExitCode
		m.host.Quit()
		return true
	}
	switch {
	case m.menu != nil:
		return m.menuKey(msg)
	case m.help:
		m.help = false
		return true
	case m.confirming:
		if k == "esc" {
			m.confirming = false
			m.setStatus("cancelled", "dim")
			return true
		}
		return false
	}
	focused, _ := m.view.UI().Focused()
	switch k {
	case "ctrl+r":
		m.start(false)
		return true
	case "pgup":
		m.moveRow(-m.paneRows())
		return true
	case "pgdown":
		m.moveRow(m.paneRows())
		return true
	}
	switch focused {
	case "filter":
		return m.filterKey(msg)
	case "rowfilter":
		return m.rowFilterKey(msg)
	case "result", "output":
		return m.resultKey(msg)
	case "tree", "":
		return m.treeKey(msg)
	}
	if k == "esc" {
		m.focus("tree")
		return true
	}
	return m.fieldKey(focused, msg)
}

// globalKey is a key with the same meaning wherever the focus is not on text.
func (m *browseModel) globalKey(k string) bool {
	switch k {
	case "?":
		m.help = true
	case "v":
		m.cycleView()
	case "backspace":
		return m.back()
	default:
		return false
	}
	return true
}

func (m *browseModel) treeKey(msg tml.Key) bool {
	k := msg.String()
	vis := m.visible()
	pos := 0
	for i, e := range vis {
		if e == m.cur {
			pos = i
		}
	}
	switch k {
	case "q":
		m.host.Quit()
	case "esc":
		if m.filter != "" {
			m.filter = ""
			return true
		}
		m.host.Quit()
	case "up", "k":
		m.pickVisible(pos - 1)
	case "down", "j":
		m.pickVisible(pos + 1)
	case "home", "g":
		m.pickVisible(0)
	case "end", "G":
		m.pickVisible(len(vis) - 1)
	case "/":
		m.filtering = true
		m.focus("filter")
	case "left", "h":
		switch {
		case m.cur.group() && !m.cur.collapsed:
			m.cur.collapsed = true
		case m.cur.parent != nil:
			m.pick(m.cur.parent)
		}
	case "right", "l", "enter":
		m.enter(k == "enter")
	case "r":
		m.start(false)
	case "tab":
		return false
	default:
		return m.globalKey(k)
	}
	return true
}

// enter opens a shut group. A command that takes no input runs. Any other command opens its form at the first empty
// required input. One key never runs a command whose inputs the user has not seen.
func (m *browseModel) enter(run bool) {
	e := m.cur
	switch {
	case e.group() && e.collapsed:
		e.collapsed = false
	case e.group() && run && !e.runnable():
		e.collapsed = true
	case !e.runnable():
	case len(e.fields) == 0:
		m.start(false)
	case len(e.missing()) > 0:
		m.focus(e.missing()[0].id)
	default:
		m.focus(e.fields[0].id)
	}
}

// filterKey edits the tree filter. The arrows still move the selection, so a search ends where the user wants it.
func (m *browseModel) filterKey(msg tml.Key) bool {
	switch msg.String() {
	case "esc":
		m.filter, m.filtering = "", false
		m.focus("tree")
	case "enter":
		m.filtering = false
		m.focus("tree")
		if vis := m.visible(); len(vis) > 0 && !containsEntry(vis, m.cur) {
			m.pick(vis[0])
		}
		m.enter(false)
	case "up", "down":
		m.treeKey(msg)
	case "tab":
		m.filtering = false
		return false
	default:
		if !editText(&m.filter, msg) {
			return false
		}
		// The selection follows the first match, so enter opens what the filter found.
		if vis := m.visible(); len(vis) > 0 && !containsEntry(vis, m.cur) {
			for _, e := range vis {
				if e.matches(m.filter) {
					m.pick(e)
					break
				}
			}
		}
	}
	return true
}

func containsEntry(list []*browseEntry, e *browseEntry) bool {
	for _, x := range list {
		if x == e {
			return true
		}
	}
	return false
}

func (m *browseModel) rowFilterKey(msg tml.Key) bool {
	switch msg.String() {
	case "esc":
		m.rowFilter, m.rowFiltering = "", false
		m.focusResult()
	case "enter":
		m.rowFiltering = false
		m.focusResult()
	case "up":
		m.moveRow(-1)
	case "down":
		m.moveRow(1)
	case "tab":
		m.rowFiltering = false
		return false
	default:
		if !editText(&m.rowFilter, msg) {
			return false
		}
		m.moveRow(0)
	}
	return true
}

func (m *browseModel) resultKey(msg tml.Key) bool {
	k := msg.String()
	f := m.top()
	switch k {
	case "up", "k":
		m.moveRow(-1)
	case "down", "j":
		m.moveRow(1)
	case "home", "g":
		m.moveRow(-1 << 20)
	case "end", "G":
		m.moveRow(1 << 20)
	case "enter":
		m.activate()
	case "right", "l", "i":
		m.drill()
	case "left", "h":
		m.back()
	case "a", "o":
		m.actions()
	case "space", " ":
		m.toggleMark()
	case "*":
		m.markAll()
	case "s":
		m.sortMenu()
	case "]":
		m.turnPage(1)
	case "[":
		m.turnPage(-1)
	case "r":
		if f != nil {
			m.cur = f.entry
			f.entry.restore(f.inputs)
			m.start(false)
		}
	case "/":
		if f != nil && f.view == viewData {
			m.rowFiltering = true
			m.focus("rowfilter")
		}
	case "esc":
		if f != nil && f.view == viewData && len(f.level().marked) > 0 {
			clear(f.level().marked)
			return true
		}
		if m.rowFilter != "" {
			m.rowFilter = ""
			return true
		}
		m.focus("tree")
	case "q":
		m.host.Quit()
	case "tab", "shift+tab":
		return false
	default:
		return m.globalKey(k)
	}
	return true
}

// fieldKey edits a form field. A choice field cycles with the arrows and opens its choices on enter.
// ctrl+o lists a field's suggestions.
func (m *browseModel) fieldKey(focused string, msg tml.Key) bool {
	field := m.field(focused)
	if field == nil {
		if msg.String() == "backspace" {
			return m.back()
		}
		return false
	}
	k := msg.String()
	if k == "ctrl+o" || (k == "down" && field.suggest() != nil && field.value == "") {
		m.suggest(field)
		return true
	}
	if field.isBool() {
		return false
	}
	if choices := field.choices(); len(choices) > 0 {
		switch k {
		case "left":
			field.cycle(-1)
		case "right", "space", " ":
			field.cycle(1)
		case "enter":
			m.pickChoice(field)
		case "backspace", "delete":
			field.value = ""
		case "tab", "shift+tab", "up", "down":
			return false
		}
		// Any other key is swallowed: a choice field takes no typing.
		return true
	}
	if k == "enter" {
		m.start(false)
		return true
	}
	return editText(&field.value, msg)
}

// editText applies a typing key to s: backspace, ctrl+u, or a printable character.
func editText(s *string, msg tml.Key) bool {
	switch msg.String() {
	case "backspace":
		if _, size := utf8.DecodeLastRuneInString(*s); size > 0 {
			*s = (*s)[:len(*s)-size]
		}
		return true
	case "ctrl+u":
		*s = ""
		return true
	}
	if r := msg.Code; msg.Text != "" && unicode.IsPrint(r) && msg.Mod&^input.Shift == 0 {
		*s += msg.Text
		return true
	}
	return false
}

func (m *browseModel) menuKey(msg tml.Key) bool {
	mn := m.menu
	switch msg.String() {
	case "esc":
		m.menu = nil
		m.focusAfterMenu()
	case "up":
		mn.move(-1)
	case "down":
		mn.move(1)
	case "pgup":
		mn.move(-m.menuRows())
	case "pgdown":
		mn.move(m.menuRows())
	case "enter":
		m.pickMenu()
	default:
		if editText(&mn.filter, msg) {
			mn.sel = 0
		}
	}
	return true
}

// focusAfterMenu puts the keyboard back where a closed menu leaves it.
func (m *browseModel) focusAfterMenu() {
	if m.top() != nil {
		m.focusResult()
		return
	}
	m.focus("tree")
}

// scroll moves whatever the wheel turned over.
func (m *browseModel) scroll(id string, delta int) {
	switch id {
	case "tree":
		vis := m.visible()
		for i, e := range vis {
			if e == m.cur {
				m.pickVisible(i + delta)
				return
			}
		}
	case "result", "output", "pane":
		m.moveRow(delta)
	case "menu":
		if m.menu != nil {
			m.menu.move(delta)
		}
	}
}
