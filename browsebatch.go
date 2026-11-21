// Sorting the result rows, marking several of them, and running a link once per marked row.

package main

import (
	"cmp"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/wow-look-at-my/api-cli/fields"
	"github.com/wow-look-at-my/tml"
)

// sorted orders rows by the level's sort column. Numbers compare as numbers, anything else as text without case.
func (l *dataLevel) sorted(rows []int) []int {
	if l.sortCol < 0 || l.sortCol >= len(l.columns) {
		return rows
	}
	out := slices.Clone(rows)
	slices.SortStableFunc(out, func(a, b int) int {
		c := compareCells(l.cells[a][l.sortCol], l.cells[b][l.sortCol])
		if l.sortDesc {
			return -c
		}
		return c
	})
	return out
}

func compareCells(a, b string) int {
	fa, errA := strconv.ParseFloat(a, 64)
	fb, errB := strconv.ParseFloat(b, 64)
	if errA == nil && errB == nil {
		return cmp.Compare(fa, fb)
	}
	return cmp.Compare(strings.ToLower(a), strings.ToLower(b))
}

// header is the column titles, with an arrow on the sort column.
func (l *dataLevel) header() []string {
	out := slices.Clone(l.columns)
	if l.sortCol >= 0 && l.sortCol < len(out) {
		arrow := " ▲"
		if l.sortDesc {
			arrow = " ▼"
		}
		out[l.sortCol] += arrow
	}
	return out
}

// sortMenu offers the columns to sort by. The column already in use flips its direction, and the first item puts
// the rows back in the order the result gave.
func (m *browseModel) sortMenu() {
	f := m.top()
	if f == nil || f.view != viewData {
		return
	}
	l := f.level()
	items := append([]string{"(result order)"}, l.columns...)
	m.openMenu("Sort by", items, func(i int) {
		switch {
		case i == 0:
			l.sortCol, l.sortDesc = -1, false
		case l.sortCol == i-1:
			l.sortDesc = !l.sortDesc
		default:
			l.sortCol, l.sortDesc = i-1, false
		}
		m.focusResult()
	})
}

// toggleMark marks or unmarks the selected row, then moves on, so a run of space presses marks a run of rows.
func (m *browseModel) toggleMark() {
	f := m.top()
	if f == nil || f.view != viewData || f.level().kind != levelTable {
		return
	}
	l := f.level()
	if l.marked[l.sel] {
		delete(l.marked, l.sel)
	} else {
		l.marked[l.sel] = true
	}
	m.moveRow(1)
}

// markAll marks every row the filter shows, or clears the marks when they are all marked already.
func (m *browseModel) markAll() {
	f := m.top()
	if f == nil || f.view != viewData || f.level().kind != levelTable {
		return
	}
	l := f.level()
	rows := m.visibleRows(l)
	all := !slices.ContainsFunc(rows, func(r int) bool { return !l.marked[r] })
	for _, r := range rows {
		if all {
			delete(l.marked, r)
		} else {
			l.marked[r] = true
		}
	}
}

// markedRows is the marked rows in the order the table shows them.
func (m *browseModel) markedRows(l *dataLevel) []int {
	var out []int
	for _, r := range m.visibleRows(l) {
		if l.marked[r] {
			out = append(out, r)
		}
	}
	return out
}

// batchItem is one run of a batch: the row it came from and the command line.
type batchItem struct {
	title string
	argv  []string
}

// followMarked runs a link once per marked row, after one confirmation. A row that cannot fill a required input of
// the target is left out, and the popup says how many.
func (m *browseModel) followMarked(l browseLink) {
	f := m.top()
	lvl := f.level()
	var items []batchItem
	skipped := 0
	for _, r := range m.markedRows(lvl) {
		rec, _ := lvl.items[r].(map[string]any)
		var match *browseLink
		for _, cand := range m.linksFor(f.entry, rec, f.carried) {
			if cand.target == l.target && cand.label == l.label {
				match = &cand
				break
			}
		}
		if match == nil || len(match.missing) > 0 {
			skipped++
			continue
		}
		argv, err := l.target.argvFor(match.values)
		if err != nil {
			skipped++
			continue
		}
		items = append(items, batchItem{title: recordTitle(rec, f.entry), argv: argv})
	}
	if len(items) == 0 {
		m.setStatus("no marked row fills "+l.target.pathString(), "bad")
		return
	}
	text := fmt.Sprintf("Run %s for %s?", l.target.pathString(), plural(len(items), "row"))
	if skipped > 0 {
		text += fmt.Sprintf(" %s cannot fill its inputs and will be left out.", plural(skipped, "row"))
	}
	m.pending = &pendingRun{entry: l.target, batch: items, push: true, prompt: text}
	m.confirming = true
	m.focus("confirm-no")
}

