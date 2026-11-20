// The browser's structured view of a JSON result: one level per step into it.

package main

import (
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/wow-look-at-my/api-cli/fields"
)

// The shapes a level draws in.
const (
	levelTable  = iota // a list of records, one column per field
	levelList          // a list of scalars
	levelObject        // one record, one row per key
	levelText          // a scalar, one row per line
)

// dataLevel is one step into a result: the value it shows, and what the user picked in it.
type dataLevel struct {
	label string
	value any
	kind  int
	sel   int

	columns []string
	cells   [][]string
	// items is what each row stands for: a record, an element, or the value under a key.
	items []any
	// keys is the key of each row of an object.
	keys []string
	// collapse is the order columns give way in when the table is too wide.
	collapse []string
}

// identityKeys name a record. The inferred columns start with them, and a breadcrumb names a record by the first one it holds.
var identityKeys = []string{"name", "title", "id", "key", "login", "full_name", "slug", "display_name", "number", "state", "status", "type"}

// maxColumns is the most columns the browser infers for a table.
const maxColumns = 8

// sampleSize is how many records column inference reads.
const sampleSize = 100

// newLevel shapes value for display. spec is the declared columns, or nil to infer them.
func newLevel(label string, value any, spec []fields.Field, ctx map[string]any) *dataLevel {
	l := &dataLevel{label: label, value: value}
	switch v := value.(type) {
	case []any:
		if slices.ContainsFunc(v, func(e any) bool { _, ok := e.(map[string]any); return ok }) {
			l.kind = levelTable
			l.table(v, spec, ctx)
		} else {
			l.kind = levelList
			l.columns = []string{"#", "VALUE"}
			for i, e := range v {
				l.cells = append(l.cells, []string{fmt.Sprint(i), summary(e)})
				l.items = append(l.items, e)
			}
		}
	case map[string]any:
		l.kind = levelObject
		l.columns = []string{"KEY", "VALUE"}
		for _, k := range objectKeys(v) {
			l.keys = append(l.keys, k)
			l.cells = append(l.cells, []string{k, summary(v[k])})
			l.items = append(l.items, v[k])
		}
	default:
		l.kind = levelText
		l.columns = []string{"VALUE"}
		for _, line := range strings.Split(fields.DisplayValue(value), "\n") {
			l.cells = append(l.cells, []string{line})
			l.items = append(l.items, line)
		}
	}
	return l
}

func (l *dataLevel) table(recs []any, spec []fields.Field, ctx map[string]any) {
	if len(spec) == 0 {
		spec = inferColumns(recs)
	}
	for _, f := range spec {
		l.columns = append(l.columns, strings.ToUpper(f.Name))
	}
	// The last column gives way first, because inference and an author both put the important ones first.
	for i := len(spec) - 1; i > 0; i-- {
		l.collapse = append(l.collapse, fmt.Sprint(i))
	}
	for _, rec := range recs {
		row := make([]string, len(spec))
		for i, f := range spec {
			row[i] = tableCell(f, rec, ctx)
		}
		l.cells = append(l.cells, row)
		l.items = append(l.items, rec)
	}
}

// tableCell is one field of one record. An expr that fails shows its error, because a blank cell would read as an empty value.
func tableCell(f fields.Field, rec any, ctx map[string]any) string {
	if f.Expr == "" {
		v, _ := fields.Lookup(rec, f.Path)
		s := summary(v)
		if s == "" && f.Default != "" {
			return f.Default
		}
		return s
	}
	s, err := fields.Cell(cliRenderer, f, rec, ctx)
	if err != nil {
		return "! " + err.Error()
	}
	return oneLineCell(s)
}

// summary is a value as one table cell: a scalar bare and on one line, a container as its size.
func summary(v any) string {
	switch x := v.(type) {
	case map[string]any:
		return "{" + plural(len(x), "key") + "}"
	case []any:
		return "[" + plural(len(x), "item") + "]"
	}
	return oneLineCell(fields.DisplayValue(v))
}

func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

