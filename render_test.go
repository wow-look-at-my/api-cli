package main

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestShellQuote(t *testing.T) {
	cases := []struct{ in, want string }{
		{"hello", `'hello'`},
		{"", `''`},
		{`it's`, `'it'\''s'`},
		{`$HOME`, `'$HOME'`},
	}
	for _, c := range cases {
		assert.Equal(t, c.want, shellQuote(c.in), c.in)
	}
}

func TestShellQuoteViaTemplate(t *testing.T) {
	got, err := renderString(`{{shellquote "it's"}}`, nil)
	require.NoError(t, err)
	assert.Equal(t, `'it'\''s'`, got)
}

func TestRenderEntry_StringsWalked(t *testing.T) {
	raw := json.RawMessage(`{"path":"/users/{{.arg.id}}","query":{"limit":"{{.flag.limit}}"}}`)
	data := map[string]any{
		"arg":  map[string]any{"id": 42},
		"flag": map[string]any{"limit": 10},
	}
	v, err := renderEntry(raw, data)
	require.NoError(t, err)
	m := v.(map[string]any)
	assert.Equal(t, "/users/42", m["path"])
	q := m["query"].(map[string]any)
	assert.Equal(t, "10", q["limit"])
}

func TestRenderEntry_LiteralTypesPreserved(t *testing.T) {
	raw := json.RawMessage(`{"n":42,"b":true,"arr":[1,"{{.arg.x}}",true]}`)
	data := map[string]any{"arg": map[string]any{"x": "hi"}}
	v, err := renderEntry(raw, data)
	require.NoError(t, err)
	m := v.(map[string]any)

	assert.Equal(t, json.Number("42"), m["n"])
	assert.Equal(t, true, m["b"])

	arr := m["arr"].([]any)
	assert.Equal(t, json.Number("1"), arr[0])
	assert.Equal(t, "hi", arr[1])
	assert.Equal(t, true, arr[2])
}

func TestRenderEntry_NullAndEmpty(t *testing.T) {
	v, err := renderEntry(nil, nil)
	require.NoError(t, err)
	assert.Nil(t, v)

	v, err = renderEntry(json.RawMessage(`null`), nil)
	require.NoError(t, err)
	assert.Nil(t, v)
}

func TestSpread_Empty(t *testing.T) {
	got, err := spread(nil)
	require.NoError(t, err)
	assert.Equal(t, spreadSentinel+spreadEndSentinel, got)

	got, err = spread([]string{})
	require.NoError(t, err)
	assert.Equal(t, spreadSentinel+spreadEndSentinel, got)
}

func TestSpread_StringSlice(t *testing.T) {
	got, err := spread([]string{"a", "b", "c"})
	require.NoError(t, err)
	assert.Equal(t, "\x00a\x00b\x00c\x01", got)
}

func TestSpread_AnySlice(t *testing.T) {
	got, err := spread([]any{"a", 1, true})
	require.NoError(t, err)
	assert.Equal(t, "\x00a\x001\x00true\x01", got)
}

func TestSpread_IntSlice(t *testing.T) {
	got, err := spread([]int{1, 2, 3})
	require.NoError(t, err)
	assert.Equal(t, "\x001\x002\x003\x01", got)
}

func TestSpread_RejectsNonSlice(t *testing.T) {
	_, err := spread("hello")
	assert.Error(t, err)
}

func TestSpread_RejectsSentinelBytes(t *testing.T) {
	_, err := spread([]string{"ok", "bad\x00val"})
	assert.Error(t, err)

	_, err = spread([]string{"bad\x01val"})
	assert.Error(t, err)
}

func TestSpreadViaTemplate(t *testing.T) {
	got, err := renderString(`{{spread .x}}`, map[string]any{"x": []string{"a", "b"}})
	require.NoError(t, err)
	assert.Equal(t, "\x00a\x00b\x01", got)
}

func TestFileExists(t *testing.T) {
	dir := t.TempDir()
	f := dir + "/file.txt"
	require.NoError(t, os.WriteFile(f, []byte("x"), 0o600))
	assert.True(t, fileExists(f))
	assert.False(t, fileExists(dir))             // directory, not file
	assert.False(t, fileExists(dir+"/nope.txt")) // missing
}

