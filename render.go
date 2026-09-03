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

	"github.com/wow-look-at-my/api-cli/fields"
	apidsl "github.com/wow-look-at-my/api-dsl"
)

// spreadSentinel (NUL) delimits elements in spread output; spreadEndSentinel
// (SOH) terminates a spread region. These are reserved internal markers —
// spread() rejects elements containing either byte.
const spreadSentinel = "\x00"
const spreadEndSentinel = "\x01"

// cliFuncs are the template functions this CLI adds to the shared set. Each one
// serves a shell, a file path, or a terminal column, which is vocabulary the
// shared language does not carry.
func cliFuncs() template.FuncMap {
	return template.FuncMap{
		"shellquote":   shellQuote,
		"spread":       spread,
		"fileExists":   fileExists,
		"dirExists":    dirExists,
		"tabwriter":    tabwriter,
		"padRight":     padRight,
		"padLeft":      padLeft,
		"displayWidth": displayWidth,
		"stripANSI":    stripANSI,
		"filterSuffix": filterSuffix,
		"filterPrefix": filterPrefix,
	}
}

// renderer executes every template this tool renders. It is read-only after
// construction, and safe for concurrent use.
var renderer = apidsl.NewRenderer(cliFuncs())

// The width-aware aligner and the <fields> renderer live in the fields package,
// which is importable on its own. These aliases keep this CLI's call sites
// reading as they did, and cliRenderer is what a field's expr and a footer
// evaluate with: the shared renderer plus the helpers above.
var (
	padRight     = fields.PadRight
	padLeft      = fields.PadLeft
	displayWidth = fields.DisplayWidth
	stripANSI    = fields.StripANSI
	alignColumns = fields.AlignColumns
)

var cliRenderer fields.Renderer = func(tmpl string, data map[string]any) (string, error) {
	return renderString(tmpl, data)
}

func renderFields(f *Fields, parsed any, ctx map[string]any, sink string, width int) (string, error) {
	return fields.Render(cliRenderer, f, parsed, ctx, sink, width)
}

// renderString executes a text/template against data with the shared functions
// plus cliFuncs.
func renderString(tmpl string, data any) (string, error) {
	return renderer.Render(tmpl, data)
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

// addQueryValue adds one declared parameter to values, per its Go type. A
// nested slice repeats the key. An empty string is dropped, so an unset
// optional parameter does not clutter the URL.
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

// shellQuote wraps s in single quotes for safe interpolation into a POSIX sh
// command line. Each embedded single quote is escaped by closing the quoted
// run, emitting an escaped quote, and reopening (see the replacement below).
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
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