// argvFor is the command line with the named inputs set, whatever the form holds for them now.
func (e *browseEntry) argvFor(values map[string]string) ([]string, error) {
	saved := e.snapshot()
	defer e.restore(saved)
	for i := range e.fields {
		if v, ok := values[e.fields[i].name()]; ok {
			e.fields[i].set(v)
		}
	}
	return e.argv()
}

// browseBatchMsg carries every run of a batch back to the model.
type browseBatchMsg struct {
	id    int
	items []batchItem
	outs  []browseOutput
	took  time.Duration
}

// launchBatch runs a batch off the program's goroutine, one command after another.
func (m *browseModel) launchBatch(run *pendingRun) {
	m.running = true
	m.begin()
	m.runID++
	m.launched = run
	m.setStatus(fmt.Sprintf("running %s for %s", run.entry.pathString(), plural(len(run.batch), "row")), "dim")
	exec, width, height, id, items, now, ctx := m.exec, m.outputWidth(), m.height, m.runID, run.batch, m.now, m.runContext()
	began := now()
	m.host.Go(func() tml.Msg {
		outs := make([]browseOutput, len(items))
		for i, it := range items {
			// A cancel stops the batch before its next run, and the rows it never reached say so.
			if ctx.Err() != nil {
				outs[i] = browseOutput{code: -1, text: "not run: cancelled"}
				continue
			}
			outs[i] = exec(it.argv, width, height)
		}
		return browseBatchMsg{id: id, items: items, outs: outs, took: now().Sub(began)}
	})
}

// finishBatch shows a batch as one table: a row per run, with its exit code and the first line it printed.
// A run's own result is under the row, so enter on the row opens it.
func (m *browseModel) finishBatch(msg browseBatchMsg) {
	m.running = false
	m.end()
	run := m.launched
	m.launched = nil
	if run == nil || msg.id != m.runID {
		return
	}
	var rows []any
	failed := 0
	for i, it := range msg.items {
		out := msg.outs[i]
		if out.code != 0 {
			failed++
		}
		row := map[string]any{"row": it.title, "exit": int64(out.code), "output": firstLine(out.text)}
		if trimmed := strings.TrimSpace(out.raw); json.Valid([]byte(trimmed)) && trimmed != "" {
			row["result"] = parseInput(trimmed, "json")
		}
		rows = append(rows, row)
	}
	raw, _ := json.Marshal(rows)
	line := fmt.Sprintf("%s × %d", run.entry.pathString(), len(msg.items))
	f := newFrame(run.entry, browseRunMsg{line: line, out: browseOutput{text: string(raw), raw: string(raw)}, took: msg.took})
	lvl := newLevel(line, f.data, []fields.Field{{Name: "row", Path: "row"}, {Name: "exit", Path: "exit"}, {Name: "output", Path: "output"}}, nil)
	for i, r := range rows {
		if result, ok := r.(map[string]any)["result"]; ok {
			lvl.items[i] = result
		}
	}
	f.levels = []*dataLevel{lvl}
	f.batch = true
	m.frames = append(m.frames, f)
	m.rowFilter, m.rowFiltering = "", false
	summary := fmt.Sprintf("ran %s for %s · %d failed · %s", run.entry.pathString(), plural(len(msg.items), "row"), failed, roundTook(msg.took))
	style := "ok"
	if failed > 0 {
		style = "bad"
	}
	m.setStatus(summary, style)
	m.focusResult()
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexAny(s, "\r\n"); i >= 0 {
		return s[:i]
	}
	return s
}
