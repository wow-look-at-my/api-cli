package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"text/template"

	"github.com/Masterminds/sprig/v3"
)

// envMap returns the process environment as a map[string]string for use as
// {{.env.VAR}} inside templates.
func envMap() map[string]string {
	out := make(map[string]string, len(os.Environ()))
	for _, kv := range os.Environ() {
		if i := strings.IndexByte(kv, '='); i > 0 {
			out[kv[:i]] = kv[i+1:]
		}
	}
	return out
}

// spreadSentinel (NUL) delimits elements in spread output; spreadEndSentinel
// (SOH) terminates a spread region. These are reserved internal markers —
// spread() rejects elements containing either byte.
const spreadSentinel = "\x00"
const spreadEndSentinel = "\x01"

// funcMap is the template function set available in every rendered template.
// It combines sprig's text FuncMap (a broad library of string/list/math/json
// helpers) with a few custom helpers specific to this tool.
func funcMap() template.FuncMap {
	fm := sprig.TxtFuncMap()
	fm["querystring"] = queryString
	fm["shellquote"] = shellQuote
	fm["urlpath"] = url.PathEscape
	fm["spread"] = spread
	fm["fileExists"] = fileExists
	fm["dirExists"] = dirExists
	fm["repeatkey"] = repeatKey
	fm["tabwriter"] = tabwriter
	fm["padRight"] = padRight
	fm["padLeft"] = padLeft
	fm["displayWidth"] = displayWidth
	fm["stripANSI"] = stripANSI
	fm["filterSuffix"] = filterSuffix
	fm["filterPrefix"] = filterPrefix
	fm["truthy"] = templateTruthy
	return fm
}

// templateTruthy reports whether v is truthy under the same rules as the format
// `when` predicates (see isTruthy): nil and false are falsy; strings use
// isTruthy ("", "false", "0", "no" falsy); other values are stringified first.
// Used by <if test=> placeholders compiled from XML.
func templateTruthy(v any) bool {
	switch x := v.(type) {
	case nil:
		return false
	case bool:
		return x
	case string:
		return isTruthy(x)
	default:
		return isTruthy(fmt.Sprintf("%v", x))
	}
}

// tabwriter formats rows with columns aligned by displayWidth. Accepts:
//   - []string: one row per element, tab-separated columns.
//   - [][]string or [][]any: explicit cells per row.
//   - []any: each element is a row; either a string or a []any of cells.
//
// Default padding between columns is 2 spaces. ANSI escapes pass through.
func tabwriter(v any) (string, error) {
	rows, err := toRows(v)
	if err != nil {
		return "", fmt.Errorf("tabwriter: %w", err)
	}
	return alignColumns(rows, 2), nil
}

func toRows(v any) ([]string, error) {
	switch x := v.(type) {
	case nil:
		return nil, nil
	case []string:
		return x, nil
	case [][]string:
		out := make([]string, len(x))
		for i, row := range x {
			out[i] = strings.Join(row, "\t")
		}
		return out, nil
	case [][]any:
		out := make([]string, len(x))
		for i, row := range x {
			cells := make([]string, len(row))
			for j, cell := range row {
				cells[j] = fmt.Sprintf("%v", cell)
			}
			out[i] = strings.Join(cells, "\t")
		}
		return out, nil
	case []any:
		out := make([]string, 0, len(x))
		for _, row := range x {
			switch r := row.(type) {
			case string:
				out = append(out, r)
			case []any:
				cells := make([]string, len(r))
				for j, c := range r {
					cells[j] = fmt.Sprintf("%v", c)
				}
				out = append(out, strings.Join(cells, "\t"))
			case []string:
				out = append(out, strings.Join(r, "\t"))
			default:
				return nil, fmt.Errorf("row %T not supported (string or []any expected)", row)
			}
		}
		return out, nil
	default:
		return nil, fmt.Errorf("expected []string / [][]string / []any, got %T", v)
	}
}

