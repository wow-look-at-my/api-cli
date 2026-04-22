package main

import (
	"encoding/json"
	"testing"

	"github.com/wow-look-at-my/testify/assert"
	"github.com/wow-look-at-my/testify/require"
)

func TestRenderString(t *testing.T) {
	data := map[string]any{
		"id":	42,
		"name":	"ada",
		"env":	map[string]string{"TOKEN": "abc"},
	}
	cases := []struct {
		in, want string
	}{
		{"{{.id}}", "42"},
		{"/users/{{.id}}", "/users/42"},
		{"hello {{.name}}", "hello ada"},
		{"Bearer {{.env.TOKEN}}", "Bearer abc"},
		{"literal only", "literal only"},
	}
	for _, c := range cases {
		got, err := renderString(c.in, data)
		assert.Nil(t, err)

		assert.Equal(t, c.want, got)

	}
}

func TestRenderString_MissingKeyErrors(t *testing.T) {
	_, err := renderString("{{.typo}}", map[string]any{"id": 1})
	require.NotNil(t, err)

	assert.Contains(t, err.Error(), "typo")

}

func TestRenderMap(t *testing.T) {
	data := map[string]any{"a": "A", "b": "B"}
	in := map[string]string{"x": "{{.a}}", "y": "{{.b}}!"}
	got, err := renderMap(in, data)
	require.Nil(t, err)

	assert.False(t, got["x"] != "A" || got["y"] != "B!")

}

func TestRenderMap_Nil(t *testing.T) {
	got, err := renderMap(nil, map[string]any{})
	require.Nil(t, err)

	assert.Nil(t, got)

}

func TestRenderBody_Null(t *testing.T) {
	out, err := renderBody(json.RawMessage(`null`), map[string]any{})
	require.Nil(t, err)

	assert.Nil(t, out)

}

func TestRenderBody_Empty(t *testing.T) {
	out, err := renderBody(nil, map[string]any{})
	require.Nil(t, err)

	assert.Nil(t, out)

}

func TestRenderBody_StringsAndLiterals(t *testing.T) {
	// Mixed string-templated leaves with literal numbers/bools. Number and
	// bool types must pass through untouched.
	raw := json.RawMessage(`{"title":"{{.title}}","userId":1,"active":true,"tags":null}`)
	data := map[string]any{"title": "hi"}
	out, err := renderBody(raw, data)
	require.Nil(t, err)

	var got map[string]any
	require.NoError(t, json.Unmarshal(out, &got))

	assert.Equal(t, "hi", got["title"])

	assert.Equal(t, float64(1), got["userId"])

	assert.Equal(t, true, got["active"])

	assert.Nil(t, got["tags"])

}

func TestRenderBody_NestedAndArrays(t *testing.T) {
	raw := json.RawMessage(`{"user":{"name":"{{.n}}","tags":["a","{{.t}}"]}}`)
	data := map[string]any{"n": "ada", "t": "admin"}
	out, err := renderBody(raw, data)
	require.Nil(t, err)

	var got map[string]any
	require.NoError(t, json.Unmarshal(out, &got))

	u := got["user"].(map[string]any)
	assert.Equal(t, "ada", u["name"])

	tags := u["tags"].([]any)
	assert.False(t, tags[0] != "a" || tags[1] != "admin")

}

func TestRenderBody_QuoteEscaping(t *testing.T) {
	// A templated string containing a JSON-special character must come out
	// cleanly escaped in the outgoing JSON.
	raw := json.RawMessage(`{"msg":"{{.m}}"}`)
	data := map[string]any{"m": `hello "world"`}
	out, err := renderBody(raw, data)
	require.Nil(t, err)

	var got map[string]any
	require.NoError(t, json.Unmarshal(out, &got))

	assert.Equal(t, `hello "world"`, got["msg"])

}
