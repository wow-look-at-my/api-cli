// Package fields renders a declarative field list as a table, a "Label: value"
// list, lines, raw text, JSON, Markdown, CSV, or a timeline, choosing a default
// from the data's shape. api-cli drives it from a <fields> element, and it is
// importable on its own so another program can present decoded JSON the same
// way without adopting the XML config language.
package fields

// Renderer evaluates a Go template against a data map. A Field's Expr and a
// Fields' Footer are templates, and the caller owns which helpers they see, so
// the renderer is supplied rather than built here.
type Renderer func(template string, data map[string]any) (string, error)

// Fields declares the shape of a leaf's output records: which fields, with
// optional rename / default / transform / compute. The renderer represents that
// one declaration automatically as a table, a "Label: value" list, lines, JSON,
// Markdown, or CSV, choosing a default from the data's shape (overridable with
// --as). Built from a <fields> element.
type Fields struct {
	Over   string  `json:"over,omitempty"`   // context path to the records (default: the whole body)
	Footer string  `json:"footer,omitempty"` // template for a trailing summary line
	List   []Field `json:"fields,omitempty"`
}

// Field is one column/row in a Fields declaration.
//
//   - Path is a record-relative source path ("stargazers_count", "user.login"),
//     or the sentinels "@key"/"@value" when Over walks a map.
//   - Expr, if set, is a Go template evaluated with the record as "." and the
//     whole format context as "$"; it overrides Path (a virtual field).
//   - Default substitutes for an empty value; Truncate caps the string length;
//     FirstLine keeps only the first line.
//   - Priority orders width-constrained dropping (lowest dropped first; default 0).
//   - ShowIn gates the field per representation: "" / "*" = all; an allowlist
//     ("json,csv") shows only there; a negated list ("!json") shows everywhere
//     except there. The two forms cannot be mixed.
type Field struct {
	Name      string `json:"name"`
	Path      string `json:"path,omitempty"`
	Expr      string `json:"expr,omitempty"`
	Default   string `json:"default,omitempty"`
	Truncate  int    `json:"truncate,omitempty"`
	FirstLine bool   `json:"firstLine,omitempty"`
	Priority  int    `json:"priority,omitempty"`
	ShowIn    string `json:"showIn,omitempty"`
}

// Render represents a Fields declaration as the chosen sink. An empty sink
// auto-selects from the data's shape; width (>0) enables priority-based column
// dropping for tables. r evaluates Expr and Footer templates and may be nil
// when the declaration uses neither.
func Render(r Renderer, f *Fields, parsed any, ctx map[string]any, sink string, width int) (string, error) {
	return renderFields(r, f, parsed, ctx, sink, width)
}

// Sinks reports the representation names Render accepts.
func Sinks() []string { return knownSinks.Slice() }

// Lookup resolves a dotted path against decoded JSON, walking maps by key and
// lists by index, and reports whether it landed on anything.
func Lookup(data any, path string) (value any, found bool) { return lookupData(data, path) }

// LookupValue is Lookup without the found flag: a miss is a nil value.
func LookupValue(data any, path string) any { return lookupValue(data, path) }

// DisplayWidth is the printed column width of s: ANSI escapes cost nothing and
// an East Asian Wide rune costs two.
func DisplayWidth(s string) int { return displayWidth(s) }

// StripANSI removes every escape sequence from s.
func StripANSI(s string) string { return stripANSI(s) }

// AlignColumns pads tab-separated rows into aligned columns, measuring each
// cell by DisplayWidth and separating columns by padding spaces.
func AlignColumns(rows []string, padding int) string { return alignColumns(rows, padding) }

// PadRight pads s with spaces to n printed columns, leaving it alone when it
// is already that wide.
func PadRight(n int, s string) string { return padRight(n, s) }

// PadLeft is PadRight against the other margin.
func PadLeft(n int, s string) string { return padLeft(n, s) }
