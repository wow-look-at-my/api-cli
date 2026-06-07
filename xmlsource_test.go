package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func mustParse(t *testing.T, src string) *Config {
	t.Helper()
	cfg, err := parseConfigXML([]byte(src))
	require.NoError(t, err)
	return cfg
}

// loadStr parses and validates a config from a string.
func loadStr(t *testing.T, src string) (*Config, error) {
	t.Helper()
	cfg, err := parseConfigXML([]byte(src))
	if err != nil {
		return nil, err
	}
	return cfg, validate(cfg)
}

func TestParseXML_ShellRun(t *testing.T) {
	cfg := mustParse(t, `<config name="x"><command name="c"><run>echo hi</run></command></config>`)
	assert.Equal(t, "x", cfg.Name)
	require.Len(t, cfg.Commands, 1)
	c := cfg.Commands[0]
	require.NotNil(t, c.Command)
	assert.True(t, c.Command.Shell)
	assert.Equal(t, "echo hi", c.Command.Template)
}

func TestParseXML_ArgvRun(t *testing.T) {
	cfg := mustParse(t, `<config name="x"><command name="c"><run><argv>echo</argv><argv>{{.arg.x}}</argv></run></command></config>`)
	assert.Equal(t, []string{"echo", "{{.arg.x}}"}, cfg.Commands[0].Command.Argv)
}

func TestParseXML_Placeholders(t *testing.T) {
	cfg := mustParse(t, `<config name="x"><vars>
		<var name="a"><value name="env.X" default="def" as="urlpath"/></var>
		<var name="b"><if test="env.Y" eq="1">yes<else/>no</if></var>
		<var name="c"><for each="items"><value name="name"/></for></var>
		<var name="d"><value expr="{{ upper .env.Z }}"/></var>
		<var name="e"><if test="env.W">on</if></var>
	</vars><command name="c"><run>x</run></command></config>`)
	assert.Equal(t, `{{ urlpath (.env.X | default "def") }}`, cfg.Vars["a"])
	assert.Equal(t, `{{ if eq (printf "%v" .env.Y) "1" }}yes{{ else }}no{{ end }}`, cfg.Vars["b"])
	assert.Equal(t, `{{ range .items }}{{ .name }}{{ end }}`, cfg.Vars["c"])
	assert.Equal(t, `{{ upper .env.Z }}`, cfg.Vars["d"])
	assert.Equal(t, `{{ if truthy .env.W }}on{{ end }}`, cfg.Vars["e"])
}

func TestParseXML_DashedPathAndIfEq(t *testing.T) {
	cfg := mustParse(t, `<config name="x"><vars>
		<var name="dash"><value name="flag.dry-run"/></var>
		<var name="ifeq"><if test="flag.s" eq="">empty<else/>set</if></var>
	</vars><command name="c"><run>x</run></command></config>`)
	// A non-identifier segment compiles to an index expression.
	assert.Equal(t, `{{ (index . "flag" "dry-run") }}`, cfg.Vars["dash"])
	// eq="" is a real equality test, not a truthiness test.
	assert.Contains(t, cfg.Vars["ifeq"].(string), `eq (printf "%v" .flag.s) ""`)

	out, err := renderString(cfg.Vars["dash"].(string), map[string]any{"flag": map[string]any{"dry-run": "yes"}})
	require.NoError(t, err)
	assert.Equal(t, "yes", out)
}

func TestParseXML_ForRenders(t *testing.T) {
	cfg := mustParse(t, `<config name="x"><vars><var name="t"><for each="items"><value name="name"/>,</for></var></vars><command name="c"><run>x</run></command></config>`)
	out, err := renderString(cfg.Vars["t"].(string), map[string]any{
		"items": []any{map[string]any{"name": "a"}, map[string]any{"name": "b"}},
	})
	require.NoError(t, err)
	assert.Equal(t, "a,b,", out)
}

func TestParseXML_BOMAndDeclaration(t *testing.T) {
	src := "\xef\xbb\xbf<?xml version=\"1.1\" encoding=\"UTF-8\"?>\n<config name=\"x\"><command name=\"c\"><run>echo hi</run></command></config>"
	cfg, err := parseConfigXML([]byte(src))
	require.NoError(t, err)
	assert.Equal(t, "x", cfg.Name)
}

