package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testComponent = `<?xml version="1.1" encoding="UTF-8"?>
<Component xmlns="urn:tml:v1" name="Status">
	<Property name="title" type="string" required="true"/>
	<Property name="count" type="string" default="0"/>
	<Property name="rows" type="record[]" default=""/>

	<DataTemplate name="Row">
		<Property name="name" type="string" required="true"/>
		<Property name="state" type="string" default=""/>
		<Template>
			<Stack orientation="horizontal" gap="1">
				<Text width="12">{name}</Text>
				<Text>{state}</Text>
			</Stack>
		</Template>
	</DataTemplate>

	<Template>
		<Stack orientation="vertical">
			<Text id="title">{title}</Text>
			<Text id="count">count {count}</Text>
			<Stack id="rows" itemsSource="{rows}" itemTemplate="Row" orientation="vertical"/>
		</Stack>
	</Template>
</Component>
`

// writeComponent puts the test component in its own directory and returns that
// directory, which is what a config's Dir is.
func writeComponent(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "status.tml"), []byte(testComponent), 0o600))
	return dir
}

func TestBuildTML_ParsesPropsAndFields(t *testing.T) {
	cfg, err := parseConfigXML([]byte(`<config name="x">
		<run><request><url>https://example.test</url></request></run>
		<command name="dash">
			<tml src="ui/status.tml" dark="true">
				<prop name="title">Deployments</prop>
				<prop name="count" from="total"/>
				<prop name="rows" over="services">
					<field name="name">id</field>
					<field name="state">status.phase</field>
				</prop>
			</tml>
		</command>
	</config>`))
	require.NoError(t, err)

	view := cfg.Commands[0].TML
	require.NotNil(t, view)
	assert.Equal(t, "ui/status.tml", view.Src)
	assert.True(t, view.Dark)
	require.Len(t, view.Props, 3)
	assert.Equal(t, "Deployments", view.Props[0].Text)
	assert.Equal(t, "total", view.Props[1].From)
	assert.Equal(t, "services", view.Props[2].Over)
	assert.Equal(t, []TMLField{{Name: "name", Path: "id"}, {Name: "state", Path: "status.phase"}}, view.Props[2].Fields)
}

func TestValidateTML_Rejects(t *testing.T) {
	cases := map[string]struct {
		view *TML
		want string
	}{
		"no src":        {&TML{}, "needs a \"src\""},
		"unnamed prop":  {&TML{Src: "a.tml", Props: []TMLProp{{Text: "x"}}}, "has no \"name\""},
		"two sources":   {&TML{Src: "a.tml", Props: []TMLProp{{Name: "p", Text: "x", From: "y"}}}, "exactly one of"},
		"no source":     {&TML{Src: "a.tml", Props: []TMLProp{{Name: "p"}}}, "exactly one of"},
		"list no field": {&TML{Src: "a.tml", Props: []TMLProp{{Name: "p", Over: "rows"}}}, "at least one <field>"},
		"field no over": {&TML{Src: "a.tml", Props: []TMLProp{{Name: "p", Text: "x", Fields: []TMLField{{Name: "a"}}}}}, "no \"over\""},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			err := validateTML(tc.view, "commands[0]")
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
		})
	}
}

func TestTMLProps_ScalarsAndRecords(t *testing.T) {
	view := &TML{Src: "status.tml", Props: []TMLProp{
		{Name: "title", Text: "{{ .var.title }}"},
		{Name: "count", From: "total"},
		{Name: "rows", Over: "services", Fields: []TMLField{{Name: "name", Path: "id"}, {Name: "state", Path: "status.phase"}}},
	}}
	parsed := map[string]any{
		"total": int64(2),
		"services": []any{
			map[string]any{"id": "api", "status": map[string]any{"phase": "up"}},
			map[string]any{"id": "web", "status": map[string]any{"phase": "down"}},
		},
	}
	ctx := map[string]any{"var": map[string]any{"title": "Fleet"}, "data": parsed}

	props, err := tmlProps(view, parsed, ctx)
	require.NoError(t, err)
	assert.Equal(t, "Fleet", props["title"].String())
	assert.Equal(t, "2", props["count"].String())

	rows := props["rows"]
	require.Len(t, rows.Items(), 2)
	name, err := rows.Items()[1].Field("name")
	require.NoError(t, err)
	assert.Equal(t, "web", name.String())
	state, err := rows.Items()[0].Field("state")
	require.NoError(t, err)
	assert.Equal(t, "up", state.String())
}

