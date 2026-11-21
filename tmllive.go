// A live <tml> screen: the watch program draws the author's component itself. The watch program keeps the scroll
// and selection state that the screen's scroll= and select= props name.

package main

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/wow-look-at-my/tml"
	"github.com/wow-look-at-my/tml/sema"
)

// tmlTap receives a screen's data before it is drawn. A live screen sets it, to draw the component itself.
var tmlTap func(parsed any, ctx map[string]any)

// tmlLive is the part of the watch program that draws the author's component.
type tmlLive struct {
	decl *TML
	view *tml.View
	// parsed and ctx are the last good run's data, which a change of state draws again without a new run.
	parsed any
	ctx    map[string]any
	state  map[string]int
	props  tml.Props
}

func newTMLLive(decl *TML) (*tmlLive, error) {
	view, err := loadTMLView(tmlEntry(decl.Src, configDir), decl.Dark)
	if err != nil {
		return nil, err
	}
	return &tmlLive{decl: decl, view: view, state: map[string]int{}}, nil
}

// interactive reports whether the screen names any state for the user to drive, which is what turns the mouse on.
func (t *TML) interactive() bool {
	for _, p := range t.Props {
		if p.Scroll != "" || p.Select != "" {
			return true
		}
	}
	return false
}

// redraw computes the props from the last good run and the current state.
func (l *tmlLive) redraw() error {
	props, err := tmlProps(l.decl, l.parsed, l.ctx, l.state)
	if err != nil {
		return err
	}
	l.props = props
	return nil
}

// bound is the state keys that name element id, as "scroll:" or "select:" plus the id.
func (l *tmlLive) bound(kind, id string) bool {
	for _, p := range l.decl.Props {
		if (kind == "scroll" && p.Scroll == id) || (kind == "select" && p.Select == id) {
			return true
		}
	}
	return false
}

// firstScroll is the id the page keys scroll when the pointer is not on anything: the first scroll= prop.
func (l *tmlLive) firstScroll() string {
	for _, p := range l.decl.Props {
		if p.Scroll != "" {
			return p.Scroll
		}
	}
	return ""
}

// rows is how many rows the list with this id holds, where a selection stops. The prop named like the id is that
// list. Without one, the longest list prop stands in for it.
func (l *tmlLive) rows(id string) int {
	most := 0
	for _, p := range l.decl.Props {
		path := p.Over
		if path == "" {
			path = p.From
		}
		if path == "" {
			continue
		}
		list, ok := tmlLookupList(path, l.parsed, l.ctx)
		if !ok {
			continue
		}
		if p.Name == id {
			return len(list)
		}
		if p.Over != "" {
			most = max(most, len(list))
		}
	}
	return most
}

func tmlLookupList(path string, parsed any, ctx map[string]any) ([]any, bool) {
	found, ok := tmlLookup(path, parsed, ctx)
	if !ok {
		return nil, false
	}
	list, ok := found.([]any)
	return list, ok
}

// move changes one piece of state, held at zero and below the bound where there is one.
func (l *tmlLive) move(key string, delta, limit int) {
	v := max(0, l.state[key]+delta)
	if limit > 0 {
		v = min(v, limit-1)
	}
	l.state[key] = v
}

// event applies what the focus ring made of an input. It reports whether the screen changed.
func (l *tmlLive) event(e tml.Event) (changed, refresh bool) {
	switch e.Kind {
	case tml.Scrolled:
		if l.bound("scroll", e.ID) {
			l.move("scroll:"+e.ID, e.Delta, l.scrollLimit(e.ID))
			return true, false
		}
		if l.bound("select", e.ID) {
			l.move("select:"+e.ID, e.Delta, l.rows(e.ID))
			return true, false
		}
	case tml.Activated:
		if e.Action == "refresh" {
			return false, true
		}
		if l.bound("select", e.ID) && e.Y >= 0 {
			l.state["select:"+e.ID] = e.Y
			return true, false
		}
	}
	return false, false
}

// scrollLimit is how far an element can scroll, from where the last frame put it. Zero means no bound is known.
func (l *tmlLive) scrollLimit(id string) int {
	if t, ok := l.view.UI().Target(id); ok && t.Scroll.MaxY > 0 {
		return t.Scroll.MaxY + 1
	}
	return 0
}

// key handles the keys that move state: the arrows on a focused list, the page keys on the first scroll region.
func (l *tmlLive) key(k string, height int) bool {
	focused, _ := l.view.UI().Focused()
	if l.bound("select", focused) {
		switch k {
		case "up", "k":
			l.move("select:"+focused, -1, l.rows(focused))
			return true
		case "down", "j":
			l.move("select:"+focused, 1, l.rows(focused))
			return true
		}
	}
	id := l.firstScroll()
	if id == "" {
		return false
	}
	page := max(1, height/2)
	switch k {
	case "pgup":
		l.move("scroll:"+id, -page, l.scrollLimit(id))
	case "pgdown", "space":
		l.move("scroll:"+id, page, l.scrollLimit(id))
	case "home":
		l.state["scroll:"+id] = 0
	default:
		return false
	}
	return true
}

func tmlStateValue(p TMLProp, state map[string]int) (sema.Value, bool) {
	switch {
	case p.Scroll != "":
		return sema.StringValue(strconv.Itoa(state["scroll:"+p.Scroll])), true
	case p.Select != "":
		return sema.StringValue(strconv.Itoa(state["select:"+p.Select])), true
	}
	return sema.Value{}, false
}

// failure is the text a live screen shows when a run gave it nothing to draw.
func failure(out string, err error) string {
	text := strings.TrimSpace(out)
	if err != nil {
		text = strings.TrimSpace(text + "\nerror: " + err.Error())
	}
	if text == "" {
		text = "the run printed nothing to draw"
	}
	return fmt.Sprintf("%s\n\nr runs again · q quits", text)
}
