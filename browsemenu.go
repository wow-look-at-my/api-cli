// The browser's popup menu: a choice field's choices, a field's suggestions, and the actions on a record.

package main

import (
	"fmt"
	"strings"

	"github.com/wow-look-at-my/api-cli/fields"
	"github.com/wow-look-at-my/tml"
)

// browseMenu is a list to pick one item from. Typing narrows it.
type browseMenu struct {
	title  string
	items  []string
	sel    int
	filter string
	choose func(item int)
}

// shown is the items that pass the filter, as indices into items.
func (mn *browseMenu) shown() []int {
	words := strings.Fields(strings.ToLower(mn.filter))
	var out []int
	for i, it := range mn.items {
		hay := strings.ToLower(it)
		ok := true
		for _, w := range words {
			ok = ok && strings.Contains(hay, w)
		}
		if ok {
			out = append(out, i)
		}
	}
	return out
}

func (mn *browseMenu) move(delta int) {
	n := len(mn.shown())
	if n == 0 {
		return
	}
	mn.sel = max(0, min(mn.sel+delta, n-1))
}

// pick chooses the selected item. The menu closes first, so a choice may open another.
func (m *browseModel) pickMenu() {
	mn := m.menu
	if mn == nil {
		return
	}
	shown := mn.shown()
	m.menu = nil
	if mn.sel < len(shown) {
		mn.choose(shown[mn.sel])
	}
}

func (mn *browseMenu) click(row int) {
	if row >= 0 && row < len(mn.shown()) {
		mn.sel = row
	}
}

// menuRows is how many items the menu shows at once.
func (m *browseModel) menuRows() int { return max(3, m.height-10) }

// menuStart is the first item on screen, chosen so the selection stays in view.
func (m *browseModel) menuStart() int {
	if m.menu == nil {
		return 0
	}
	rows, n := m.menuRows(), len(m.menu.shown())
	return max(0, min(m.menu.sel-rows/2, n-rows))
}

func (m *browseModel) openMenu(title string, items []string, choose func(int)) {
	m.menu = &browseMenu{title: title, items: items, choose: choose}
	m.view.UI().Focus("menu")
}

// pickChoice opens the choices of a choice field. The first item puts the field back on its default.
func (m *browseModel) pickChoice(f *browseField) {
	choices := f.choices()
	items := append([]string{"(default)"}, choices...)
	id := f.id
	m.openMenu(f.label, items, func(i int) {
		if field := m.field(id); field != nil {
			field.value = ""
			if i > 0 {
				field.value = choices[i-1]
			}
		}
		m.view.UI().Focus(id)
	})
}

// actions opens the menu of everything the current record can open, and stepping into it.
func (m *browseModel) actions() {
	links := m.links()
	var items []string
	for _, l := range links {
		label := l.label
		if len(l.missing) > 0 {
			label += "  (needs " + strings.Join(l.missing, ", ") + ")"
		}
		items = append(items, label)
	}
	f := m.top()
	canDrill := f != nil && f.view == viewData && f.level().child(f.entry) != nil
	if canDrill {
		items = append(items, "inspect this row")
	}
	if len(items) == 0 {
		m.setStatus("nothing to open from here", "dim")
		return
	}
	m.openMenu("Open", items, func(i int) {
		if i < len(links) {
			m.follow(links[i])
			return
		}
		m.drill()
		m.focusResult()
	})
}

// browseSuggestMsg carries a suggestion source's output back to the model.
type browseSuggestMsg struct {
	field  string
	source *browseEntry
	out    browseOutput
}

// suggest runs a field's suggest= source and offers what it lists. The source's inputs come from the form and the shared memory.
func (m *browseModel) suggest(f *browseField) {
	s := f.suggest()
	if s == nil || m.running {
		return
	}
	src := m.byPath[s.From]
	if src == nil {
		return
	}
	carried := m.cur.inputs()
	for i := range src.fields {
		sf := &src.fields[i]
		if v, ok := carried[sf.name()]; ok && sf.value == "" {
			sf.value = v
		}
	}
	m.prefill(src)
	argv, err := src.argv()
	if err != nil {
		m.setStatus(fmt.Sprintf("suggestions for %s come from %q: %v", f.label, s.From, err), "bad")
		return
	}
	m.running = true
	m.setStatus("listing "+f.label+" from "+s.From, "dim")
	exec, w, h, id := m.exec, m.outputWidth(), m.height, f.id
	m.host.Go(func() tml.Msg {
		return browseSuggestMsg{field: id, source: src, out: exec(argv, w, h)}
	})
}

// suggested opens the menu of values a suggestion source listed.
func (m *browseModel) suggested(msg browseSuggestMsg) {
	m.running = false
	f := m.field(msg.field)
	if f == nil {
		return
	}
	if msg.out.code != 0 {
		m.setStatus(fmt.Sprintf("%s failed with exit %d", msg.source.pathString(), msg.out.code), "bad")
		return
	}
	values, labels := suggestions(msg.out.raw, msg.source, f)
	if len(values) == 0 {
		m.setStatus(msg.source.pathString()+" listed nothing to pick", "dim")
		return
	}
	m.setStatus("", "dim")
	id := f.id
	m.openMenu(f.label, labels, func(i int) {
		if field := m.field(id); field != nil {
			field.value = values[i]
		}
		m.view.UI().Focus(id)
	})
}

// suggestions reads a source's records as values for f. suggest-field= names the value. Without it, the field that
// answers to the input's name wins, then a name or an id.
func suggestions(raw string, src *browseEntry, f *browseField) (values, labels []string) {
	frame := newFrame(src, browseRunMsg{out: browseOutput{raw: raw}})
	l := frame.level()
	if !frame.isJSON || l == nil {
		for _, line := range browseLines(raw) {
			if line = strings.TrimSpace(line); line != "" {
				values, labels = append(values, line), append(labels, line)
			}
		}
		return values, labels
	}
	path := f.suggest().Field
	for _, item := range l.items {
		rec, isRec := item.(map[string]any)
		var v string
		switch {
		case !isRec:
			v = fields.DisplayValue(item)
		case path != "":
			if x, ok := fields.Lookup(rec, path); ok {
				v = fields.DisplayValue(x)
			}
		default:
			v = guessValue(rec, f.name())
		}
		if v == "" {
			continue
		}
		label := v
		if title := recordTitle(item, src); isRec && title != "" && title != v {
			label = v + "  " + title
		}
		values, labels = append(values, v), append(labels, label)
	}
	return values, labels
}

func guessValue(rec map[string]any, name string) string {
	if v, ok := recordValue(rec, name); ok {
		return v
	}
	for _, k := range []string{"name", "id", "key", "slug"} {
		if v, ok := recordValue(rec, k); ok {
			return v
		}
	}
	return ""
}
