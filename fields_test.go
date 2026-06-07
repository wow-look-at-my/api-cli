package main

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fctx builds a minimal format context with the parsed body at .data.
func fctx(parsed any) map[string]any {
	return map[string]any{"data": parsed, "tty": false, "width": 0}
}

func TestFields_TableAutoFromArray(t *testing.T) {
	parsed := []any{
		map[string]any{"login": "a", "stars": int64(10)},
		map[string]any{"login": "bb", "stars": int64(2)},
	}
	f := &Fields{List: []Field{{Name: "login", Path: "login"}, {Name: "stars", Path: "stars"}}}
	out, err := renderFields(f, parsed, fctx(parsed), "", 0)
	require.NoError(t, err)
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	require.Len(t, lines, 3)
	assert.Equal(t, "login  stars", lines[0])
	assert.Equal(t, "a      10", lines[1])
	assert.Equal(t, "bb     2", lines[2])
}

func TestFields_ListAutoFromObject(t *testing.T) {
	parsed := map[string]any{"id": int64(1), "name": "Ada"}
	f := &Fields{List: []Field{{Name: "id", Path: "id"}, {Name: "name", Path: "name"}}}
	out, err := renderFields(f, parsed, fctx(parsed), "", 0)
	require.NoError(t, err)
	assert.Contains(t, out, "id:   1\n")
	assert.Contains(t, out, "name: Ada\n")
}

func TestFields_LinesFromScalars(t *testing.T) {
	parsed := []any{"go", "rust", "zig"}
	f := &Fields{}
	out, err := renderFields(f, parsed, fctx(parsed), "", 0)
	require.NoError(t, err)
	assert.Equal(t, "go\nrust\nzig\n", out)
}

func TestFields_Over(t *testing.T) {
	parsed := map[string]any{"items": []any{map[string]any{"n": int64(1)}}, "total": int64(99)}
	f := &Fields{Over: "data.items", Footer: "{{.data.total}} total", List: []Field{{Name: "n", Path: "n"}}}
	out, err := renderFields(f, parsed, fctx(parsed), "table", 0)
	require.NoError(t, err)
	assert.Contains(t, out, "n\n1\n")
	assert.Contains(t, out, "99 total")
}

func TestFields_MapWalkWithExpr(t *testing.T) {
	parsed := map[string]any{"Go": int64(100), "Rust": int64(100)}
	f := &Fields{
		Over:	"data",
		List: []Field{
			{Name: "language", Path: "@key"},
			{Name: "bytes", Path: "@value"},
			{Name: "percent", Expr: `{{ $t := 0 }}{{ range $.data }}{{ $t = add $t . }}{{ end }}{{ printf "%.1f%%" (mulf 100.0 (divf .value $t)) }}`},
		},
	}
	out, err := renderFields(f, parsed, fctx(parsed), "table", 0)
	require.NoError(t, err)
	assert.Contains(t, out, "language")
	assert.Contains(t, out, "Go")
	assert.Contains(t, out, "50.0%")
}

func TestFields_ShowInGating(t *testing.T) {
	parsed := map[string]any{"state": "open", "draft": true}
	f := &Fields{List: []Field{
		{Name: "state", Path: "state", ShowIn: "json"},
		{Name: "state", Expr: "{{.state}} (composed)", ShowIn: "!json"},
	}}
	// Human list: the virtual composed field shows, the json-only does not.
	listOut, err := renderFields(f, parsed, fctx(parsed), "list", 0)
	require.NoError(t, err)
	assert.Contains(t, listOut, "open (composed)")

	// JSON: the raw field shows, the !json virtual does not.
	jsonOut, err := renderFields(f, parsed, fctx(parsed), "json", 0)
	require.NoError(t, err)
	assert.Contains(t, jsonOut, `"state": "open"`)
	assert.NotContains(t, jsonOut, "composed")
}

func TestFields_JSONSinkProjection(t *testing.T) {
	parsed := []any{map[string]any{"a": int64(1), "b": "x", "c": "drop"}}
	f := &Fields{List: []Field{{Name: "a", Path: "a"}, {Name: "b", Path: "b"}}}
	out, err := renderFields(f, parsed, fctx(parsed), "json", 0)
	require.NoError(t, err)
	assert.Contains(t, out, `"a": 1`)
	assert.Contains(t, out, `"b": "x"`)
	assert.NotContains(t, out, "drop")
}

func TestFields_JSONSinkDerived(t *testing.T) {
	parsed := map[string]any{"a": int64(1)}
	f := &Fields{}
	out, err := renderFields(f, parsed, fctx(parsed), "json", 0)
	require.NoError(t, err)
	assert.Contains(t, out, `"a": 1`)
}

