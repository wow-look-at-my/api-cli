package main

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// knownSinks are the representations a Fields declaration can be rendered as.
var knownSinks = map[string]bool{
	"table": true, "list": true, "lines": true,
	"raw": true, "json": true, "markdown": true, "csv": true,
}

// record is one row of output. obj is the object (object record), the entry
// value (when walking a map), or a scalar. key is the entry key when isEntry.
type record struct {
	obj     any
	key     string
	isEntry bool
}

// renderFields represents a Fields declaration as the chosen sink. An empty
// sink auto-selects from the data shape. width (>0) enables priority-based
// column dropping for tables.
func renderFields(f *Fields, parsed any, ctx map[string]any, sink string, width int) (string, error) {
	recs, shape := resolveRecords(f, parsed, ctx)

	fieldsList := f.List
	derived := len(fieldsList) == 0
	if derived {
		fieldsList = deriveFields(recs)
	}

	if sink == "" {
		sink = defaultSink(shape)
	}
	if !knownSinks[sink] {
		return "", fmt.Errorf("unknown representation %q", sink)
	}

	var (
		out string
		err error
	)
	switch sink {
	case "json":
		return renderJSONSink(f, recs, fieldsList, shape, parsed, ctx, derived)
	case "raw":
		out = renderRawSink(recs)
	case "lines":
		out, err = renderLinesSink(recs, fieldsList, ctx)
	case "list":
		out, err = renderListSink(recs, fieldsList, ctx, sink)
	case "table":
		out, err = renderTableSink(recs, fieldsList, ctx, sink, width)
	case "markdown":
		out, err = renderMarkdownSink(recs, fieldsList, ctx, sink)
	case "csv":
		out, err = renderCSVSink(recs, fieldsList, ctx, sink)
	}
	if err != nil {
		return "", err
	}

	if f.Footer != "" && humanSink(sink) && strings.TrimSpace(out) != "" {
		foot, ferr := renderString(f.Footer, ctx)
		if ferr != nil {
			return "", fmt.Errorf("render footer: %w", ferr)
		}
		if strings.TrimSpace(foot) != "" {
			out = strings.TrimRight(out, "\n") + "\n" + foot + "\n"
		}
	}
	return out, nil
}

// humanSink reports whether a footer line should follow the body. Markdown and
// csv are structured outputs, so a trailing prose footer would corrupt them.
func humanSink(sink string) bool {
	switch sink {
	case "table", "list", "lines":
		return true
	}
	return false
}

// resolveRecords selects the record set per f.Over and classifies its shape:
// "array-objects", "array-scalars", "map-entries", "single", or "scalar".
func resolveRecords(f *Fields, parsed any, ctx map[string]any) ([]record, string) {
	src := parsed
	if f.Over != "" {
		src = lookupPath(ctx, f.Over)
	}
	switch v := src.(type) {
	case []any:
		hasMap := false
		for _, el := range v {
			if _, ok := el.(map[string]any); ok {
				hasMap = true
				break
			}
		}
		recs := make([]record, 0, len(v))
		for _, el := range v {
			recs = append(recs, record{obj: el})
		}
		// Scalars (lines) only when no element is an object and no fields are
		// declared. A declared field list, or any object element (e.g. a null
		// row among objects), keeps the table shape; missing values render empty.
		if !hasMap && len(f.List) == 0 {
			return recs, "array-scalars"
		}
		return recs, "array-objects"
	case map[string]any:
		if fieldsWalkMap(f) {
			keys := sortedKeys(v)
			recs := make([]record, 0, len(v))
			for _, k := range keys {
				recs = append(recs, record{obj: v[k], key: k, isEntry: true})
			}
			return recs, "map-entries"
		}
		return []record{{obj: v}}, "single"
	default:
		return []record{{obj: src}}, "scalar"
	}
}

// fieldsWalkMap reports whether any field reads @key/@value, the signal that a
// map should be walked entry-by-entry rather than treated as a single record.
func fieldsWalkMap(f *Fields) bool {
	for _, fld := range f.List {
		if fld.Path == "@key" || fld.Path == "@value" {
			return true
		}
	}
	return false
}

func defaultSink(shape string) string {
	switch shape {
	case "array-objects", "map-entries":
		return "table"
	case "array-scalars":
		return "lines"
	case "single":
		return "list"
	default:
		return "raw"
	}
}

// deriveFields builds a field list from record keys when none were declared.
func deriveFields(recs []record) []Field {
	for _, r := range recs {
		if m, ok := r.obj.(map[string]any); ok {
			var out []Field
			for _, k := range sortedKeys(m) {
				out = append(out, Field{Name: k, Path: k})
			}
			return out
		}
	}
	return nil
}

// includedFields returns the fields visible in the given sink per show_in.
func includedFields(fields []Field, sink string) []Field {
	out := make([]Field, 0, len(fields))
	for _, fld := range fields {
		if showIn(fld.ShowIn, sink) {
			out = append(out, fld)
		}
	}
	return out
}

