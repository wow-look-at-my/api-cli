package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wow-look-at-my/tml/sema"
)

// The build-board case: a list call, one call per element for its log, and one
// card per build carrying attributes plus the tail of that log.
//
// Every server here closes through t.Cleanup rather than a defer, so it
// outlives the swapped client that points at it. A defer closes it first, and
// the global then names a dead server for as long as the restore takes.

const boardComponent = `<?xml version="1.1" encoding="UTF-8"?>
<Component xmlns="urn:tml:v1" name="Board">
	<Property name="heading" type="string" required="true"/>
	<Property name="builds" type="record[]" default=""/>

	<DataTemplate name="Build">
		<Property name="name" type="string" required="true"/>
		<Property name="status" type="string" default=""/>
		<Property name="stage" type="string" default=""/>
		<Property name="log" type="string[]" default=""/>
		<Template>
			<Stack orientation="vertical">
				<Stack orientation="horizontal" gap="1">
					<Text>{name}</Text>
					<Text>{status}</Text>
					<Text>{stage}</Text>
				</Stack>
				<For each="{log}" as="line"><Text>  {line}</Text></For>
			</Stack>
		</Template>
	</DataTemplate>

	<Template>
		<Stack orientation="vertical">
			<Text id="heading">{heading}</Text>
			<Stack id="builds" itemsSource="{builds}" itemTemplate="Build" orientation="vertical"/>
		</Stack>
	</Template>
</Component>
`

func writeBoardComponent(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "board.tml"), []byte(boardComponent), 0o600))
	return dir
}

// fakeCI answers a list call and a per-build detail call, in the shape a CI
// server takes: the list names the runs, and the detail carries the log as one
// entry per stage.
func fakeCI(t *testing.T, calls *[]string) *httptest.Server {
	t.Helper()
	logs := map[string][]map[string]any{
		"api": {
			{"stage": "clone", "stdout": "cloning\ndone\n"},
			{"stage": "docker build", "stdout": "step 1/9\nstep 2/9\nstep 3/9 RUN a very long command line that will not fit inside one card\n"},
		},
		"web": {{"stage": "clone", "stdout": "cloning\n"}},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		*calls = append(*calls, r.URL.Path+" "+strings.TrimSpace(string(body)))
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/detail") {
			var req struct {
				ID string `json:"id"`
			}
			_ = json.Unmarshal(body, &req)
			_ = json.NewEncoder(w).Encode(map[string]any{"logs": logs[req.ID]})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"updates": []any{
			map[string]any{"id": "api", "status": "InProgress"},
			map[string]any{"id": "web", "status": "InProgress"},
		}})
	}))
	t.Cleanup(srv.Close)
	return srv
}

func boardConfig(srv *httptest.Server, dir string) *Config {
	return &Config{
		Name: "ci",
		Dir:  dir,
		Request: &Request{
			Method: "POST",
			URL:    srv.URL + "/{{ .entry.call }}",
			Body:   `{{ .entry.params }}`,
		},
		Commands: []Command{{
			Name:  "builds",
			Entry: json.RawMessage(`{"call":"list","params":"{}"}`),
			Steps: []Step{
				{Name: "running", Entry: json.RawMessage(`{"call":"list","params":"{}"}`)},
				{
					Name:  "detail",
					Over:  "result.running.updates",
					Entry: json.RawMessage(`{"call":"detail","params":"{\"id\":{{ .item.id | toJson }}}"}`),
				},
			},
			TML: &TML{Src: "board.tml", Props: []TMLProp{
				{Name: "heading", Text: "{{ len .result.detail }} building"},
				{Name: "builds", Over: "result.detail", Fields: []TMLField{
					{Name: "name", Path: "item.id"},
					{Name: "status", Path: "item.status"},
					{Name: "stage", Expr: "{{ with .result.logs }}{{ (index . (sub (len .) 1)).stage }}{{ end }}"},
					{Name: "log", Expr: "{{ range .result.logs }}{{ .stdout }}{{ end }}", Lines: true, Last: "2", Truncate: "40"},
				}},
			}},
		}},
	}
}

func TestBuildBoard_OneCardPerBuildWithItsOwnLog(t *testing.T) {
	var calls []string
	srv := fakeCI(t, &calls)
	swapHTTPClient(t, srv)

	cfg := boardConfig(srv, writeBoardComponent(t))
	code, out, errOut := execCmdFull(t, cfg, "builds", "--format=always")
	require.Equal(t, 0, code, errOut)

	// The list once, then one detail call per element in the source order, then
	// the leaf's own call.
	require.Len(t, calls, 4)
	assert.Contains(t, calls[0], "/list")
	assert.Contains(t, calls[1], `{"id":"api"}`)
	assert.Contains(t, calls[2], `{"id":"web"}`)
	assert.Contains(t, calls[3], "/list")

	assert.Contains(t, out, "2 building")
	assert.Contains(t, out, "api")
	assert.Contains(t, out, "web")
	// The last stage of the build, not the first.
	assert.Contains(t, out, "docker build")
	// last="2" keeps the tail of the joined log and drops what came before.
	assert.Contains(t, out, "step 2/9")
	assert.NotContains(t, out, "step 1/9")
	// truncate="40" clips the long line to the card's width.
	assert.Contains(t, out, "…")
}

