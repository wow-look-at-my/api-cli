package main

import (
	_ "embed"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

//go:embed testdata/decode-config.xml
var decodeConfig string

//go:embed testdata/decode-rows.csv
var decodeRowsCSV string

// decodeRowsJSON is the records decodeRowsCSV holds.
//
//go:embed testdata/decode-rows.json
var decodeRowsJSON string

//go:embed testdata/decode-manifest.yml
var decodeManifestYAML string

//go:embed testdata/decode-feed.xml
var decodeFeedXML string

//go:embed testdata/decode-events.ndjson
var decodeEventsNDJSON string

//go:embed testdata/decode-signin.html
var decodeSignInHTML string

// decodeBodies is what the test server answers, by path. An empty type leaves
// the header to net/http, which calls every one of these text/plain.
var decodeBodies = map[string]struct{ contentType, body string }{
	"/untyped-csv": {"", decodeRowsCSV},
	"/csv":         {"text/csv; charset=utf-8", decodeRowsCSV},
	"/yaml":        {"application/yaml", decodeManifestYAML},
	"/feed":        {"application/rss+xml", decodeFeedXML},
	"/events":      {"application/x-ndjson", decodeEventsNDJSON},
	"/html":        {"text/html", decodeSignInHTML},
	"/lines":       {"text/plain", "a\nb\nc"},
	"/mislabeled":  {"text/csv", "name,id\nada\n"},
}

// decodeHarness loads the config and points it at a server of its own.
func decodeHarness(t *testing.T) *Config {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		answer, ok := decodeBodies[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		if answer.contentType != "" {
			w.Header().Set("Content-Type", answer.contentType)
		}
		_, _ = w.Write([]byte(answer.body))
	}))
	t.Cleanup(srv.Close)
	swapHTTPClient(t, srv)
	t.Setenv("API_CLI_DECODE_TEST_URL", srv.URL)

	cfg, err := loadStr(t, decodeConfig)
	require.NoError(t, err)
	return cfg
}

func TestDecodeConfig_RunCarriesItsFormat(t *testing.T) {
	cfg, err := loadStr(t, decodeConfig)
	require.NoError(t, err)

	assert.Equal(t, "csv", cfg.Request.Decode, "the root run")
	byName := map[string]Command{}
	for _, c := range cfg.Commands {
		byName[c.Name] = c
	}
	assert.Nil(t, byName["rows"].Request, "rows declares no run, so it inherits the root's with its format")
	assert.Equal(t, "", byName["typed"].Request.Decode, "a run that names no format carries none")
	assert.Equal(t, "lines", byName["mounts"].Command.Decode, "a shell run")
	assert.Equal(t, "tsv", byName["users"].Command.Decode, "an argv run")
	assert.Equal(t, "form", byName["token"].Steps[0].Command.Decode, "a step's own run")
}

func TestDecodeConfig_RejectsWhatItCannotRead(t *testing.T) {
	_, err := loadStr(t, `<config name="x"><command name="c"><run decode="toml">echo</run></command></config>`)
	require.Error(t, err)
	assert.Contains(t, err.Error(), `<run decode="toml">: unknown format`)

	_, err = loadStr(t, `<config name="x"><transports><transport name="t"><run decode="csv">curl</run></transport></transports><command name="c"><run>echo</run></command></config>`)
	require.Error(t, err, "a transport's program hands back bytes, and the request names their format")
	assert.Contains(t, err.Error(), "decode")
}

// The header order is the table's column order. A sort gives id, name, state.
func TestDecodeIntegration_CSVAsATable(t *testing.T) {
	cfg := decodeHarness(t)
	code, out, errOut := execCmdFull(t, cfg, "--as=table", "rows")
	require.Equal(t, 0, code, "stderr: %s", errOut)

	lines := strings.Split(strings.TrimSpace(out), "\n")
	require.Len(t, lines, 4)
	assert.Equal(t, []string{"name", "id", "state"}, strings.Fields(lines[0]))
	assert.Equal(t, []string{"ada", "1", "open"}, strings.Fields(lines[1]))
	assert.Equal(t, []string{"bob,", "jr", "2", "closed"}, strings.Fields(lines[2]), "a quoted cell keeps its comma")
}