// spread expands a slice into multiple arguments. In argv-form commands, the
// element "{{spread .arg.files}}" becomes N separate argv entries. In
// shell-form commands, expandSpreadForShell (exec.go) replaces each sentinel
// region with individually shell-quoted elements.
//
// Output format: \x00elem1\x00elem2\x01 (NUL-delimited, SOH-terminated).
// Elements must not contain \x00 or \x01; spread returns an error if they do.
//
// Accepted shapes: nil, []string, []int, []any (each element stringified).
func spread(v any) (string, error) {
	parts, err := toStringSlice(v)
	if err != nil {
		return "", fmt.Errorf("spread: %w", err)
	}
	for _, p := range parts {
		if strings.ContainsAny(p, spreadSentinel+spreadEndSentinel) {
			return "", fmt.Errorf("spread: element contains reserved sentinel byte: %q", p)
		}
	}
	if len(parts) == 0 {
		return spreadSentinel + spreadEndSentinel, nil
	}
	return spreadSentinel + strings.Join(parts, spreadSentinel) + spreadEndSentinel, nil
}

func toStringSlice(v any) ([]string, error) {
	switch x := v.(type) {
	case nil:
		return nil, nil
	case []string:
		return x, nil
	case []int:
		out := make([]string, len(x))
		for i, n := range x {
			out[i] = strconv.Itoa(n)
		}
		return out, nil
	case []any:
		out := make([]string, len(x))
		for i, item := range x {
			out[i] = fmt.Sprintf("%v", item)
		}
		return out, nil
	default:
		return nil, fmt.Errorf("expected slice, got %T", v)
	}
}

// fileExists reports whether path exists and is a regular file. Errors other
// than "not exist" surface as false (template helpers shouldn't error on
// permission issues during a precondition check).
func fileExists(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return info.Mode().IsRegular()
}

// dirExists reports whether path exists and is a directory.
func dirExists(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return info.IsDir()
}

// filterSuffix returns elements from list that end with the given suffix.
// Used in passthrough mode to locate specific files: {{.rest | filterSuffix ".cpp1.ii" | first}}.
func filterSuffix(suffix string, list any) ([]string, error) {
	items, err := toStringSlice(list)
	if err != nil {
		return nil, fmt.Errorf("filterSuffix: %w", err)
	}
	var out []string
	for _, s := range items {
		if strings.HasSuffix(s, suffix) {
			out = append(out, s)
		}
	}
	return out, nil
}

// filterPrefix returns elements from list that start with the given prefix.
func filterPrefix(prefix string, list any) ([]string, error) {
	items, err := toStringSlice(list)
	if err != nil {
		return nil, fmt.Errorf("filterPrefix: %w", err)
	}
	var out []string
	for _, s := range items {
		if strings.HasPrefix(s, prefix) {
			out = append(out, s)
		}
	}
	return out, nil
}

// queryString renders a map (or struct) of parameters as a URL-encoded query
// string prefixed with "?". An empty/nil map yields the empty string so it's
// safe to inline directly after a path: "{{.entry.path}}{{querystring .entry.query}}".
//
// Accepted input shapes:
//   - map[string]string, map[string]any — keys and string values
//   - values that are themselves slices produce repeated key=value pairs
//     (e.g., {"tag": ["a", "b"]} → "?tag=a&tag=b")
//
// Values that render to the empty string are dropped, so optional flags
// defaulted to "" don't clutter the URL.
func queryString(v any) (string, error) {
	if v == nil {
		return "", nil
	}
	values := url.Values{}
	switch m := v.(type) {
	case map[string]string:
		for k, val := range m {
			if val != "" {
				values.Add(k, val)
			}
		}
	case map[string]any:
		for k, val := range m {
			if err := addQueryValue(values, k, val); err != nil {
				return "", err
			}
		}
	default:
		return "", fmt.Errorf("querystring: unsupported type %T (need map)", v)
	}
	enc := values.Encode()
	if enc == "" {
		return "", nil
	}
	return "?" + enc, nil
}