// showIn reports whether a field with the given show_in spec is visible in the
// sink. "" / "*" = all; "a,b" = only those; "!a,b" = all except those.
func showIn(spec, sink string) bool {
	spec = strings.TrimSpace(spec)
	if spec == "" || spec == "*" {
		return true
	}
	neg := strings.HasPrefix(spec, "!")
	if neg {
		spec = spec[1:]
	}
	in := false
	for _, p := range strings.Split(spec, ",") {
		if strings.TrimSpace(p) == sink {
			in = true
			break
		}
	}
	if neg {
		return !in
	}
	return in
}

// cellValue computes a field's display string for a record, applying default,
// firstline, and truncate.
func cellValue(fld Field, rec record, ctx map[string]any) (string, error) {
	raw, err := rawFieldValue(fld, rec, ctx)
	if err != nil {
		return "", err
	}
	s := displayValue(raw)
	if strings.TrimSpace(s) == "" && fld.Default != "" {
		s = fld.Default
	}
	if fld.FirstLine {
		if i := strings.IndexAny(s, "\r\n"); i >= 0 {
			s = s[:i]
		}
	}
	if fld.Truncate > 0 {
		r := []rune(s)
		if len(r) > fld.Truncate {
			s = string(r[:fld.Truncate])
		}
	}
	return s, nil
}

// rawFieldValue resolves a field's underlying value (before display coercion).
func rawFieldValue(fld Field, rec record, ctx map[string]any) (any, error) {
	switch fld.Path {
	case "@key":
		return rec.key, nil
	case "@value":
		return rec.obj, nil
	}
	if fld.Expr != "" {
		return renderString(fld.Expr, exprData(rec, ctx))
	}
	if fld.Path == "" {
		return rec.obj, nil
	}
	return lookupPath(rec.obj, fld.Path), nil
}

// exprData builds the data context for a field expr: the whole format context,
// with the record's own fields promoted to the top level (so `.field` is the
// record), plus `.key`/`.value` for map-entry rows. `$` therefore reaches
// `.var`, `.data`, etc.
func exprData(rec record, ctx map[string]any) map[string]any {
	d := make(map[string]any, len(ctx)+4)
	for k, v := range ctx {
		d[k] = v
	}
	if m, ok := rec.obj.(map[string]any); ok {
		for k, v := range m {
			d[k] = v
		}
	}
	if rec.isEntry {
		d["key"] = rec.key
		d["value"] = rec.obj
	}
	return d
}

func displayValue(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case bool:
		return strconv.FormatBool(x)
	case json.Number:
		return x.String()
	case int:
		return strconv.Itoa(x)
	case int64:
		return strconv.FormatInt(x, 10)
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	default:
		b, err := json.Marshal(x)
		if err != nil {
			return fmt.Sprintf("%v", x)
		}
		return string(b)
	}
}

// ---------------------------------------------------------------------------
// Sinks
// ---------------------------------------------------------------------------

func renderRawSink(recs []record) string {
	var b strings.Builder
	for _, r := range recs {
		b.WriteString(displayValue(r.obj))
		b.WriteByte('\n')
	}
	return b.String()
}

func renderLinesSink(recs []record, fields []Field, ctx map[string]any) (string, error) {
	inc := includedFields(fields, "lines")
	var b strings.Builder
	for _, r := range recs {
		// Scalar records print their value directly; only object records use the
		// first declared field as the line.
		if _, isMap := r.obj.(map[string]any); len(inc) == 0 || !isMap {
			b.WriteString(displayValue(r.obj))
		} else {
			v, err := cellValue(inc[0], r, ctx)
			if err != nil {
				return "", err
			}
			b.WriteString(v)
		}
		b.WriteByte('\n')
	}
	return b.String(), nil
}

func renderListSink(recs []record, fields []Field, ctx map[string]any, sink string) (string, error) {
	inc := includedFields(fields, sink)
	maxLabel := 0
	for _, fld := range inc {
		if w := displayWidth(fld.Name) + 1; w > maxLabel {
			maxLabel = w
		}
	}
	var b strings.Builder
	for i, r := range recs {
		if i > 0 {
			b.WriteByte('\n')
		}
		for _, fld := range inc {
			v, err := cellValue(fld, r, ctx)
			if err != nil {
				return "", err
			}
			b.WriteString(padRight(maxLabel+1, fld.Name+":"))
			b.WriteString(v)
			b.WriteByte('\n')
		}
	}
	return b.String(), nil
}

func renderTableSink(recs []record, fields []Field, ctx map[string]any, sink string, width int) (string, error) {
	inc := includedFields(fields, sink)
	if len(inc) == 0 {
		return "", nil
	}
	cols, err := buildColumns(inc, recs, ctx)
	if err != nil {
		return "", err
	}
	cols = dropByPriority(cols, width, 2)
	rows := make([]string, len(recs)+1)
	for ri := 0; ri <= len(recs); ri++ {
		cells := make([]string, len(cols))
		for ci, col := range cols {
			cells[ci] = col.cells[ri]
		}
		rows[ri] = strings.Join(cells, "\t")
	}
	return alignColumns(rows, 2), nil
}