func TestDecodeIntegration_CSVAsJSON(t *testing.T) {
	cfg := decodeHarness(t)
	code, out, errOut := execCmdFull(t, cfg, "--as=json", "rows")
	require.Equal(t, 0, code, "stderr: %s", errOut)
	assert.JSONEq(t, decodeRowsJSON, out)
}

// Nothing reads the structure, so the bytes go out as they arrived.
func TestDecodeIntegration_UnshapedOutputIsTheBody(t *testing.T) {
	cfg := decodeHarness(t)
	code, out := execCmd(t, cfg, "rows")
	require.Equal(t, 0, code)
	assert.Equal(t, decodeRowsCSV, out)

	code, out = execCmd(t, cfg, "--no-format", "named")
	require.Equal(t, 0, code)
	assert.Equal(t, decodeRowsCSV, out)

	code, out = execCmd(t, cfg, "shaped")
	require.Equal(t, 0, code)
	assert.Equal(t, decodeRowsCSV, out, "an empty <response> pretty-prints JSON, and leaves any other format alone")
}

func TestDecodeIntegration_DeclaredFieldsOverCSV(t *testing.T) {
	cfg := decodeHarness(t)
	code, out, errOut := execCmdFull(t, cfg, "named")
	require.Equal(t, 0, code, "stderr: %s", errOut)
	lines := strings.Split(strings.TrimSpace(out), "\n")
	assert.Equal(t, []string{"NAME", "ID"}, strings.Fields(lines[0]))
	assert.Equal(t, []string{"cy", "3"}, strings.Fields(lines[3]))
}

func TestDecodeIntegration_JQOverCSV(t *testing.T) {
	cfg := decodeHarness(t)
	code, out, errOut := execCmdFull(t, cfg, "open")
	require.Equal(t, 0, code, "stderr: %s", errOut)
	assert.JSONEq(t, `["1","3"]`, out)
}

func TestDecodeIntegration_JQOverText(t *testing.T) {
	cfg := decodeHarness(t)
	code, out, errOut := execCmdFull(t, cfg, "count")
	require.Equal(t, 0, code, "stderr: %s", errOut)
	assert.Equal(t, "3", strings.TrimSpace(out))
}

// decode="auto" is the default, and a response says what it is.
func TestDecodeIntegration_ContentTypeNamesTheFormat(t *testing.T) {
	cfg := decodeHarness(t)

	code, out, errOut := execCmdFull(t, cfg, "--as=json", "typed", "csv")
	require.Equal(t, 0, code, "stderr: %s", errOut)
	assert.JSONEq(t, decodeRowsJSON, out)

	code, out, errOut = execCmdFull(t, cfg, "--as=json", "typed", "events")
	require.Equal(t, 0, code, "stderr: %s", errOut)
	assert.JSONEq(t, `[{"id":1,"kind":"push"},{"id":2,"kind":"tag"}]`, out)

	code, out, errOut = execCmdFull(t, cfg, "--as=json", "typed", "untyped-csv")
	require.Equal(t, 0, code, "stderr: %s", errOut)
	assert.Equal(t, jsonString(t, strings.TrimSpace(decodeRowsCSV)), strings.TrimSpace(out), "text/plain names no format, so the body stays one string")

	code, out, errOut = execCmdFull(t, cfg, "--as=json", "typed", "mislabeled")
	require.Equal(t, 0, code, "stderr: %s", errOut)
	assert.Equal(t, jsonString(t, "name,id\nada"), strings.TrimSpace(out), "a guess that does not hold falls back to text, and does not fail")
}

// jsonString is s as the json sink prints a string.
func jsonString(t *testing.T, s string) string {
	t.Helper()
	b, err := json.Marshal(s)
	require.NoError(t, err)
	return string(b)
}

