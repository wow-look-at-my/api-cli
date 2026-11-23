package main

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEvalPredicate(t *testing.T) {
	cases := []struct {
		name string
		tmpl string
		data map[string]any
		want bool
	}{
		{"present key", "{{ .data.items }}", map[string]any{"data": map[string]any{"items": []any{1}}}, true},
		{"missing key", "{{ .data.items }}", map[string]any{"data": map[string]any{"name": "x"}}, false},
		{"missing parent", "{{ .data.items.count }}", map[string]any{}, false},
		{"field on a string", "{{ .data.items }}", map[string]any{"data": "<html>502</html>"}, false},
		{"field on a number", "{{ .data.items }}", map[string]any{"data": int64(3)}, false},
		{"field on a list", "{{ .data.items }}", map[string]any{"data": []any{"a"}}, false},
		{"not on a string", "{{ not .data.error }}", map[string]any{"data": "plain"}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := evalPredicate(c.tmpl, c.data)
			require.NoError(t, err)
			assert.Equal(t, c.want, got)
		})
	}
}

func TestEvalPredicate_OtherErrorsFail(t *testing.T) {
	_, err := evalPredicate("{{ .data.items", map[string]any{})
	require.Error(t, err, "a parse error is a broken config, not a false predicate")

	_, err = evalPredicate(`{{ fail "boom" }}`, map[string]any{})
	require.ErrorContains(t, err, "boom")
}

// A plain-text body matches no shape block, so the body prints as it came.
func TestFieldsBlocks_TextBodyMatchesNoBlock(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("upstream is down"))
	}))
	t.Cleanup(srv.Close)
	swapHTTPClient(t, srv)
	cfg := &Config{
		Name: "t",
		Commands: []Command{{
			Name:    "ls",
			Request: &Request{Method: "GET", URL: srv.URL},
			Fields: []FieldsBlock{
				{When: "{{ .data.children }}", Fields: &Fields{Over: "children", List: []Field{{Name: "name", Path: "name"}}}},
				{When: "{{ .data.error }}", Fields: &Fields{List: []Field{{Name: "error", Path: "error"}}}},
			},
		}},
	}
	code, out := execCmd(t, cfg, "ls", "--format=always")
	require.Equal(t, 0, code, out)
	assert.Contains(t, out, "upstream is down")
}

// A JSON body without the key the block tests skips that block.
func TestFieldsBlocks_MissingKeyIsFalse(t *testing.T) {
	srv := jsonServer(t, `{"error":"not found"}`)
	cfg := &Config{
		Name: "t",
		Commands: []Command{{
			Name:    "ls",
			Request: &Request{Method: "GET", URL: srv.URL},
			Fields: []FieldsBlock{
				{When: "{{ .data.children }}", Fields: &Fields{Over: "children", List: []Field{{Name: "name", Path: "name"}}}},
				{When: "{{ .data.error }}", Fields: &Fields{List: []Field{{Name: "error", Path: "error"}}}},
			},
		}},
	}
	code, out := execCmd(t, cfg, "ls", "--format=always")
	require.Equal(t, 0, code, out)
	assert.Contains(t, out, "not found")
	assert.NotContains(t, out, "resolved to nothing")
}
