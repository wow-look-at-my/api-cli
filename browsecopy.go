// Copying a value out of the browser, through the terminal's clipboard.

package main

import (
	"encoding/json"
	"strings"
)

// copyMenu offers what the result on screen can put on the clipboard: each cell of the selected row and the row
// as JSON. This also covers the command line that made the result, and the whole result.
func (m *browseModel) copyMenu() {
	f := m.top()
	if f == nil {
		m.setStatus("nothing to copy yet", "dim")
		return
	}
	var labels, values []string
	add := func(label, value string) {
		labels = append(labels, label)
		values = append(values, value)
	}
	if f.view == viewData {
		l := f.level()
		if l.sel >= 0 && l.sel < len(l.cells) {
			for i, col := range l.columns {
				add(col+": "+l.cells[l.sel][i], l.cells[l.sel][i])
			}
			if b, err := json.MarshalIndent(l.items[l.sel], "", "  "); err == nil {
				add("this row as JSON", string(b))
			}
		}
	}
	add("the command line: "+f.line, f.line)
	add("the whole result", strings.Join(f.lines(), "\n"))
	if f.view == viewData && f.hasData {
		if b, err := json.MarshalIndent(f.data, "", "  "); err == nil {
			values[len(values)-1] = string(b)
		}
	}
	m.openMenu("Copy", labels, func(i int) {
		m.host.Copy(values[i])
		label := labels[i]
		if len(label) > 40 {
			label = label[:40] + "…"
		}
		m.setStatus("copied "+label+" (to the clipboard, where the terminal takes OSC 52)", "ok")
		m.focusResult()
	})
}