func addQueryValue(values url.Values, key string, v any) error {
	switch val := v.(type) {
	case nil:
		return nil
	case string:
		if val != "" {
			values.Add(key, val)
		}
	case bool:
		values.Add(key, fmt.Sprintf("%t", val))
	case json.Number:
		values.Add(key, val.String())
	case int, int64, float64:
		values.Add(key, fmt.Sprintf("%v", val))
	case []any:
		for _, item := range val {
			if err := addQueryValue(values, key, item); err != nil {
				return err
			}
		}
	case []string:
		for _, item := range val {
			if item != "" {
				values.Add(key, item)
			}
		}
	default:
		return fmt.Errorf("querystring: unsupported value type %T for key %q", v, key)
	}
	return nil
}

// repeatKey emits repeated query-string parameters for a single key, one per
// slice element: repeatkey "tag" ["a","b"] → "tag=a&tag=b" (URL-encoded, no
// leading "?"). Empty string elements are dropped. Nil/empty slices yield "".
func repeatKey(key string, v any) (string, error) {
	parts, err := toStringSlice(v)
	if err != nil {
		return "", fmt.Errorf("repeatkey: %w", err)
	}
	values := url.Values{}
	for _, p := range parts {
		if p != "" {
			values.Add(key, p)
		}
	}
	enc := values.Encode()
	return enc, nil
}

// shellQuote wraps s in single quotes for safe interpolation into a POSIX sh
// command line. Embedded single quotes are escaped using the 'one single
// quote per quote' dance, e.g. foo'bar becomes:
//
//	'foo'\''bar'
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// renderString executes a text/template against data using the full funcMap.
//
// missingkey=zero: accessing a missing map key yields the zero value of the
// map's element type. Combined with sprig's `required` helper this lets the
// config author decide per-field whether a missing value is an error.
func renderString(tmpl string, data any) (string, error) {
	t, err := template.New("t").Funcs(funcMap()).Option("missingkey=zero").Parse(tmpl)
	if err != nil {
		return "", fmt.Errorf("parse template %q: %w", tmpl, err)
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("execute template %q: %w", tmpl, err)
	}
	return buf.String(), nil
}

// renderEntry walks raw JSON, rendering every string leaf as a template
// against the given data context. Numbers, booleans, and nulls pass through
// unchanged; object keys are never rendered. Returns a Go value (map, slice,
// string, json.Number, bool, nil) suitable for exposure to a subsequent
// template render as `.entry`.
//
// Returns nil if raw is empty or explicitly null.
func renderEntry(raw json.RawMessage, data any) (any, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil, nil
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, fmt.Errorf("parse entry: %w", err)
	}
	return walkEntry(v, data)
}

func walkEntry(v any, data any) (any, error) {
	switch x := v.(type) {
	case string:
		return renderString(x, data)
	case map[string]any:
		out := make(map[string]any, len(x))
		// Sort keys for deterministic output (helps tests & debugging).
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			r, err := walkEntry(x[k], data)
			if err != nil {
				return nil, fmt.Errorf("at key %q: %w", k, err)
			}
			out[k] = r
		}
		return out, nil
	case []any:
		out := make([]any, len(x))
		for i, vv := range x {
			r, err := walkEntry(vv, data)
			if err != nil {
				return nil, fmt.Errorf("at index %d: %w", i, err)
			}
			out[i] = r
		}
		return out, nil
	default:
		return v, nil
	}
}

// lookupPath walks a dotted context path ("var.filter", "data.items") into a
// nested map structure. Returns nil if any segment is missing or a non-map is
// traversed. An empty path returns the value unchanged.
func lookupPath(data any, path string) any {
	cur := data
	for _, seg := range strings.Split(path, ".") {
		seg = strings.TrimSpace(seg)
		if seg == "" {
			continue
		}
		m, ok := cur.(map[string]any)
		if !ok {
			return nil
		}
		cur = m[seg]
	}
	return cur
}

// mergeVars merges `child` into a copy of `parent`, with the child winning on
// key collision. Nil inputs are treated as empty maps. Returns a fresh map.
func mergeVars(parent, child map[string]any) map[string]any {
	out := make(map[string]any, len(parent)+len(child))
	for k, v := range parent {
		out[k] = v
	}
	for k, v := range child {
		out[k] = v
	}
	return out
}
