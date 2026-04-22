package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRenderString(t *testing.T) {
	data := map[string]any{
		"id":   42,
		"name": "ada",
		"env":  map[string]string{"TOKEN": "abc"},
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
		if err != nil {
			t.Errorf("renderString(%q) error: %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("renderString(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestRenderString_MissingKeyErrors(t *testing.T) {
	_, err := renderString("{{.typo}}", map[string]any{"id": 1})
	if err == nil {
		t.Fatal("expected error for missing key, got nil")
	}
	if !strings.Contains(err.Error(), "typo") {
		t.Errorf("error should name the missing key, got: %v", err)
	}
}

func TestRenderMap(t *testing.T) {
	data := map[string]any{"a": "A", "b": "B"}
	in := map[string]string{"x": "{{.a}}", "y": "{{.b}}!"}
	got, err := renderMap(in, data)
	if err != nil {
		t.Fatal(err)
	}
	if got["x"] != "A" || got["y"] != "B!" {
		t.Errorf("got %v", got)
	}
}

func TestRenderMap_Nil(t *testing.T) {
	got, err := renderMap(nil, map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Errorf("expected nil, got %v", got)
	}
}

func TestRenderBody_Null(t *testing.T) {
	out, err := renderBody(json.RawMessage(`null`), map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	if out != nil {
		t.Errorf("expected nil bytes, got %q", out)
	}
}

func TestRenderBody_Empty(t *testing.T) {
	out, err := renderBody(nil, map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	if out != nil {
		t.Errorf("expected nil bytes, got %q", out)
	}
}

func TestRenderBody_StringsAndLiterals(t *testing.T) {
	// Mixed string-templated leaves with literal numbers/bools. Number and
	// bool types must pass through untouched.
	raw := json.RawMessage(`{"title":"{{.title}}","userId":1,"active":true,"tags":null}`)
	data := map[string]any{"title": "hi"}
	out, err := renderBody(raw, data)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("output invalid JSON: %v (%s)", err, out)
	}
	if got["title"] != "hi" {
		t.Errorf("title = %v", got["title"])
	}
	if got["userId"] != float64(1) {
		t.Errorf("userId = %v (%T), want 1", got["userId"], got["userId"])
	}
	if got["active"] != true {
		t.Errorf("active = %v", got["active"])
	}
	if got["tags"] != nil {
		t.Errorf("tags = %v, want nil", got["tags"])
	}
}

func TestRenderBody_NestedAndArrays(t *testing.T) {
	raw := json.RawMessage(`{"user":{"name":"{{.n}}","tags":["a","{{.t}}"]}}`)
	data := map[string]any{"n": "ada", "t": "admin"}
	out, err := renderBody(raw, data)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatal(err)
	}
	u := got["user"].(map[string]any)
	if u["name"] != "ada" {
		t.Errorf("name = %v", u["name"])
	}
	tags := u["tags"].([]any)
	if tags[0] != "a" || tags[1] != "admin" {
		t.Errorf("tags = %v", tags)
	}
}

func TestRenderBody_QuoteEscaping(t *testing.T) {
	// A templated string containing a JSON-special character must come out
	// cleanly escaped in the outgoing JSON.
	raw := json.RawMessage(`{"msg":"{{.m}}"}`)
	data := map[string]any{"m": `hello "world"`}
	out, err := renderBody(raw, data)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("output invalid JSON: %v (%s)", err, out)
	}
	if got["msg"] != `hello "world"` {
		t.Errorf("msg = %v", got["msg"])
	}
}