func TestParseXML_Request(t *testing.T) {
	cfg := mustParse(t, `<config name="x">
		<run><request method="POST">
			<url><value name="var.base"/>/p</url>
			<query from="entry.query">
				<param name="a"><value name="flag.a"/></param>
				<if test="flag.b"><param name="b">1</param></if>
			</query>
			<header name="Accept">application/json</header>
			<if test="var.token"><header name="Authorization">Bearer <value name="var.token"/></header></if>
			<body>{{.flag.body}}</body>
			<response jq="var.filter"/>
		</request></run>
		<command name="c"/></config>`)
	req := cfg.Request
	require.NotNil(t, req)
	assert.Equal(t, "POST", req.Method)
	assert.Equal(t, "{{ .var.base }}/p", req.URL)
	assert.Equal(t, "entry.query", req.QueryFrom)
	require.Len(t, req.Query, 2)
	assert.Equal(t, Param{Name: "a", Value: "{{ .flag.a }}"}, req.Query[0])
	assert.Equal(t, Param{Name: "b", Value: "1", When: "flag.b"}, req.Query[1])
	require.Len(t, req.Headers, 2)
	assert.Equal(t, Header{Name: "Accept", Value: "application/json"}, req.Headers[0])
	assert.Equal(t, Header{Name: "Authorization", Value: "Bearer {{ .var.token }}", When: "var.token"}, req.Headers[1])
	assert.Equal(t, "{{.flag.body}}", req.Body)
	require.NotNil(t, req.Response)
	assert.Equal(t, "var.filter", req.Response.JQ)
}

func TestParseXML_Fields(t *testing.T) {
	cfg := mustParse(t, `<config name="x"><command name="c"><run>x</run>
		<fields over="data.items" footer="{{.data.total}} total">
			<field name="login">login</field>
			<field name="lang" default="-" truncate="5" priority="-1" show_in="!json">language</field>
			<field name="virt" expr="{{.a}}-{{.b}}"/>
			<field name="sha" firstline="true">sha</field>
		</fields></command></config>`)
	f := cfg.Commands[0].Fields
	require.NotNil(t, f)
	assert.Equal(t, "data.items", f.Over)
	assert.Equal(t, "{{.data.total}} total", f.Footer)
	require.Len(t, f.List, 4)
	assert.Equal(t, Field{Name: "login", Path: "login"}, f.List[0])
	assert.Equal(t, Field{Name: "lang", Path: "language", Default: "-", Truncate: 5, Priority: -1, ShowIn: "!json"}, f.List[1])
	assert.Equal(t, Field{Name: "virt", Expr: "{{.a}}-{{.b}}"}, f.List[2])
	assert.Equal(t, Field{Name: "sha", Path: "sha", FirstLine: true}, f.List[3])
}

func TestParseXML_EntryShapes(t *testing.T) {
	cfg := mustParse(t, `<config name="x"><command name="c"><run>x</run>
		<entry>
			<path>/u/<value name="arg.id"/></path>
			<query><param name="q"><value name="flag.q"/></param></query>
		</entry></command></config>`)
	ent, err := renderEntry(cfg.Commands[0].Entry, map[string]any{
		"arg":  map[string]any{"id": 5},
		"flag": map[string]any{"q": "hi"},
	})
	require.NoError(t, err)
	m := ent.(map[string]any)
	assert.Equal(t, "/u/5", m["path"])
	assert.Equal(t, map[string]any{"q": "hi"}, m["query"])
}

func TestParseXML_ArgsFlagsSteps(t *testing.T) {
	cfg := mustParse(t, `<config name="x"><command name="c">
		<arg name="id" type="int" required="true"/>
		<flag name="limit" short="n" type="int" default="30" conflicts="page"/>
		<flag name="page" type="int" default="1"/>
		<flag name="tags" type="string-slice" default="a,b"/>
		<flag name="on" type="bool" default="true"/>
		<steps><step name="s" when="{{.arg.id}}"><run>echo {{.arg.id}}</run></step></steps>
		<run>echo done</run></command></config>`)
	c := cfg.Commands[0]
	require.Len(t, c.Args, 1)
	assert.Equal(t, Arg{Name: "id", Type: "int", Required: true}, c.Args[0])
	require.Len(t, c.Flags, 4)
	assert.Equal(t, 30, c.Flags[0].Default)
	assert.Equal(t, []string{"page"}, c.Flags[0].Conflicts)
	assert.Equal(t, []any{"a", "b"}, c.Flags[2].Default)
	assert.Equal(t, true, c.Flags[3].Default)
	require.Len(t, c.Steps, 1)
	assert.Equal(t, "{{.arg.id}}", c.Steps[0].When)
	require.NotNil(t, c.Steps[0].Command)
}

