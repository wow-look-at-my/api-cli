package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"text/template"
)

// envMap returns the process environment as a map[string]string, for use as
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

// renderString executes a text/template against data. Missing keys are
// treated as errors (rather than rendering "<no value>") so typos fail loudly.
func renderString(tmpl string, data map[string]any) (string, error) {
	t, err := template.New("t").Option("missingkey=error").Parse(tmpl)
	if err != nil {
		return "", fmt.Errorf("parse template %q: %w", tmpl, err)
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("execute template %q: %w", tmpl, err)
	}
	return buf.String(), nil
}

// renderMap renders every value in m as a template. Keys are never rendered.
// Returns a fresh map; the input is not modified.
func renderMap(m map[string]string, data map[string]any) (map[string]string, error) {
	if m == nil {
		return nil, nil
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		rv, err := renderString(v, data)
		if err != nil {
			return nil, fmt.Errorf("render %q: %w", k, err)
		}
		out[k] = rv
	}
	return out, nil
}

// renderBody walks the parsed JSON body, rendering every string leaf as a
// template. Object keys, numbers, booleans, and nulls pass through unchanged.
// Uses json.Number to preserve integer precision.
//
// Returns nil if the input is empty or a JSON null.
func renderBody(raw json.RawMessage, data map[string]any) ([]byte, error) {
	if len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil, nil
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, fmt.Errorf("parse body: %w", err)
	}
	rendered, err := walkBody(v, data)
	if err != nil {
		return nil, err
	}
	return json.Marshal(rendered)
}

func walkBody(v any, data map[string]any) (any, error) {
	switch x := v.(type) {
	case string:
		return renderString(x, data)
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, vv := range x {
			r, err := walkBody(vv, data)
			if err != nil {
				return nil, fmt.Errorf("at key %q: %w", k, err)
			}
			out[k] = r
		}
		return out, nil
	case []any:
		out := make([]any, len(x))
		for i, vv := range x {
			r, err := walkBody(vv, data)
			if err != nil {
				return nil, fmt.Errorf("at index %d: %w", i, err)
			}
			out[i] = r
		}
		return out, nil
	default:
		// json.Number, bool, nil — pass through.
		return v, nil
	}
}