func renderMarkdownSink(recs []record, fields []Field, ctx map[string]any, sink string) (string, error) {
	inc := includedFields(fields, sink)
	if len(inc) == 0 {
		return "", nil
	}
	var b strings.Builder
	header := make([]string, len(inc))
	sep := make([]string, len(inc))
	for i, fld := range inc {
		header[i] = fld.Name
		sep[i] = "---"
	}
	b.WriteString("| " + strings.Join(header, " | ") + " |\n")
	b.WriteString("| " + strings.Join(sep, " | ") + " |\n")
	for _, r := range recs {
		cells := make([]string, len(inc))
		for i, fld := range inc {
			v, err := cellValue(fld, r, ctx)
			if err != nil {
				return "", err
			}
			cells[i] = strings.ReplaceAll(strings.ReplaceAll(v, "|", "\\|"), "\n", " ")
		}
		b.WriteString("| " + strings.Join(cells, " | ") + " |\n")
	}
	return b.String(), nil
}

func renderCSVSink(recs []record, fields []Field, ctx map[string]any, sink string) (string, error) {
	inc := includedFields(fields, sink)
	if len(inc) == 0 {
		return "", nil
	}
	var b strings.Builder
	header := make([]string, len(inc))
	for i, fld := range inc {
		header[i] = csvQuote(fld.Name)
	}
	b.WriteString(strings.Join(header, ",") + "\n")
	for _, r := range recs {
		cells := make([]string, len(inc))
		for i, fld := range inc {
			v, err := cellValue(fld, r, ctx)
			if err != nil {
				return "", err
			}
			cells[i] = csvQuote(v)
		}
		b.WriteString(strings.Join(cells, ",") + "\n")
	}
	return b.String(), nil
}

func csvQuote(s string) string {
	if strings.ContainsAny(s, ",\"\n\r") {
		return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
	}
	return s
}

func renderJSONSink(f *Fields, recs []record, fields []Field, shape string, parsed any, ctx map[string]any, derived bool) (string, error) {
	if derived {
		// No projection: emit the selected records as-is.
		switch shape {
		case "single":
			return marshalJSON(recs[0].obj)
		case "scalar":
			return marshalJSON(recs[0].obj)
		default:
			arr := make([]any, len(recs))
			for i, r := range recs {
				arr[i] = r.obj
			}
			return marshalJSON(arr)
		}
	}
	inc := includedFields(fields, "json")
	project := func(r record) (any, error) {
		m := make(map[string]any, len(inc))
		for _, fld := range inc {
			raw, err := rawFieldValue(fld, r, ctx)
			if err != nil {
				return nil, err
			}
			// Match the human sinks: a blank value (nil or empty string) falls
			// back to the declared default.
			if fld.Default != "" && (raw == nil || raw == "") {
				raw = fld.Default
			}
			m[fld.Name] = raw
		}
		return m, nil
	}
	if shape == "single" {
		v, err := project(recs[0])
		if err != nil {
			return "", err
		}
		return marshalJSON(v)
	}
	arr := make([]any, len(recs))
	for i, r := range recs {
		v, err := project(r)
		if err != nil {
			return "", err
		}
		arr[i] = v
	}
	return marshalJSON(arr)
}

// ---------------------------------------------------------------------------
// Table columns + priority dropping
// ---------------------------------------------------------------------------

type column struct {
	cells    []string // cells[0] is the header, cells[1..] are rows
	priority int
	width    int
}

func buildColumns(inc []Field, recs []record, ctx map[string]any) ([]column, error) {
	cols := make([]column, len(inc))
	for ci, fld := range inc {
		c := column{cells: make([]string, len(recs)+1), priority: fld.Priority}
		c.cells[0] = fld.Name
		c.width = displayWidth(fld.Name)
		for ri, r := range recs {
			v, err := cellValue(fld, r, ctx)
			if err != nil {
				return nil, err
			}
			c.cells[ri+1] = v
			if w := displayWidth(v); w > c.width {
				c.width = w
			}
		}
		cols[ci] = c
	}
	return cols, nil
}

// dropByPriority removes the lowest-priority columns until the table fits in
// width, keeping at least one column. Ties drop the rightmost column first, so
// document order is otherwise preserved. width <= 0 disables dropping.
func dropByPriority(cols []column, width, padding int) []column {
	if width <= 0 {
		return cols
	}
	for len(cols) > 1 && tableWidth(cols, padding) > width {
		victim := 0
		for i := 1; i < len(cols); i++ {
			if cols[i].priority <= cols[victim].priority {
				victim = i
			}
		}
		cols = append(cols[:victim], cols[victim+1:]...)
	}
	return cols
}

func tableWidth(cols []column, padding int) int {
	total := 0
	for _, c := range cols {
		total += c.width
	}
	if len(cols) > 1 {
		total += padding * (len(cols) - 1)
	}
	return total
}
