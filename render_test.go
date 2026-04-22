package main

import (
	"encoding/json"
	"sort"
	"strings"
	"testing"

	"github.com/wow-look-at-my/testify/assert"
	"github.com/wow-look-at-my/testify/require"
)

func TestRenderString_Namespaces(t *testing.T) {
	data := map[string]any{
		"arg":   map[string]any{"id": 42, "name": "ada"},
		"flag":  map[string]any{"limit": 10, "verbose": true},
		"env":   map[string]string{"TOKEN": "abc"},
		"var":   map[string]any{"base_url": "https://x.example"},
		"entry": map[string]any{"path": "/users/42"},
	}
	cases := []struct{ in, want string }{
		{"{{.arg.id}}", "42"},
		{"{{.arg.name}}", "ada"},
		{"{{.flag.limit}}", "10"},
		{"{{.env.TOKEN}}", "abc"},
		{"{{.var.base_url}}{{.entry.path}}", "https://x.example/users/42"},
		{"{{if .flag.verbose}}VERBOSE{{end}}", "VERBOSE"},
		{"literal only", "literal only"},
	}
	for _, c := range cases {
		got, err := renderString(c.in, data)
		assert.NoError(t, err, c.in)
		assert.Equal(t, c.want, got, c.in)
	}
}

func TestRenderString_MissingKeyZero(t *testing.T) {
	// Under missingkey=zero a missing key in a map[string]interface{} yields
	// a nil value, which renders as "<no value>" — not ideal but it doesn't
	// error. `default` collapses it to a fallback; `required` upgrades it
	// to an error. Both are available via sprig.
	data := map[string]any{"arg": map[string]any{"id": 1}}

	// `if` treats nil as falsy, so conditionals work naturally.
	got, err := renderString(`{{if .arg.tpyo}}have{{else}}missing{{end}}`, data)
	require.NoError(t, err)
	assert.Equal(t, "missing", got)

	// `default` substitutes a fallback when the value is nil/zero/empty.
	got, err = renderString(`{{.arg.tpyo | default "fallback"}}`, data)
	require.NoError(t, err)
	assert.Equal(t, "fallback", got)

	// `required` is the opt-in strict mode.
	_, err = renderString(`{{required "arg.tpyo must be set" .arg.tpyo}}`, data)
	assert.Error(t, err)

	// With a typed string map, missing keys do render as "" as expected.
	got, err = renderString(`x{{.env.DEFINITELY_UNSET_VAR_12345}}x`, map[string]any{"env": map[string]string{}})
	require.NoError(t, err)
	assert.Equal(t, "xx", got)
}

func TestSprigHelpersAvailable(t *testing.T) {
	// Spot-check: sprig helpers (toJson, default, upper) are wired in.
	got, err := renderString(`{{upper "hi"}}`, nil)
	require.NoError(t, err)
	assert.Equal(t, "HI", got)

	got, err = renderString(`{{default "fallback" .missing}}`, map[string]any{})
	require.NoError(t, err)
	assert.Equal(t, "fallback", got)

	got, err = renderString(`{{.x | toJson}}`, map[string]any{"x": map[string]any{"a": 1}})
	require.NoError(t, err)
	assert.Equal(t, `{"a":1}`, got)
}

func TestQueryString_EmptyIsBlank(t *testing.T) {
	got, err := queryString(nil)
	require.NoError(t, err)
	assert.Equal(t, "", got)

	got, err = queryString(map[string]any{})
	require.NoError(t, err)
	assert.Equal(t, "", got)
}

func TestQueryString_MapStringAny(t *testing.T) {
	got, err := queryString(map[string]any{
		"a":  "1",
		"b":  "2",
		"no": "",
	})
	require.NoError(t, err)
	// url.Values.Encode sorts keys deterministically.
	assert.Equal(t, "?a=1&b=2", got)
}

func TestQueryString_MapStringString(t *testing.T) {
	got, err := queryString(map[string]string{"a": "1", "skip": ""})
	require.NoError(t, err)
	assert.Equal(t, "?a=1", got)
}

func TestQueryString_SliceValuesRepeat(t *testing.T) {
	got, err := queryString(map[string]any{
		"tag": []any{"a", "b"},
	})
	require.NoError(t, err)
	assert.Equal(t, "?tag=a&tag=b", got)
}

func TestQueryString_MixedScalars(t *testing.T) {
	got, err := queryString(map[string]any{
		"n":   json.Number("42"),
		"b":   true,
		"nil": nil,
	})
	require.NoError(t, err)
	parts := strings.Split(strings.TrimPrefix(got, "?"), "&")
	sort.Strings(parts)
	assert.Equal(t, []string{"b=true", "n=42"}, parts)
}

func TestQueryString_RejectsNonMap(t *testing.T) {
	_, err := queryString("hello")
	assert.Error(t, err)
}

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

func TestUrlpathViaTemplate(t *testing.T) {
	got, err := renderString(`{{urlpath "a/b c"}}`, nil)
	require.NoError(t, err)
	// "/" stays, space becomes %20.
	assert.Contains(t, got, "%20")
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

func TestMergeVars(t *testing.T) {
	parent := map[string]any{"a": "1", "b": "2"}
	child := map[string]any{"b": "override", "c": "3"}
	got := mergeVars(parent, child)
	assert.Equal(t, "1", got["a"])
	assert.Equal(t, "override", got["b"])
	assert.Equal(t, "3", got["c"])
	// Parent not mutated.
	assert.Equal(t, "2", parent["b"])
}

func TestEnvMap(t *testing.T) {
	m := envMap()
	assert.NotEmpty(t, m["PATH"])
}