func TestParseXML_FormatsLegacy(t *testing.T) {
	cfg := mustParse(t, `<config name="x">
		<formats><format name="u" input="json" when="{{.tty}}">
			<view name="t" default="true">{{.data}}</view>
		</format></formats>
		<command name="c"><run>x</run><format ref="u"/></command></config>`)
	require.Contains(t, cfg.Formats, "u")
	assert.Equal(t, "json", cfg.Formats["u"].Input)
	require.Len(t, cfg.Formats["u"].Views, 1)
	assert.True(t, cfg.Formats["u"].Views[0].Default)
	require.NotNil(t, cfg.Commands[0].Format)
	assert.Equal(t, "u", cfg.Commands[0].Format.Name)
}

func TestParseXML_InlineFormat(t *testing.T) {
	cfg := mustParse(t, `<config name="x"><command name="c"><run>x</run>
		<format input="json"><view name="v">{{.data}}</view></format></command></config>`)
	ref := cfg.Commands[0].Format
	require.NotNil(t, ref)
	require.NotNil(t, ref.Inline)
	assert.Equal(t, "v", ref.Inline.Views[0].Name)
}

func TestParseXML_ConfigLevelFields(t *testing.T) {
	cfg := mustParse(t, `<config name="x">
		<description>Hi there</description>
		<vars><var name="v">val</var></vars>
		<run>root cmd</run>
		<cwd>/tmp</cwd>
		<stdin>input</stdin>
		<command name="c" passthrough="true" confirm="sure?"/></config>`)
	assert.Equal(t, "Hi there", cfg.Description)
	assert.Equal(t, "val", cfg.Vars["v"])
	require.NotNil(t, cfg.Command)
	assert.Equal(t, "root cmd", cfg.Command.Template)
	assert.Equal(t, "/tmp", cfg.Cwd)
	assert.Equal(t, "input", cfg.Stdin)
	assert.True(t, cfg.Commands[0].Passthrough)
	assert.Equal(t, "sure?", cfg.Commands[0].Confirm)
}

func TestParseXML_ParseErrors(t *testing.T) {
	cases := map[string]string{
		"root not config":   `<nope/>`,
		"unknown root attr":  `<config name="x" bogus="y"/>`,
		"unknown child":      `<config name="x"><bogus/></config>`,
		"malformed xml":      `<config name="x"><command></config>`,
		"multiple roots":     `<config name="a"><command name="c"><run>x</run></command></config><config name="b"/>`,
		"url unknown attr":    `<config name="x"><run><request><url bad="1">u</url></request></run><command name="c"/></config>`,
		"cwd unknown attr":    `<config name="x"><cwd bad="1">/tmp</cwd><command name="c"><run>x</run></command></config>`,
		"value name+expr":    `<config name="x"><vars><var name="v"><value name="a" expr="b"/></var></vars><command name="c"><run>x</run></command></config>`,
		"value neither":      `<config name="x"><vars><var name="v"><value/></var></vars><command name="c"><run>x</run></command></config>`,
		"if no test":         `<config name="x"><vars><var name="v"><if>x</if></var></vars><command name="c"><run>x</run></command></config>`,
		"var no name":        `<config name="x"><vars><var>v</var></vars><command name="c"><run>x</run></command></config>`,
		"field path+expr":    `<config name="x"><command name="c"><run>x</run><fields><field name="f" expr="e">p</field></fields></command></config>`,
		"field neither":      `<config name="x"><command name="c"><run>x</run><fields><field name="f"/></fields></command></config>`,
		"run request+element": `<config name="x"><run><request><url>u</url></request><argv>x</argv></run><command name="c"/></config>`,
		"bad flag default":   `<config name="x"><command name="c"><flag name="n" type="int" default="notanint"/><run>x</run></command></config>`,
	}
	for name, src := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := parseConfigXML([]byte(src))
			assert.Error(t, err)
		})
	}
}

func TestParseXML_ValidateErrors(t *testing.T) {
	cases := map[string]string{
		"leaf no run":      `<config name="x"><command name="c"/></config>`,
		"request no url":    `<config name="x"><run><request><header name="A">v</header></request></run><command name="c"/></config>`,
		"fields and format": `<config name="x"><formats><format name="u"><view name="v">x</view></format></formats><command name="c"><run>x</run><fields><field name="f">p</field></fields><format ref="u"/></command></config>`,
	}
	for name, src := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := loadStr(t, src)
			assert.Error(t, err)
		})
	}
}