// oneLineCell keeps the first line of s. The table separator becomes a broken bar, so a cell cannot split its row.
func oneLineCell(s string) string {
	if i := strings.IndexAny(s, "\r\n"); i >= 0 {
		s = s[:i] + " …"
	}
	return strings.ReplaceAll(strings.ReplaceAll(s, "\t", " "), "|", "¦")
}

// objectKeys orders a record's keys for its detail view: the identity keys first, then scalars, then containers, each alphabetical.
func objectKeys(m map[string]any) []string {
	keys := fields.SortedKeys(m)
	rank := func(k string) int {
		if i := slices.Index(identityKeys, k); i >= 0 {
			return i
		}
		switch m[k].(type) {
		case map[string]any, []any:
			return len(identityKeys) + 1
		}
		return len(identityKeys)
	}
	sort.SliceStable(keys, func(i, j int) bool { return rank(keys[i]) < rank(keys[j]) })
	return keys
}

// inferColumns picks the columns for a list of records nobody described: identity keys first, then the scalars most records hold.
// A key that holds a URL is left out, because an API that links everything would fill the table with them.
// A nested record that names itself, such as owner.login, counts as a scalar.
func inferColumns(recs []any) []fields.Field {
	type stat struct {
		seen, scalar, url int
		total             int
	}
	stats := map[string]*stat{}
	sample := recs[:min(len(recs), sampleSize)]
	objects := 0
	for _, r := range sample {
		m, ok := r.(map[string]any)
		if !ok {
			continue
		}
		objects++
		for k, v := range m {
			for path, val := range columnCandidates(k, v) {
				s := stats[path]
				if s == nil {
					s = &stat{}
					stats[path] = s
				}
				s.seen++
				if isScalar(val) {
					s.scalar++
					str := fields.DisplayValue(val)
					s.total += len(str)
					if strings.HasPrefix(str, "http://") || strings.HasPrefix(str, "https://") {
						s.url++
					}
				}
			}
		}
	}
	var keep []string
	for path, s := range stats {
		if s.scalar*2 < s.seen || s.url*2 > s.scalar || s.seen*4 < objects {
			continue
		}
		keep = append(keep, path)
	}
	score := func(p string) (int, int) {
		if i := slices.Index(identityKeys, p); i >= 0 {
			return 0, i
		}
		if strings.Contains(p, ".") {
			return 2, 0
		}
		// A short value reads well in a column; a long one is a description.
		return 1, stats[p].total / max(1, stats[p].scalar)
	}
	sort.Slice(keep, func(i, j int) bool {
		ai, aj := score(keep[i])
		bi, bj := score(keep[j])
		if ai != bi {
			return ai < bi
		}
		if aj != bj {
			return aj < bj
		}
		return keep[i] < keep[j]
	})
	if len(keep) > maxColumns {
		keep = keep[:maxColumns]
	}
	if len(keep) == 0 {
		return []fields.Field{{Name: "value", Path: ""}}
	}
	out := make([]fields.Field, len(keep))
	for i, p := range keep {
		out[i] = fields.Field{Name: p, Path: p}
	}
	return out
}

// columnCandidates is the columns one key of a record offers: the key itself, or the identity of a nested record.
func columnCandidates(k string, v any) map[string]any {
	if m, ok := v.(map[string]any); ok {
		for _, id := range []string{"login", "name", "id", "slug", "key"} {
			if sub, ok := m[id]; ok && isScalar(sub) {
				return map[string]any{k + "." + id: sub}
			}
		}
		return map[string]any{k: v}
	}
	return map[string]any{k: v}
}

func isScalar(v any) bool {
	switch v.(type) {
	case map[string]any, []any:
		return false
	}
	return true
}

