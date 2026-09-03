package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// recordingServer serves canned bodies per path and records the paths it saw.
func recordingServer(t *testing.T, bodies map[string]string) (*httptest.Server, func() []string) {
	t.Helper()
	var mu sync.Mutex
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen = append(seen, r.URL.RequestURI())
		mu.Unlock()
		body, ok := bodies[r.URL.Path]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv, func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), seen...)
	}
}

// A step with no <run> reuses the leaf's inherited request, varying only its
// entry — the pattern the README's steps example needed curl for.
func TestSteps_RequestInheritsLeafRequest(t *testing.T) {
	srv, seen := recordingServer(t, map[string]string{
		"/users":  `[{"id":7,"name":"ada"}]`,
		"/posts":  `[{"title":"first"},{"title":"second"}]`,
		"/lookup": `{}`,
	})
	swapHTTPClient(t, srv)

	cfg := &Config{
		Name:    "t",
		Vars:    map[string]any{"base": srv.URL},
		Request: &Request{Method: "GET", URL: "{{.var.base}}{{.entry.path}}", QueryFrom: "entry.query"},
		Commands: []Command{{
			Name: "posts",
			Args: []Arg{{Name: "username", Required: true}},
			Steps: []Step{{
				Name:  "user",
				Entry: json.RawMessage(`{"path":"/users","query":{"username":"{{.arg.username}}"}}`),
			}},
			Entry: json.RawMessage(`{"path":"/posts","query":{"userId":"{{(index .result.user 0).id}}"}}`),
		}},
	}

	code, out, errOut := execCmdFull(t, cfg, "posts", "ada")
	require.Equal(t, 0, code, errOut)
	assert.JSONEq(t, `[{"title":"first"},{"title":"second"}]`, out)
	assert.Equal(t, []string{"/users?username=ada", "/posts?userId=7"}, seen())
}

// A step may declare its own request while the leaf runs a command.
func TestSteps_RequestStepThenCommandLeaf(t *testing.T) {
	srv, _ := recordingServer(t, map[string]string{"/whoami": `{"login":"octo"}`})
	swapHTTPClient(t, srv)

	cfg := &Config{
		Name: "t",
		Commands: []Command{{
			Name: "greet",
			Steps: []Step{{
				Name:    "me",
				Request: &Request{Method: "GET", URL: srv.URL + "/whoami"},
			}},
			Command: &Cmd{Shell: true, Template: `printf 'hello %s' {{.result.me.login}}`},
		}},
	}
	code, out, errOut := execCmdFull(t, cfg, "greet")
	require.Equal(t, 0, code, errOut)
	assert.Equal(t, "hello octo", out)
	assert.Equal(t, "2 executions\n", errOut)
}

// A command step and a request step compose in one leaf, each seeing the
// other's result.
func TestSteps_MixedCommandAndRequest(t *testing.T) {
	srv, seen := recordingServer(t, map[string]string{"/things/9": `{"label":"nine"}`})
	swapHTTPClient(t, srv)

	cfg := &Config{
		Name: "t",
		Commands: []Command{{
			Name: "show",
			Steps: []Step{
				{Name: "id", Command: &Cmd{Shell: true, Template: `printf '9'`}},
				{Name: "thing", Request: &Request{Method: "GET", URL: srv.URL + "/things/{{.result.id}}"}},
			},
			Command: &Cmd{Shell: true, Template: `printf '%s' {{.result.thing.label}}`},
		}},
	}
	code, out, errOut := execCmdFull(t, cfg, "show")
	require.Equal(t, 0, code, errOut)
	assert.Equal(t, "nine", out)
	assert.Equal(t, []string{"/things/9"}, seen())
}

// A failing request step aborts the leaf, like a failing command step.
func TestSteps_FailingRequestAborts(t *testing.T) {
	srv, _ := recordingServer(t, map[string]string{})
	swapHTTPClient(t, srv)

	cfg := &Config{
		Name: "t",
		Commands: []Command{{
			Name: "show",
			Steps: []Step{
				{Name: "gone", Request: &Request{Method: "GET", URL: srv.URL + "/missing"}},
			},
			Command: &Cmd{Shell: true, Template: `printf 'should not run'`},
		}},
	}
	code, out, errOut := execCmdFull(t, cfg, "show")
	assert.Equal(t, 1, code)
	assert.Empty(t, out)
	assert.Contains(t, errOut, "HTTP 404")
}

