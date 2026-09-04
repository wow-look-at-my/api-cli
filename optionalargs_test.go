package main

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGatherArgs_OmittedOptionalIsTheZeroValue(t *testing.T) {
	node := Command{Args: []Arg{
		{Name: "id", Required: true},
		{Name: "name"},
		{Name: "count", Type: "int"},
	}}
	got, err := gatherArgs(node, []string{"7"})
	require.NoError(t, err)
	assert.Equal(t, map[string]any{"id": "7", "name": "", "count": 0}, got)
}

func TestMCPGatherArgs_OmittedOptionalIsTheZeroValue(t *testing.T) {
	node := Command{Args: []Arg{{Name: "id"}, {Name: "count", Type: "int"}, {Name: "rest", Variadic: true}}}
	got, err := mcpGatherArgs(node, map[string]any{})
	require.NoError(t, err)
	assert.Equal(t, map[string]any{"id": "", "count": 0, "rest": []string{}}, got)
}

// TestOptionalArg_UrlpathOnAnOmittedArg is the failure this zero value removes:
// urlpath takes a string, and the omitted arg used to reach it as nil.
func TestOptionalArg_UrlpathOnAnOmittedArg(t *testing.T) {
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.URL.Path)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	t.Cleanup(srv.Close)
	swapHTTPClient(t, srv)

	cfg := &Config{
		Name: "t",
		Commands: []Command{{
			Name:    "user",
			Args:    []Arg{{Name: "name"}},
			Request: &Request{Method: "GET", URL: srv.URL + "/users/{{ urlpath .arg.name }}"},
		}},
	}

	code, out := execCmd(t, cfg, "user")
	require.Equal(t, 0, code)
	assert.Contains(t, out, "ok")
	assert.Equal(t, []string{"/users/"}, seen)
}

// TestStepWhen_GuardsTheStepRender proves when= runs before the step's own
// request renders, so a step that must not run cannot fail on a missing arg.
func TestStepWhen_GuardsTheStepRender(t *testing.T) {
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.URL.Path)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	t.Cleanup(srv.Close)
	swapHTTPClient(t, srv)

	cfg := &Config{
		Name: "t",
		Commands: []Command{{
			Name: "thing",
			Args: []Arg{{Name: "id"}},
			Steps: []Step{{
				Name:    "detail",
				When:    "{{ .arg.id }}",
				Request: &Request{Method: "GET", URL: srv.URL + "/detail/{{ urlpath .arg.id }}"},
			}},
			Request: &Request{Method: "GET", URL: srv.URL + "/list"},
		}},
	}

	code, _ := execCmd(t, cfg, "thing")
	require.Equal(t, 0, code)
	assert.Equal(t, []string{"/list"}, seen)

	seen = nil
	code, _ = execCmd(t, cfg, "thing", "9")
	require.Equal(t, 0, code)
	assert.Equal(t, []string{"/detail/9", "/list"}, seen)
}

func TestValidate_PreconditionCannotReadResult(t *testing.T) {
	cfg := &Config{
		Name: "t",
		Commands: []Command{{
			Name:          "c",
			Preconditions: []string{`{{ range .result.list }}{{ end }}`},
			Command:       &Cmd{Shell: true, Template: "true"},
		}},
	}
	err := validate(cfg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "runs before <steps>")
}