// A failing element fails the step: a board missing one build reads as a
// shorter queue rather than as a broken run.
func TestStepOver_FailingElementFailsTheRun(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/detail") {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"error":"gone"}`))
			return
		}
		_, _ = w.Write([]byte(`{"updates":[{"id":"api"}]}`))
	}))
	t.Cleanup(srv.Close)
	swapHTTPClient(t, srv)

	cfg := boardConfig(srv, writeBoardComponent(t))
	code, _, _ := execCmdFull(t, cfg, "builds", "--format=always")
	assert.NotEqual(t, 0, code)
}

// A repeated step says what it cannot repeat over, rather than running zero
// times and leaving an empty board that reads as an idle queue.
func TestStepOver_SaysWhatItCannotRepeat(t *testing.T) {
	step := []Step{{Name: "detail", Over: "result.running.updates", Command: &Cmd{Shell: true, Template: "true"}}}

	cases := map[string]struct {
		data map[string]any
		want string
	}{
		"missing": {map[string]any{"result": map[string]any{}}, "not in the context"},
		"scalar": {map[string]any{"result": map[string]any{
			"running": map[string]any{"updates": "not a list"},
		}}, "needs a list"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			oc, err := runSteps(step, tc.data, map[string]any{}, nil, nil, "", "", captureExec, execStderr)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
			assert.Equal(t, 0, oc.executions)
		})
	}
}

// The element rides in the context as .item, and the step's own result pairs
// each element with its response.
func TestStepOver_PairsEachElementWithItsResult(t *testing.T) {
	step := []Step{{
		Name:    "detail",
		Over:    "result.running",
		Command: &Cmd{Shell: true, Template: `printf '{"id":"%s","n":%d}' {{ .item.id }} {{ .index }}`},
	}}
	data := map[string]any{"result": map[string]any{"running": []any{
		map[string]any{"id": "api"},
		map[string]any{"id": "web"},
	}}}
	results := map[string]any{}

	oc, err := runSteps(step, data, results, nil, nil, "", "", captureExec, execStderr)
	require.NoError(t, err)
	assert.Equal(t, 2, oc.executions)

	pairs, ok := results["detail"].([]any)
	require.True(t, ok)
	require.Len(t, pairs, 2)
	first, _ := pairs[0].(map[string]any)
	assert.Equal(t, map[string]any{"id": "api"}, first["item"])
	assert.Equal(t, map[string]any{"id": "api", "n": int64(0)}, first["result"])
	second, _ := pairs[1].(map[string]any)
	assert.Equal(t, map[string]any{"id": "web", "n": int64(1)}, second["result"])

	// The binding is the step's, not the leaf's: it is gone afterwards.
	assert.NotContains(t, data, "item")
	assert.NotContains(t, data, "index")
}

func TestTMLLines_TailAndClip(t *testing.T) {
	assert.Equal(t, []string{"two", "three"}, valueStrings(tmlLines("one\ntwo\nthree\n", 2, 0)))
	assert.Equal(t, []string{"a very lo…"}, valueStrings(tmlLines("a very long line indeed\n", 0, 10)))
	assert.Empty(t, valueStrings(tmlLines("", 0, 0)))
	assert.Equal(t, []string{"kept"}, valueStrings(tmlLines("kept\n", 5, 0)))
}

func TestTruncateCells(t *testing.T) {
	assert.Equal(t, "abc", truncateCells("abc", 5))
	assert.Equal(t, "ab…", truncateCells("abcdef", 3))
	assert.Equal(t, "…", truncateCells("abcdef", 1))
	// A wide rune costs two columns, so fewer of them fit.
	assert.Equal(t, "日…", truncateCells("日本語です", 4))
}

func TestTMLExprData_ElementKeysWinOverTheBinding(t *testing.T) {
	element := map[string]any{"item": map[string]any{"id": "api"}, "result": map[string]any{"n": 1}}
	data := tmlExprData(element, 3, map[string]any{"var": map[string]any{"x": "y"}})
	assert.Equal(t, map[string]any{"id": "api"}, data["item"])
	assert.Equal(t, 3, data["index"])
	assert.Equal(t, map[string]any{"x": "y"}, data["var"])
}

// valueStrings reads a list value back as the strings it holds.
func valueStrings(v sema.Value) []string {
	out := make([]string, 0, len(v.Items()))
	for _, item := range v.Items() {
		out = append(out, item.String())
	}
	return out
}