func TestDirExists(t *testing.T) {
	dir := t.TempDir()
	f := dir + "/file.txt"
	require.NoError(t, os.WriteFile(f, []byte("x"), 0o600))
	assert.True(t, dirExists(dir))
	assert.False(t, dirExists(f)) // file, not dir
	assert.False(t, dirExists(dir+"/nope"))
}

func TestFileExistsViaTemplate(t *testing.T) {
	dir := t.TempDir()
	f := dir + "/file.txt"
	require.NoError(t, os.WriteFile(f, []byte("x"), 0o600))
	got, err := renderString(`{{if fileExists .p}}YES{{else}}NO{{end}}`, map[string]any{"p": f})
	require.NoError(t, err)
	assert.Equal(t, "YES", got)

	got, err = renderString(`{{if dirExists .p}}YES{{else}}NO{{end}}`, map[string]any{"p": dir})
	require.NoError(t, err)
	assert.Equal(t, "YES", got)
}

func TestTabwriter_StringSlice(t *testing.T) {
	rows := []any{"ID\tNAME", "1\tAda", "42\tHopper"}
	got, err := renderString(`{{ tabwriter . }}`, rows)
	require.NoError(t, err)
	want := "ID  NAME\n1   Ada\n42  Hopper\n"
	assert.Equal(t, want, got)
}

func TestTabwriter_MalformedInput(t *testing.T) {
	_, err := renderString(`{{ tabwriter . }}`, 42)
	assert.Error(t, err)
}

func TestPadTemplateHelpers(t *testing.T) {
	got, err := renderString(`[{{ padRight 6 "ab" }}]`, nil)
	require.NoError(t, err)
	assert.Equal(t, "[ab    ]", got)

	got, err = renderString(`[{{ padLeft 6 "ab" }}]`, nil)
	require.NoError(t, err)
	assert.Equal(t, "[    ab]", got)
}

func TestDisplayWidthAndStripANSITemplateHelpers(t *testing.T) {
	got, err := renderString(`{{ displayWidth . }}`, "\x1b[31mhi\x1b[0m")
	require.NoError(t, err)
	assert.Equal(t, "2", got)

	got, err = renderString(`{{ stripANSI . }}`, "\x1b[31mhi\x1b[0m")
	require.NoError(t, err)
	assert.Equal(t, "hi", got)
}

func TestToRows_NilAndExoticShapes(t *testing.T) {
	rows, err := toRows(nil)
	assert.NoError(t, err)
	assert.Nil(t, rows)

	rows, err = toRows([][]string{{"a", "b"}, {"c", "d"}})
	require.NoError(t, err)
	assert.Equal(t, []string{"a\tb", "c\td"}, rows)

	rows, err = toRows([][]any{{"a", 1}, {"b", 2}})
	require.NoError(t, err)
	assert.Equal(t, []string{"a\t1", "b\t2"}, rows)

	rows, err = toRows([]any{[]any{"x", 1}, "y\tz"})
	require.NoError(t, err)
	assert.Equal(t, []string{"x\t1", "y\tz"}, rows)
}

func TestFilterSuffix(t *testing.T) {
	data := map[string]any{
		"items": []string{"foo.cpp1.ii", "bar.o", "baz.cpp1.ii", "other"},
	}
	got, err := renderString(`{{.items | filterSuffix ".cpp1.ii" | join ","}}`, data)
	require.NoError(t, err)
	assert.Equal(t, "foo.cpp1.ii,baz.cpp1.ii", got)

	got, err = renderString(`{{.items | filterSuffix ".cpp1.ii" | first}}`, data)
	require.NoError(t, err)
	assert.Equal(t, "foo.cpp1.ii", got)
}

func TestFilterPrefix(t *testing.T) {
	data := map[string]any{
		"items": []string{"--flag1", "pos", "--flag2", "-short"},
	}
	got, err := renderString(`{{.items | filterPrefix "--" | join ","}}`, data)
	require.NoError(t, err)
	assert.Equal(t, "--flag1,--flag2", got)
}
