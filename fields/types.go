// Package fields renders a declarative field list as a table, a "Label: value"
// list, lines, raw text, JSON, Markdown, CSV, or a timeline, choosing a default
// from the data's shape. api-cli drives it from a <fields> element, and it is
// importable on its own so another program can present decoded JSON the same
// way without adopting the XML config language.
package fields

// Renderer evaluates a Go template against a data map. The caller supplies it.
type Renderer func(template string, data map[string]any) (string, error)

// Fields declares the shape of a leaf's output records.
type Fields struct {
	Over   string  `json:"over,omitempty"`   // context path to the records (default: the whole body)
	Footer string  `json:"footer,omitempty"` // template for a trailing summary line
	List   []Field `json:"fields,omitempty"`
}

// Field is a column in a Fields declaration. Path reads the record ("@key" and
// "@value" name a map entry); Expr computes instead, with the record as "." and
// the context as "$". ShowIn takes an allowlist ("json,csv") or a denylist
// ("!json"), never a mix.
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
// follows the data's shape, a width lets a table drop columns by priority, and
// r evaluates Expr and Footer.
func Render(r Renderer, f *Fields, parsed any, ctx map[string]any, sink string, width int) (string, error) {
	return renderFields(r, f, parsed, ctx, sink, width)
}

// KnownSink reports whether Render accepts sink as a representation name.
func KnownSink(sink string) bool { return knownSinks.Contains(sink) }

// Sinks names every representation Render accepts, for a caller documenting them.
func Sinks() []string { return knownSinks.Values() }

// Lookup walks decoded JSON by a dotted path, maps by key and lists by index.
func Lookup(data any, path string) (value any, found bool) { return lookupData(data, path) }

// LookupValue is Lookup without the found flag: a miss is a nil value.
func LookupValue(data any, path string) any { return lookupValue(data, path) }

// DisplayWidth is the printed column width of s.
func DisplayWidth(s string) int { return displayWidth(s) }

// StripANSI removes every escape sequence from s.
func StripANSI(s string) string { return stripANSI(s) }

// AlignColumns pads tab-separated rows into columns separated by padding spaces.
func AlignColumns(rows []string, padding int) string { return alignColumns(rows, padding) }

// PadRight pads s with spaces on the right to n printed columns.
func PadRight(n int, s string) string { return padRight(n, s) }

// PadLeft is PadRight against the other margin.
func PadLeft(n int, s string) string { return padLeft(n, s) }
