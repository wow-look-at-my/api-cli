package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- jqProgram ---

func TestJQProgram_Empty(t *testing.T) {
	got, err := jqProgram("  ", map[string]any{})
	require.NoError(t, err)
	assert.Empty(t, got)
}

func TestJQProgram_TemplateSeesFlags(t *testing.T) {
	data := map[string]any{"flag": map[string]any{"limit": 3}}
	got, err := jqProgram(".[0:{{ .flag.limit }}]", data)
	require.NoError(t, err)
	assert.Equal(t, ".[0:3]", got)
}

func TestJQProgram_TemplateBadSyntax(t *testing.T) {
	_, err := jqProgram("{{ .flag.limit", map[string]any{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "render jq")
}

func TestJQProgram_ContextPath(t *testing.T) {
	data := map[string]any{"var": map[string]any{"filter": ".[0:2]"}}
	got, err := jqProgram("var.filter", data)
	require.NoError(t, err)
	assert.Equal(t, ".[0:2]", got)
}

func TestJQProgram_ContextPathMissing(t *testing.T) {
	_, err := jqProgram("var.nope", map[string]any{"var": map[string]any{}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "resolved to nothing")
}

func TestJQProgram_ContextPathNotAString(t *testing.T) {
	data := map[string]any{"var": map[string]any{"filter": []any{1, 2}}}
	_, err := jqProgram("var.filter", data)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not a jq program")
}

func TestJQProgram_LiteralProgram(t *testing.T) {
	got, err := jqProgram(".items | length", map[string]any{})
	require.NoError(t, err)
	assert.Equal(t, ".items | length", got)
}

// --- the flag-dependent jq program, end to end ---

// jqPostsServer serves eight posts, six of whose titles contain "qui". Six is
// past the default limit of 5, so a run that omits --limit proves the declared
// default reached the program.
func jqPostsServer(t *testing.T) *httptest.Server {
	t.Helper()
	posts := []map[string]any{
		{"id": 1, "title": "alpha"},
		{"id": 2, "title": "qui-1"},
		{"id": 3, "title": "qui-2"},
		{"id": 4, "title": "beta"},
		{"id": 5, "title": "qui-3"},
		{"id": 6, "title": "qui-4"},
		{"id": 7, "title": "qui-5"},
		{"id": 8, "title": "qui-6"},
	}
	body, err := json.Marshal(posts)
	require.NoError(t, err)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// jqPostsConfig declares the same leaf twice: `inline` writes the jq program in
// the attribute, `viavar` keeps it in a <var> and points jq= at that path. Both
// forms must see this invocation's flags.
func jqPostsConfig(url string) *Config {
	const program = `[.[] | select(.title | contains("{{ .flag.contains }}"))][0:{{ .flag.limit }}]`
	flags := []Flag{
		{Name: "contains", Type: "string"},
		{Name: "limit", Type: "int", Default: float64(5)},
	}
	return &Config{
		Name: "t",
		Vars: map[string]any{"filter": program},
		Commands: []Command{
			{
				Name:    "inline",
				Flags:   flags,
				Request: &Request{Method: "GET", URL: url + "/posts", Response: &Response{JQ: program}},
			},
			{
				Name:    "viavar",
				Flags:   flags,
				Request: &Request{Method: "GET", URL: url + "/posts", Response: &Response{JQ: "var.filter"}},
			},
		},
	}
}

// jqTitles runs a leaf with --format=raw and decodes the titles it printed.
// --format=raw is the jq-shaped body, not the pre-jq one, which is what makes
// the shaping observable without a <fields> declaration.
func jqTitles(t *testing.T, cfg *Config, argv ...string) []string {
	t.Helper()
	code, out, errOut := execCmdFull(t, cfg, append(argv, "--format=raw")...)
	require.Equal(t, 0, code, "stderr: %s", errOut)

	var records []struct {
		Title string `json:"title"`
	}
	require.NoError(t, json.Unmarshal([]byte(out), &records), "output: %s", out)
	titles := make([]string, len(records))
	for i, r := range records {
		titles[i] = r.Title
	}
	return titles
}

func TestIntegration_JQSeesFlags(t *testing.T) {
	srv := jqPostsServer(t)
	swapHTTPClient(t, srv)
	cfg := jqPostsConfig(srv.URL)

	for _, leaf := range []string{"inline", "viavar"} {
		t.Run(leaf, func(t *testing.T) {
			assert.Equal(t, []string{"qui-1", "qui-2"},
				jqTitles(t, cfg, leaf, "--limit", "2", "--contains", "qui"))

			// No --limit: the flag's declared default of 5 caps the six matches.
			assert.Equal(t, []string{"qui-1", "qui-2", "qui-3", "qui-4", "qui-5"},
				jqTitles(t, cfg, leaf, "--contains", "qui"))

			// No --contains: nothing is filtered out, still capped by the limit.
			assert.Equal(t, []string{"alpha", "qui-1", "qui-2", "beta"},
				jqTitles(t, cfg, leaf, "--limit", "4"))
		})
	}
}

func TestIntegration_JQStaticVarStillWorks(t *testing.T) {
	srv := jqPostsServer(t)
	swapHTTPClient(t, srv)

	cfg := &Config{
		Name: "t",
		Vars: map[string]any{"filter": ".[0:2]"},
		Commands: []Command{{
			Name:    "posts",
			Request: &Request{Method: "GET", URL: srv.URL + "/posts", Response: &Response{JQ: "var.filter"}},
		}},
	}
	assert.Equal(t, []string{"alpha", "qui-1"}, jqTitles(t, cfg, "posts"))
}

func TestIntegration_JQMissingVarStillFails(t *testing.T) {
	srv := jqPostsServer(t)
	swapHTTPClient(t, srv)

	cfg := &Config{
		Name: "t",
		Commands: []Command{{
			Name:    "posts",
			Request: &Request{Method: "GET", URL: srv.URL + "/posts", Response: &Response{JQ: "var.absent"}},
		}},
	}
	code, _, errOut := execCmdFull(t, cfg, "posts")
	assert.Equal(t, 1, code)
	assert.Contains(t, errOut, "resolved to nothing")
}

func TestMcpExecLeaf_JQSeesFlags(t *testing.T) {
	srv := jqPostsServer(t)
	swapHTTPClient(t, srv)

	cfg := jqPostsConfig(srv.URL)
	leaves := collectMCPLeaves(cfg.Commands, mcpInherit{vars: cfg.Vars})
	require.Len(t, leaves, 2)

	for i := range leaves {
		leaf := &leaves[i]
		out, isErr := mcpExecLeaf(leaf, map[string]any{"contains": "qui", "limit": float64(2)})
		require.False(t, isErr, "%s: %s", leaf.name, out)
		assert.Contains(t, out, "qui-1")
		assert.Contains(t, out, "qui-2")
		assert.NotContains(t, out, "qui-3")
	}
}

// --- vars see this invocation's flags, not just jq ---

func TestIntegration_VarSeesFlag(t *testing.T) {
	cfg := &Config{
		Name:    "t",
		Vars:    map[string]any{"greeting": "hello {{ .flag.who }}"},
		Command: &Cmd{Shell: true, Template: `printf '%s' {{.var.greeting | shellquote}}`},
		Commands: []Command{{
			Name:  "greet",
			Flags: []Flag{{Name: "who", Type: "string", Default: "world"}},
		}},
	}

	code, out := execCmd(t, cfg, "greet")
	require.Equal(t, 0, code)
	assert.Equal(t, "hello world", out)

	code, out = execCmd(t, cfg, "greet", "--who", "matt")
	require.Equal(t, 0, code)
	assert.Equal(t, "hello matt", out)
}

// A templated flag default reads .var, and a var reads .flag. Both hold at
// once: the default renders against the flag-blind pass, the var against the
// finished flag map.
func TestIntegration_TemplatedFlagDefaultAndVarFlagReference(t *testing.T) {
	cfg := &Config{
		Name:    "t",
		Vars:    map[string]any{"base": "abc", "echo": "{{ .flag.name }}"},
		Command: &Cmd{Shell: true, Template: `printf '%s/%s' {{.flag.name | shellquote}} {{.var.echo | shellquote}}`},
		Commands: []Command{{
			Name:  "show",
			Flags: []Flag{{Name: "name", Type: "string", Default: "{{ .var.base }}-x"}},
		}},
	}

	code, out := execCmd(t, cfg, "show")
	require.Equal(t, 0, code)
	assert.Equal(t, "abc-x/abc-x", out)
}