func TestDecodeIntegration_YAMLRecords(t *testing.T) {
	cfg := decodeHarness(t)
	code, out, errOut := execCmdFull(t, cfg, "manifest")
	require.Equal(t, 0, code, "stderr: %s", errOut)
	lines := strings.Split(strings.TrimSpace(out), "\n")
	require.Len(t, lines, 3)
	assert.Equal(t, []string{"NAME", "IMAGE"}, strings.Fields(lines[0]))
	assert.Equal(t, []string{"web", "nginx:1.27"}, strings.Fields(lines[1]))
	assert.Equal(t, []string{"sidecar", "envoy:1.31"}, strings.Fields(lines[2]))
}

func TestDecodeIntegration_XMLRecords(t *testing.T) {
	cfg := decodeHarness(t)
	code, out, errOut := execCmdFull(t, cfg, "feed")
	require.Equal(t, 0, code, "stderr: %s", errOut)
	lines := strings.Split(strings.TrimSpace(out), "\n")
	require.Len(t, lines, 3)
	assert.Equal(t, []string{"TITLE", "ID"}, strings.Fields(lines[0]))
	assert.Equal(t, []string{"one", "a1"}, strings.Fields(lines[1]))
	assert.Equal(t, []string{"two", "a2"}, strings.Fields(lines[2]))
}

// A login page under decode="json" is a failed run, not an empty table.
func TestDecodeIntegration_NamedFormatFailsOnAnotherBody(t *testing.T) {
	cfg := decodeHarness(t)
	code, out, errOut := execCmdFull(t, cfg, "strict")
	assert.Equal(t, 1, code)
	assert.Empty(t, out)
	assert.Contains(t, errOut, "the output is not json")
	assert.Contains(t, errOut, "<title>Sign in</title>", "the error shows the start of the body")
}

func TestDecodeIntegration_CommandOutput(t *testing.T) {
	cfg := decodeHarness(t)

	code, out, errOut := execCmdFull(t, cfg, "--as=json", "mounts")
	require.Equal(t, 0, code, "stderr: %s", errOut)
	assert.JSONEq(t, `["a","b"]`, out)

	code, out, errOut = execCmdFull(t, cfg, "users")
	require.Equal(t, 0, code, "stderr: %s", errOut)
	lines := strings.Split(strings.TrimSpace(out), "\n")
	require.Len(t, lines, 3)
	assert.Equal(t, []string{"UID", "NAME"}, strings.Fields(lines[0]))
	assert.Equal(t, []string{"0", "root"}, strings.Fields(lines[1]))
	assert.Equal(t, []string{"1000", "ada"}, strings.Fields(lines[2]))
}

func TestDecodeIntegration_StepResults(t *testing.T) {
	cfg := decodeHarness(t)

	code, out, errOut := execCmdFull(t, cfg, "token")
	require.Equal(t, 0, code, "stderr: %s", errOut)
	assert.Equal(t, "abc 60\n", out, "a form-encoded answer is an object at .result")

	code, out, errOut = execCmdFull(t, cfg, "kept")
	require.Equal(t, 0, code, "stderr: %s", errOut)
	assert.Equal(t, "string\n", out, "text does not sniff the JSON inside")

	code, out, errOut = execCmdFull(t, cfg, "each")
	require.Equal(t, 0, code, "stderr: %s", errOut)
	assert.Equal(t, "two\n", out, "each element of a repeated step decodes")
}

func TestDecodeIntegration_StepFailsOnAnotherBody(t *testing.T) {
	cfg := decodeHarness(t)
	code, out := execCmd(t, cfg, "badstep")
	assert.Equal(t, 1, code)
	assert.NotContains(t, out, "unreachable", "the leaf's own run never starts")

	var step []Step
	for _, c := range cfg.Commands {
		if c.Name == "badstep" {
			step = c.Steps
		}
	}
	_, err := runSteps(step, map[string]any{}, map[string]any{}, nil, nil, "", "", captureExec, execStderr)
	require.Error(t, err)
	assert.Contains(t, err.Error(), `step "s": the output is not json`)
	assert.Contains(t, err.Error(), "not-json")
}