func TestTMLProps_MissingPathFailsLoud(t *testing.T) {
	view := &TML{Src: "s.tml", Props: []TMLProp{{Name: "count", From: "nope"}}}
	_, err := tmlProps(view, map[string]any{"total": 1}, map[string]any{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not in the response or the context")

	list := &TML{Src: "s.tml", Props: []TMLProp{{Name: "rows", Over: "total", Fields: []TMLField{{Name: "name"}}}}}
	_, err = tmlProps(list, map[string]any{"total": 1}, map[string]any{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "needs a list")
}

func TestTMLScalar(t *testing.T) {
	assert.Equal(t, "", tmlScalar(nil))
	assert.Equal(t, "hi", tmlScalar("hi"))
	assert.Equal(t, "true", tmlScalar(true))
	assert.Equal(t, "7", tmlScalar(int64(7)))
	assert.Equal(t, "1.5", tmlScalar(1.5))
	assert.Equal(t, "12", tmlScalar(float64(12)))
}

func TestRenderTMLFrame(t *testing.T) {
	dir := writeComponent(t)
	view := &TML{Src: "status.tml", Props: []TMLProp{
		{Name: "title", Text: "Fleet"},
		{Name: "count", From: "total"},
		{Name: "rows", Over: "services", Fields: []TMLField{{Name: "name", Path: "id"}, {Name: "state", Path: "phase"}}},
	}}
	parsed := map[string]any{
		"total":    int64(2),
		"services": []any{map[string]any{"id": "api", "phase": "up"}, map[string]any{"id": "web", "phase": "down"}},
	}

	frame, err := renderTMLFrame(view, dir, parsed, map[string]any{"data": parsed}, 40, 12)
	require.NoError(t, err)
	assert.Contains(t, frame, "Fleet")
	assert.Contains(t, frame, "count 2")
	assert.Contains(t, frame, "api")
	assert.Contains(t, frame, "down")
}

// A component rejects a property it never declared, so a config that hands it
// one fails the run rather than drawing a screen missing the value.
func TestRenderTMLFrame_UndeclaredProp(t *testing.T) {
	dir := writeComponent(t)
	view := &TML{Src: "status.tml", Props: []TMLProp{
		{Name: "title", Text: "Fleet"},
		{Name: "nonesuch", Text: "x"},
	}}
	_, err := renderTMLFrame(view, dir, map[string]any{}, map[string]any{}, 40, 12)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "nonesuch")
}

func TestTMLEntry(t *testing.T) {
	assert.Equal(t, filepath.Join("cfg", "ui", "a.tml"), tmlEntry("ui/a.tml", "cfg"))
	assert.Equal(t, "ui/a.tml", tmlEntry("ui/a.tml", ""))
	abs := filepath.Join(string(filepath.Separator), "tmp", "a.tml")
	assert.Equal(t, abs, tmlEntry(abs, "cfg"))
}

// tmlConfig is a leaf that requests srv and draws the response through the test
// component.
func tmlConfig(t *testing.T, srv *httptest.Server, dir string) *Config {
	t.Helper()
	return &Config{
		Name: "t",
		Dir:  dir,
		Request: &Request{
			URL: srv.URL,
		},
		Commands: []Command{{
			Name: "dash",
			TML: &TML{Src: "status.tml", Props: []TMLProp{
				{Name: "title", Text: "Fleet"},
				{Name: "count", From: "total"},
				{Name: "rows", Over: "services", Fields: []TMLField{{Name: "name", Path: "id"}, {Name: "state", Path: "phase"}}},
			}},
		}},
	}
}

func TestTMLLeaf_RendersOnATerminal(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"total":2,"services":[{"id":"api","phase":"up"},{"id":"web","phase":"down"}]}`))
	}))
	defer srv.Close()
	swapHTTPClient(t, srv)

	cfg := tmlConfig(t, srv, writeComponent(t))
	code, out, errOut := execCmdFull(t, cfg, "dash", "--format=always")
	require.Equal(t, 0, code, errOut)
	assert.Contains(t, out, "Fleet")
	assert.Contains(t, out, "count 2")
	assert.Contains(t, out, "api")
}

// Piped, a screen is not what the caller asked for: the leaf falls through to
// the raw body, which is what a pipe can use.
func TestTMLLeaf_PipedFallsThrough(t *testing.T) {
	body := `{"total":2,"services":[{"id":"api","phase":"up"}]}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()
	swapHTTPClient(t, srv)

	cfg := tmlConfig(t, srv, writeComponent(t))
	code, out, errOut := execCmdFull(t, cfg, "dash")
	require.Equal(t, 0, code, errOut)
	assert.Equal(t, body, strings.TrimSpace(out))
	assert.NotContains(t, out, "Fleet")
}

// --as names a representation, so it wins over the screen and the leaf goes
// through <fields> instead.
func TestTMLLeaf_AsSinkWins(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"total":2,"services":[{"id":"api","phase":"up"}]}`))
	}))
	defer srv.Close()
	swapHTTPClient(t, srv)

	cfg := tmlConfig(t, srv, writeComponent(t))
	code, out, errOut := execCmdFull(t, cfg, "dash", "--format=always", "--as=json")
	require.Equal(t, 0, code, errOut)
	assert.Contains(t, out, `"total"`)
	assert.NotContains(t, out, "Fleet")
}

func TestTMLLeaf_RenderErrorIsReported(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"services":[]}`))
	}))
	defer srv.Close()
	swapHTTPClient(t, srv)

	cfg := tmlConfig(t, srv, writeComponent(t))
	code, _, errOut := execCmdFull(t, cfg, "dash", "--format=always")
	assert.Equal(t, 1, code)
	assert.Contains(t, errOut, "total")
}

func TestValidateTML_ConfigRejectsBothPresentations(t *testing.T) {
	cfg := &Config{Name: "t", Request: &Request{URL: "https://example.test"}, Commands: []Command{{
		Name:   "dash",
		Fields: []FieldsBlock{{}},
		TML:    &TML{Src: "a.tml"},
	}}}
	err := validate(cfg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not both")
}