// rootPath is where a result's records sit. The <tui rows=> hint wins, then a <fields over=> that is a plain path,
// then an envelope. This is an object with exactly one key that holds a list of records.
func rootPath(data any, e *browseEntry) string {
	if e.hints.Rows != "" {
		return e.hints.Rows
	}
	for _, b := range e.node.Fields {
		if over := b.Fields.Over; over != "" && !strings.Contains(over, "{{") {
			if _, ok := fields.Lookup(data, over); ok {
				return over
			}
			if trimmed, ok := strings.CutPrefix(over, "data."); ok {
				if _, found := fields.Lookup(data, trimmed); found {
					return trimmed
				}
			}
		}
	}
	m, ok := data.(map[string]any)
	if !ok {
		return ""
	}
	found := ""
	for k, v := range m {
		list, ok := v.([]any)
		if !ok || !slices.ContainsFunc(list, func(e any) bool { _, ok := e.(map[string]any); return ok }) {
			continue
		}
		if found != "" {
			return ""
		}
		found = k
	}
	return found
}

// declaredColumns is what the author said a record of e looks like: <tui columns=>, or the first <fields> block that lists fields.
func declaredColumns(e *browseEntry) []fields.Field {
	if len(e.hints.Columns) > 0 {
		out := make([]fields.Field, len(e.hints.Columns))
		for i, p := range e.hints.Columns {
			name := p
			if j := strings.LastIndex(p, "."); j >= 0 {
				name = p[j+1:]
			}
			out[i] = fields.Field{Name: name, Path: p}
		}
		return out
	}
	for _, b := range e.node.Fields {
		var out []fields.Field
		for _, f := range b.Fields.List {
			if f.Path == "@key" || f.Path == "@value" || !showsInTable(f.ShowIn) {
				continue
			}
			out = append(out, f)
		}
		if len(out) > 0 {
			return out
		}
	}
	return nil
}

// showsInTable reads a field's show_in= the way the fields package does, for the table representation.
func showsInTable(spec string) bool {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return true
	}
	parts := splitList(strings.ReplaceAll(spec, "!", ""))
	listed := slices.Contains(parts, "table")
	if strings.HasPrefix(spec, "!") {
		return !listed
	}
	return listed
}

// recordTitle names a record for a breadcrumb: the <tui title=> path, or the first identity key it holds.
func recordTitle(rec any, e *browseEntry) string {
	if e != nil && e.hints.Title != "" {
		if v, ok := fields.Lookup(rec, e.hints.Title); ok && isScalar(v) {
			return oneLineCell(fields.DisplayValue(v))
		}
	}
	m, ok := rec.(map[string]any)
	if !ok {
		return ""
	}
	for _, k := range identityKeys {
		if v, ok := m[k]; ok && isScalar(v) && v != nil {
			return oneLineCell(fields.DisplayValue(v))
		}
	}
	return ""
}

// record is the record the user is on in this level, for links: the selected row of a table, or the object itself.
func (l *dataLevel) record() map[string]any {
	switch l.kind {
	case levelTable:
		if l.sel >= 0 && l.sel < len(l.items) {
			m, _ := l.items[l.sel].(map[string]any)
			return m
		}
	case levelObject:
		m, _ := l.value.(map[string]any)
		return m
	}
	return nil
}

// child is the level the selected row opens, or nil when the row holds nothing to step into.
func (l *dataLevel) child(e *browseEntry) *dataLevel {
	if l.sel < 0 || l.sel >= len(l.items) {
		return nil
	}
	item := l.items[l.sel]
	switch l.kind {
	case levelTable:
		return newLevel(recordTitle(item, e), item, nil, nil)
	case levelObject:
		if isScalar(item) {
			if s, ok := item.(string); !ok || !strings.ContainsAny(s, "\n") {
				return nil
			}
		}
		return newLevel(l.keys[l.sel], item, nil, nil)
	case levelList:
		if isScalar(item) {
			return nil
		}
		return newLevel(fmt.Sprintf("[%d]", l.sel), item, nil, nil)
	}
	return nil
}

// filtered is the rows that hold every word of filter, as indices into the level's rows.
func (l *dataLevel) filtered(filter string) []int {
	words := strings.Fields(strings.ToLower(filter))
	var out []int
	for i, row := range l.cells {
		hay := strings.ToLower(strings.Join(row, " "))
		if !slices.ContainsFunc(words, func(w string) bool { return !strings.Contains(hay, w) }) {
			out = append(out, i)
		}
	}
	return out
}