func TestFields_DefaultTruncateFirstline(t *testing.T) {
	parsed := map[string]any{
		"lang":	nil,
		"sha":	"abcdef1234567890",
		"msg":	"first line\nsecond line",
	}
	f := &Fields{List: []Field{
		{Name: "lang", Path: "lang", Default: "-"},
		{Name: "sha", Path: "sha", Truncate: 7},
		{Name: "msg", Path: "msg", FirstLine: true},
	}}
	out, err := renderFields(f, parsed, fctx(parsed), "list", 0)
	require.NoError(t, err)
	assert.Contains(t, out, "lang: -\n")
	assert.Contains(t, out, "sha:  abcdef1\n")
	assert.Contains(t, out, "msg:  first line\n")
}

func TestFields_PriorityDrop(t *testing.T) {
	parsed := []any{map[string]any{"a": "1", "b": "2", "c": "3"}}
	f := &Fields{List: []Field{
		{Name: "keep", Path: "a", Priority: 0},
		{Name: "drop", Path: "b", Priority: -2},
		{Name: "alsokeep", Path: "c", Priority: 0},
	}}
	// Narrow width forces the lowest-priority column out (full table is 20
	// wide; 16 fits keep+alsokeep but not the priority -2 column).
	out, err := renderFields(f, parsed, fctx(parsed), "table", 16)
	require.NoError(t, err)
	assert.Contains(t, out, "keep")
	assert.Contains(t, out, "alsokeep")
	assert.NotContains(t, out, "drop")
}

func TestFields_Markdown(t *testing.T) {
	parsed := []any{map[string]any{"a": "1", "b": "x|y"}}
	f := &Fields{List: []Field{{Name: "a", Path: "a"}, {Name: "b", Path: "b"}}}
	out, err := renderFields(f, parsed, fctx(parsed), "markdown", 0)
	require.NoError(t, err)
	assert.Contains(t, out, "| a | b |")
	assert.Contains(t, out, "| --- | --- |")
	assert.Contains(t, out, `x\|y`)
}

func TestFields_CSV(t *testing.T) {
	parsed := []any{map[string]any{"a": "1", "b": "has,comma"}}
	f := &Fields{List: []Field{{Name: "a", Path: "a"}, {Name: "b", Path: "b"}}}
	out, err := renderFields(f, parsed, fctx(parsed), "csv", 0)
	require.NoError(t, err)
	assert.Contains(t, out, "a,b\n")
	assert.Contains(t, out, `"has,comma"`)
}

func TestFields_RawScalar(t *testing.T) {
	f := &Fields{}
	out, err := renderFields(f, "just a string", fctx("just a string"), "", 0)
	require.NoError(t, err)
	assert.Equal(t, "just a string\n", out)
}

func TestFields_UnknownSink(t *testing.T) {
	f := &Fields{List: []Field{{Name: "a", Path: "a"}}}
	_, err := renderFields(f, []any{map[string]any{"a": "1"}}, fctx(nil), "bogus", 0)
	require.Error(t, err)
}

func TestShowIn(t *testing.T) {
	assert.True(t, showIn("", "table"))
	assert.True(t, showIn("*", "json"))
	assert.True(t, showIn("json,csv", "json"))
	assert.False(t, showIn("json,csv", "table"))
	assert.False(t, showIn("!json", "json"))
	assert.True(t, showIn("!json", "table"))
}

func TestDisplayValue(t *testing.T) {
	assert.Equal(t, "", displayValue(nil))
	assert.Equal(t, "hi", displayValue("hi"))
	assert.Equal(t, "true", displayValue(true))
	assert.Equal(t, "42", displayValue(int64(42)))
	assert.Equal(t, "3", displayValue(float64(3)))
	assert.Equal(t, "1.5", displayValue(1.5))
	assert.Equal(t, `["a","b"]`, displayValue([]any{"a", "b"}))
}

func TestFields_MixedObjectNullArrayStaysTable(t *testing.T) {
	parsed := []any{
		map[string]any{"login": "a", "stars": int64(1)},
		nil, // a null row among objects must not collapse the table to lines
		map[string]any{"login": "b", "stars": int64(2)},
	}
	f := &Fields{List: []Field{{Name: "login", Path: "login"}, {Name: "stars", Path: "stars"}}}
	out, err := renderFields(f, parsed, fctx(parsed), "", 0)
	require.NoError(t, err)
	assert.Contains(t, out, "login  stars")
	assert.Contains(t, out, "a")
	assert.Contains(t, out, "b")
}

func TestFields_JSONDefaultOnEmptyString(t *testing.T) {
	parsed := map[string]any{"a": ""}
	f := &Fields{List: []Field{{Name: "a", Path: "a", Default: "N/A"}}}
	out, err := renderFields(f, parsed, fctx(parsed), "json", 0)
	require.NoError(t, err)
	assert.Contains(t, out, `"a": "N/A"`)
}

func TestFields_FooterSuppressedWhenBodyEmpty(t *testing.T) {
	parsed := []any{map[string]any{"a": "1"}}
	// The only field is json-only, so the table body is empty in this sink.
	f := &Fields{Footer: "FOOT", List: []Field{{Name: "a", Path: "a", ShowIn: "json"}}}
	out, err := renderFields(f, parsed, fctx(parsed), "table", 0)
	require.NoError(t, err)
	assert.NotContains(t, out, "FOOT")
}
