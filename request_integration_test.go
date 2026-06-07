package main

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIntegration_RequestFieldsTable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/repos", r.URL.Path)
		_, _ = w.Write([]byte(`[{"login":"a","stars":10},{"login":"bb","stars":2}]`))
	}))
	defer srv.Close()
	swapHTTPClient(t, srv)

	cfg := &Config{
		Name:    "t",
		Request: &Request{Method: "GET", URL: srv.URL + "/repos"},
		Commands: []Command{{
			Name:   "list",
			Fields: &Fields{List: []Field{{Name: "login", Path: "login"}, {Name: "stars", Path: "stars"}}},
		}},
	}
	code, out := execCmd(t, cfg, "list")
	require.Equal(t, 0, code)
	assert.Contains(t, out, "login  stars")
	assert.Contains(t, out, "a      10")
	assert.Contains(t, out, "bb     2")
}

func TestIntegration_RequestJQAndFooter(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"total_count":42,"items":[{"name":"x","html_url":"drop"}]}`))
	}))
	defer srv.Close()
	swapHTTPClient(t, srv)

	cfg := &Config{
		Name: "t",
		Vars: map[string]any{"filter": `walk(if type == "object" then with_entries(select(.key | endswith("url") | not)) else . end)`},
		Request: &Request{
			Method:   "GET",
			URL:      srv.URL + "/search",
			Response: &Response{JQ: "var.filter"},
		},
		Commands: []Command{{
			Name: "search",
			Fields: &Fields{
				Over:   "data.items",
				Footer: "{{.data.total_count}} total",
				List:   []Field{{Name: "name", Path: "name"}},
			},
		}},
	}
	code, out := execCmd(t, cfg, "search")
	require.Equal(t, 0, code)
	assert.Contains(t, out, "name")
	assert.Contains(t, out, "x")
	assert.Contains(t, out, "42 total")
}

func TestIntegration_RequestRawStreaming(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("raw diff body"))
	}))
	defer srv.Close()
	swapHTTPClient(t, srv)

	cfg := &Config{
		Name:     "t",
		Commands: []Command{{Name: "diff", Request: &Request{Method: "GET", URL: srv.URL + "/diff"}}},
	}
	code, out := execCmd(t, cfg, "diff")
	require.Equal(t, 0, code)
	assert.Equal(t, "raw diff body\n", out)
}

func TestIntegration_RequestNoFormatGivesRaw(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`[{"login":"a"}]`))
	}))
	defer srv.Close()
	swapHTTPClient(t, srv)

	cfg := &Config{
		Name:    "t",
		Request: &Request{Method: "GET", URL: srv.URL + "/x"},
		Commands: []Command{{
			Name:   "list",
			Fields: &Fields{List: []Field{{Name: "login", Path: "login"}}},
		}},
	}
	code, out := execCmd(t, cfg, "list", "--no-format")
	require.Equal(t, 0, code)
	assert.Contains(t, out, `[{"login":"a"}]`) // raw body verbatim, not a table
}

func TestIntegration_RequestAsJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`[{"login":"a","extra":"drop"}]`))
	}))
	defer srv.Close()
	swapHTTPClient(t, srv)

	cfg := &Config{
		Name:    "t",
		Request: &Request{Method: "GET", URL: srv.URL + "/x"},
		Commands: []Command{{
			Name:   "list",
			Fields: &Fields{List: []Field{{Name: "login", Path: "login"}}},
		}},
	}
	code, out := execCmd(t, cfg, "list", "--as", "json")
	require.Equal(t, 0, code)
	assert.Contains(t, out, `"login": "a"`)
	assert.NotContains(t, out, "drop") // projected to declared fields
}

func TestIntegration_RequestErrorStatusExitCode(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	swapHTTPClient(t, srv)

	cfg := &Config{
		Name:     "t",
		Commands: []Command{{Name: "go", Request: &Request{Method: "GET", URL: srv.URL + "/x"}}},
	}
	code, _ := execCmd(t, cfg, "go")
	assert.NotEqual(t, 0, code)
}

func TestMCP_RequestLeafWithFields(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"login":"octo","id":1}`))
	}))
	defer srv.Close()
	swapHTTPClient(t, srv)

	leaf := &mcpLeaf{
		name: "get",
		node: Command{
			Name:   "get",
			Fields: &Fields{List: []Field{{Name: "login", Path: "login"}, {Name: "id", Path: "id"}}},
		},
		request: &Request{Method: "GET", URL: srv.URL + "/user"},
		vars:    map[string]any{},
	}
	out, isErr := mcpExecLeaf(leaf, map[string]any{})
	require.False(t, isErr)
	assert.Contains(t, out, "login: octo")
	assert.Contains(t, out, "id:    1")
}

func TestMCP_RequestLeafError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	swapHTTPClient(t, srv)
	srv.Close()

	leaf := &mcpLeaf{
		name:    "get",
		node:    Command{Name: "get"},
		request: &Request{Method: "GET", URL: srv.URL + "/user"},
		vars:    map[string]any{},
	}
	_, isErr := mcpExecLeaf(leaf, map[string]any{})
	assert.True(t, isErr)
}
