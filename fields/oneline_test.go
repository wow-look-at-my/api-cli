package fields

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A value with newlines used to put every later column at the left margin, which
// reads as a table with no columns at all.
func TestFields_TableFoldsAMultiLineCell(t *testing.T) {
	parsed := []any{
		map[string]any{"id": int64(1), "body": "first line\nsecond line", "tail": "x"},
		map[string]any{"id": int64(2), "body": "short", "tail": "y"},
	}
	f := &Fields{List: []Field{
		{Name: "id", Path: "id"},
		{Name: "body", Path: "body"},
		{Name: "tail", Path: "tail"},
	}}
	out, err := renderFields(testRenderer, f, parsed, fctx(parsed), "table", 0)
	require.NoError(t, err)

	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	require.Len(t, lines, 3, "one header and one line per record")
	assert.Equal(t, "id  body                    tail", lines[0])
	assert.Equal(t, "1   first line second line  x", lines[1])
	assert.Equal(t, "2   short                   y", lines[2])
}

// A tab inside a value opened a column that was not declared.
func TestFields_TableFoldsATabInACell(t *testing.T) {
	parsed := []any{map[string]any{"a": "left\tright", "b": "end"}}
	f := &Fields{List: []Field{{Name: "a", Path: "a"}, {Name: "b", Path: "b"}}}
	out, err := renderFields(testRenderer, f, parsed, fctx(parsed), "table", 0)
	require.NoError(t, err)

	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	require.Len(t, lines, 2)
	assert.Equal(t, "a           b", lines[0])
	assert.Equal(t, "left right  end", lines[1])
}

// Spacing inside a single-line value is left alone.
func TestFields_TableKeepsRunsOfSpaces(t *testing.T) {
	parsed := []any{map[string]any{"a": "two  spaces"}}
	f := &Fields{List: []Field{{Name: "a", Path: "a"}}}
	out, err := renderFields(testRenderer, f, parsed, fctx(parsed), "table", 0)
	require.NoError(t, err)
	assert.Contains(t, out, "two  spaces")
}

// The list sink keeps the whole value, and aligns its continuation lines under
// the label.
func TestFields_ListIndentsAMultiLineValue(t *testing.T) {
	parsed := map[string]any{"name": "Ada", "body": "first line\nsecond line"}
	f := &Fields{List: []Field{{Name: "name", Path: "name"}, {Name: "body", Path: "body"}}}
	out, err := renderFields(testRenderer, f, parsed, fctx(parsed), "list", 0)
	require.NoError(t, err)
	assert.Contains(t, out, "body: first line\n      second line\n")
}

func TestFields_MarkdownFoldsACell(t *testing.T) {
	parsed := []any{map[string]any{"a": "one\ttwo\nthree", "b": "p|q"}}
	f := &Fields{List: []Field{{Name: "a", Path: "a"}, {Name: "b", Path: "b"}}}
	out, err := renderFields(testRenderer, f, parsed, fctx(parsed), "markdown", 0)
	require.NoError(t, err)
	assert.Contains(t, out, `| one two three | p\|q |`)
}

// The width the drop decision reads is the folded width, not the whole value.
func TestFields_TableDropsOnTheFoldedWidth(t *testing.T) {
	parsed := []any{map[string]any{"id": int64(1), "body": "aaaa\nbbbb"}}
	f := &Fields{List: []Field{{Name: "id", Path: "id"}, {Name: "body", Path: "body"}}}
	out, err := renderFields(testRenderer, f, parsed, fctx(parsed), "table", 20)
	require.NoError(t, err)
	assert.Contains(t, out, "aaaa bbbb")
}
