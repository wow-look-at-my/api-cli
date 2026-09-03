package fields

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// probeBody is the shape that made these bugs visible: a list of records under
// a top-level key, each with a nested object.
const probeBody = `{"count":3,"response":[` +
	`{"id":1,"name":"alpha","detail":{"status":"ok"}},` +
	`{"id":2,"name":"beta","detail":{"status":"bad"}},` +
	`{"id":3,"name":"gamma","detail":{"status":"ok"}}]}`

func TestLookupData_IndexesLists(t *testing.T) {
	data := parseResult(probeBody)

	v, ok := lookupData(data, "response.0.name")
	require.True(t, ok)
	assert.Equal(t, "alpha", v)

	v, ok = lookupData(data, "response.2.detail.status")
	require.True(t, ok)
	assert.Equal(t, "ok", v)

	v, ok = lookupData(data, "count")
	require.True(t, ok)
	assert.Equal(t, int64(3), v)
}

func TestLookupData_MissingIsNotFound(t *testing.T) {
	data := parseResult(probeBody)

	for _, path := range []string{"response.9.name", "response.-1.name", "response.first", "nope", "count.0"} {
		_, ok := lookupData(data, path)
		assert.False(t, ok, "path %q should not resolve", path)
	}
}

// A key that is present with a null value resolves. Only that distinction lets
// a caller tell "the API said null" from "this path is a typo".
func TestLookupData_NullResolvesButMissingDoesNot(t *testing.T) {
	data := parseResult(`{"a":null}`)

	v, ok := lookupData(data, "a")
	assert.True(t, ok)
	assert.Nil(t, v)

	_, ok = lookupData(data, "b")
	assert.False(t, ok)
}

func TestFields_OverBodyRelativeArray(t *testing.T) {
	parsed := parseResult(probeBody)
	f := &Fields{Over: "response", List: []Field{
		{Name: "id", Path: "id"},
		{Name: "name", Path: "name"},
		{Name: "status", Path: "detail.status"},
	}}

	out, err := renderFields(testRenderer, f,parsed, fctx(parsed), "", 0)
	require.NoError(t, err)
	assert.Equal(t, "id  name   status\n1   alpha  ok\n2   beta   bad\n3   gamma  ok\n", out)
}

// The documented context spelling reaches the same records.
func TestFields_OverContextRelativeArray(t *testing.T) {
	parsed := parseResult(probeBody)
	f := &Fields{Over: "data.response", List: []Field{{Name: "name", Path: "name"}}}

	out, err := renderFields(testRenderer, f,parsed, fctx(parsed), "", 0)
	require.NoError(t, err)
	assert.Equal(t, "name\nalpha\nbeta\ngamma\n", out)
}

func TestFields_OverMapWalksEntries(t *testing.T) {
	parsed := parseResult(`{"languages":{"Go":120,"Rust":80}}`)
	f := &Fields{Over: "languages", List: []Field{
		{Name: "language", Path: "@key"},
		{Name: "bytes", Path: "@value"},
	}}

	out, err := renderFields(testRenderer, f,parsed, fctx(parsed), "", 0)
	require.NoError(t, err)
	assert.Equal(t, "language  bytes\nGo        120\nRust      80\n", out)
}

// Without over=, the whole body is the record, so a field path indexes into it.
func TestFields_AbsolutePathIndexesArray(t *testing.T) {
	parsed := parseResult(probeBody)
	f := &Fields{List: []Field{
		{Name: "count", Path: "count"},
		{Name: "first", Path: "response.0.name"},
	}}

	out, err := renderFields(testRenderer, f,parsed, fctx(parsed), "", 0)
	require.NoError(t, err)
	assert.Contains(t, out, "count: 3\n")
	assert.Contains(t, out, "first: alpha\n")
}

func TestFields_OverMissingPathIsAnError(t *testing.T) {
	parsed := parseResult(probeBody)
	f := &Fields{Over: "responses", List: []Field{{Name: "name", Path: "name"}}}

	_, err := renderFields(testRenderer, f,parsed, fctx(parsed), "", 0)
	require.Error(t, err)
	assert.ErrorContains(t, err, `over="responses" resolved to nothing`)
}

func TestFields_OverScalarIsAnError(t *testing.T) {
	parsed := parseResult(probeBody)
	f := &Fields{Over: "count", List: []Field{{Name: "name", Path: "name"}}}

	_, err := renderFields(testRenderer, f,parsed, fctx(parsed), "", 0)
	require.Error(t, err)
	assert.ErrorContains(t, err, `over="count" is a number, not a list or a map`)
}

func TestFields_OverNullIsAnError(t *testing.T) {
	parsed := parseResult(`{"items":null}`)
	f := &Fields{Over: "items", List: []Field{{Name: "name", Path: "name"}}}

	_, err := renderFields(testRenderer, f,parsed, fctx(parsed), "", 0)
	require.Error(t, err)
	assert.ErrorContains(t, err, `over="items" is null, not a list or a map`)
}

// An empty list is a real answer ("nothing matched"), not a broken path.
func TestFields_OverEmptyListRendersNothing(t *testing.T) {
	parsed := parseResult(`{"items":[]}`)
	f := &Fields{Over: "items", List: []Field{{Name: "name", Path: "name"}}}

	out, err := renderFields(testRenderer, f,parsed, fctx(parsed), "", 0)
	require.NoError(t, err)
	assert.Equal(t, "name\n", out)
}