// A step's when= gates a request step the same as a command step.
func TestSteps_RequestSkippedByWhen(t *testing.T) {
	srv, seen := recordingServer(t, map[string]string{"/x": `{}`})
	swapHTTPClient(t, srv)

	cfg := &Config{
		Name: "t",
		Commands: []Command{{
			Name:  "show",
			Flags: []Flag{{Name: "deep", Type: "bool"}},
			Steps: []Step{{
				Name:    "extra",
				When:    "{{.flag.deep}}",
				Request: &Request{Method: "GET", URL: srv.URL + "/x"},
			}},
			Command: &Cmd{Shell: true, Template: `printf 'done'`},
		}},
	}
	code, out, errOut := execCmdFull(t, cfg, "show")
	require.Equal(t, 0, code, errOut)
	assert.Equal(t, "done", out)
	assert.Empty(t, seen())
}

// Steps run through a transport too, and the MCP path runs the same loop.
func TestSteps_RequestThroughTransportInMCP(t *testing.T) {
	serial(t)
	cfg := &Config{
		Name: "t",
		Transports: map[string]*Transport{
			"fake": shellTransport("fake", `printf '{"url":"%s"}' "{{.request.url}}"`, true),
		},
		Request: &Request{Method: "GET", URL: "https://internal.example{{.entry.path}}"},
		Commands: []Command{{
			Name:  "chain",
			Steps: []Step{{Name: "first", Entry: json.RawMessage(`{"path":"/one"}`)}},
			Entry: json.RawMessage(`{"path":"/two"}`),
			Fields: &Fields{List: []Field{
				{Name: "step", Expr: "{{$.result.first.url}}"},
				{Name: "leaf", Path: "url"},
			}},
		}},
	}
	require.NoError(t, validate(cfg))
	installTransports(cfg)
	t.Cleanup(func() { installTransports(nil) })

	leaves := collectMCPLeaves(cfg.Commands, mcpInherit{vars: cfg.Vars, request: cfg.Request, formats: cfg.Formats})
	require.Len(t, leaves, 1)

	out, isErr := mcpExecLeaf(&leaves[0], map[string]any{})
	require.False(t, isErr, out)
	assert.Contains(t, out, "https://internal.example/one")
	assert.Contains(t, out, "https://internal.example/two")
}

func TestParseXML_StepWithRequest(t *testing.T) {
	cfg := mustParse(t, `<config name="x">
	<command name="c">
		<steps>
			<step name="user">
				<run><request method="POST"><url>https://api/x</url><body>hi</body></request></run>
			</step>
		</steps>
		<run>echo {{.result.user}}</run>
	</command>
</config>`)
	step := cfg.Commands[0].Steps[0]
	require.NotNil(t, step.Request)
	assert.Nil(t, step.Command)
	assert.Equal(t, "POST", step.Request.Method)
	assert.Equal(t, "https://api/x", step.Request.URL)
	assert.Equal(t, "hi", step.Request.Body)
}

func TestValidate_StepRequestNeedsURL(t *testing.T) {
	_, err := loadStr(t, `<config name="x">
	<command name="c">
		<steps><step name="s"><run><request method="GET"><body>x</body></request></run></step></steps>
		<run>echo hi</run>
	</command>
</config>`)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "<request> requires a <url>")
}

// A leaf whose only run is a request still reports a missing step run clearly.
func TestSteps_NoRunAvailable(t *testing.T) {
	cfg := &Config{
		Name: "t",
		Commands: []Command{{
			Name:    "x",
			Steps:   []Step{{Name: "orphan"}},
			Command: &Cmd{Shell: true, Template: "true"},
		}},
	}
	// Clear the leaf's own run after validation so the step has nothing to
	// inherit — the shape validation cannot reach.
	cfg.Commands[0].Command = nil
	var oc stepOutcome
	oc, err := runSteps(cfg.Commands[0].Steps, map[string]any{}, map[string]any{}, nil, nil, "", "", captureExec, execStderr)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no command or request available")
	assert.Equal(t, 0, oc.executions)
}