func TestDecodeIntegration_LegacyFormat(t *testing.T) {
	cfg := decodeHarness(t)

	code, out, errOut := execCmdFull(t, cfg, "legacy")
	require.Equal(t, 0, code, "stderr: %s", errOut)
	assert.Equal(t, "3 lines", strings.TrimSpace(out), "no input= reads what the run declares")

	code, out, errOut = execCmdFull(t, cfg, "pinned")
	require.Equal(t, 0, code, "stderr: %s", errOut)
	assert.Equal(t, "string", strings.TrimSpace(out), "input= still overrides the run")
}

func decodeMCPLeaf(t *testing.T, cfg *Config, name string) *mcpLeaf {
	t.Helper()
	leaves := collectMCPLeaves(cfg.Commands, mcpInherit{vars: cfg.Vars, cmd: cfg.Command, request: cfg.Request, formats: cfg.Formats})
	for i := range leaves {
		if leaves[i].name == name {
			return &leaves[i]
		}
	}
	require.Failf(t, "no such tool", "%q", name)
	return nil
}

func TestDecodeIntegration_MCPToolsDecodeToo(t *testing.T) {
	cfg := decodeHarness(t)

	out, isErr := mcpExecLeaf(decodeMCPLeaf(t, cfg, "named"), map[string]any{})
	require.False(t, isErr, out)
	lines := strings.Split(strings.TrimSpace(out), "\n")
	assert.Equal(t, []string{"NAME", "ID"}, strings.Fields(lines[0]))
	assert.Equal(t, []string{"ada", "1"}, strings.Fields(lines[1]))

	out, isErr = mcpExecLeaf(decodeMCPLeaf(t, cfg, "users"), map[string]any{})
	require.False(t, isErr, out)
	assert.Equal(t, []string{"0", "root"}, strings.Fields(strings.Split(strings.TrimSpace(out), "\n")[1]))

	out, isErr = mcpExecLeaf(decodeMCPLeaf(t, cfg, "legacy"), map[string]any{})
	require.False(t, isErr, out)
	assert.Equal(t, "3 lines", strings.TrimSpace(out))

	out, isErr = mcpExecLeaf(decodeMCPLeaf(t, cfg, "strict"), map[string]any{})
	assert.True(t, isErr)
	assert.Contains(t, out, "the output is not json")

	out, isErr = mcpExecLeaf(decodeMCPLeaf(t, cfg, "rows"), map[string]any{})
	require.False(t, isErr, out)
	assert.Equal(t, decodeRowsCSV, out, "a tool with no shape hands back the body")
}

// The browser's data view takes any body that decodes to records.
func TestDecodeIntegration_BrowserDataView(t *testing.T) {
	h := newDataHarness(t)
	entry := browseEntryAt(t, h.m.entries, "ping")

	f := newFrame(entry, browseRunMsg{out: browseOutput{text: decodeRowsCSV, raw: decodeRowsCSV, format: "csv"}})
	require.True(t, f.hasData)
	require.NotNil(t, f.level())
	assert.Equal(t, levelTable, f.level().kind)
	assert.Len(t, f.level().items, 3)
	f.view = viewRaw
	assert.Equal(t, browseLines(decodeRowsCSV), f.lines(), "the raw view is the CSV, not JSON made from it")

	text := newFrame(entry, browseRunMsg{out: browseOutput{text: decodeRowsCSV, raw: decodeRowsCSV}})
	assert.False(t, text.hasData, "with no format the same bytes are text")

	broken := newFrame(entry, browseRunMsg{out: browseOutput{raw: "<html>", format: "json"}})
	assert.False(t, broken.hasData, "a body its format rejects has no data view")

	scalar := newFrame(entry, browseRunMsg{out: browseOutput{raw: "just words", format: "text"}})
	assert.False(t, scalar.hasData, "one string is not records")
}