// ---------------------------------------------------------------------------
// End to end: the reproducer, through the real request + fields path.
// ---------------------------------------------------------------------------

func probeServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(probeBody))
	}))
	t.Cleanup(srv.Close)
	swapHTTPClient(t, srv)
	return srv
}

func TestIntegration_RequestOverListOfRecords(t *testing.T) {
	srv := probeServer(t)
	cfg := &Config{
		Name:    "t",
		Request: &Request{Method: "GET", URL: srv.URL + "/data.json"},
		Commands: []Command{{
			Name: "over",
			Fields: &Fields{Over: "response", List: []Field{
				{Name: "id", Path: "id"},
				{Name: "name", Path: "name"},
				{Name: "status", Path: "detail.status"},
			}},
		}},
	}

	code, out := execCmd(t, cfg, "over")
	require.Equal(t, 0, code)
	assert.Equal(t, "id  name   status\n1   alpha  ok\n2   beta   bad\n3   gamma  ok\n", out)
}

// The loud failure: a path that names nothing exits non-zero and says which
// path, instead of printing one empty record over exit 0.
func TestIntegration_RequestOverMissingPathFailsLoudly(t *testing.T) {
	srv := probeServer(t)
	cfg := &Config{
		Name:    "t",
		Request: &Request{Method: "GET", URL: srv.URL + "/data.json"},
		Commands: []Command{{
			Name:   "over",
			Fields: &Fields{Over: "responses", List: []Field{{Name: "name", Path: "name"}}},
		}},
	}

	code, out, errOut := execCmdFull(t, cfg, "over")
	assert.NotEqual(t, 0, code)
	assert.Empty(t, out)
	assert.Contains(t, errOut, `over="responses" resolved to nothing`)
}

// <response jq=> shapes the body before <fields> reads it, over the built-in
// client: the jq program invents `items`, and over= finds it.
func TestIntegration_RequestJQShapesBodyForFields(t *testing.T) {
	srv := probeServer(t)
	cfg := &Config{
		Name: "t",
		Vars: map[string]any{"shape": `{count, items: [.response[] | {id, name}]}`},
		Request: &Request{
			Method:   "GET",
			URL:      srv.URL + "/data.json",
			Response: &Response{JQ: "var.shape"},
		},
		Commands: []Command{{
			Name: "jq",
			Fields: &Fields{Over: "items", Footer: "{{.data.count}} total", List: []Field{
				{Name: "id", Path: "id"},
				{Name: "name", Path: "name"},
			}},
		}},
	}

	code, out := execCmd(t, cfg, "jq")
	require.Equal(t, 0, code)
	assert.Equal(t, "id  name\n1   alpha\n2   beta\n3   gamma\n3 total\n", out)
}

// The same shaping over a <transport>: the response path must not depend on
// which side of resolveTransport the bytes came from.
func TestIntegration_RequestJQShapesBodyViaTransport(t *testing.T) {
	cfg := &Config{
		Name:       "t",
		Transports: map[string]*Transport{"fake": shellTransport("fake", `printf '%s' '`+probeBody+`'`, true)},
		Vars:       map[string]any{"shape": `{count, items: [.response[] | {id, name}]}`},
		Request: &Request{
			Method:   "GET",
			URL:      "https://internal.example/data.json",
			Response: &Response{JQ: "var.shape"},
		},
		Commands: []Command{{
			Name: "jq",
			Fields: &Fields{Over: "items", List: []Field{
				{Name: "id", Path: "id"},
				{Name: "name", Path: "name"},
			}},
		}},
	}

	code, out := execCmd(t, cfg, "jq")
	require.Equal(t, 0, code)
	assert.Equal(t, "id  name\n1   alpha\n2   beta\n3   gamma\n", out)
}

// A jq path that names no var is a config typo. Shaping it away silently is how
// an unshaped body reaches <fields> and renders as nothing.
func TestIntegration_RequestJQMissingPathFailsLoudly(t *testing.T) {
	srv := probeServer(t)
	cfg := &Config{
		Name: "t",
		Vars: map[string]any{"shape": `{count}`},
		Request: &Request{
			Method:   "GET",
			URL:      srv.URL + "/data.json",
			Response: &Response{JQ: "var.shpe"},
		},
		Commands: []Command{{Name: "jq"}},
	}

	code, out, errOut := execCmdFull(t, cfg, "jq")
	assert.NotEqual(t, 0, code)
	assert.Empty(t, out)
	assert.Contains(t, errOut, `jq="var.shpe" resolved to nothing`)
}

// --version is answered by the binary, so it must not need a config to find.
func TestRun_VersionWithoutConfig(t *testing.T) {
	chdir(t, t.TempDir())
	prevStdout := os.Stdout
	r, w, err := os.Pipe()
	require.NoError(t, err)
	os.Stdout = w
	t.Cleanup(func() { os.Stdout = prevStdout })

	var errOut bytes.Buffer
	code := run([]string{"--version"}, &errOut)

	require.NoError(t, w.Close())
	out, _ := io.ReadAll(r)

	assert.Equal(t, 0, code)
	assert.NotContains(t, errOut.String(), "no config found")
	assert.Contains(t, string(out), "version")
	assert.NotEmpty(t, buildVersion())
}
