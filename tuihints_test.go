package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func loadTUIConfig(t *testing.T, src string) (*Config, error) {
	t.Helper()
	cfg, err := parseConfigXML([]byte(src))
	if err != nil {
		return nil, err
	}
	return cfg, validate(cfg)
}

func TestParseTUIHints(t *testing.T) {
	cfg, err := loadTUIConfig(t, browseDataConfig)
	require.NoError(t, err)
	list := cfg.Commands[0].Commands[0]
	require.NotNil(t, list.TUI)
	assert.True(t, list.TUI.Autorun)
	assert.Equal(t, []TUILink{{To: "repos view", Label: "Open repo", With: []TUIWith{{Name: "repo", Field: "name"}}}}, list.TUI.Links)
	assert.Equal(t, []string{"open", "closed", "all"}, list.Flags[0].Choices)
	view := cfg.Commands[0].Commands[1]
	assert.Equal(t, &Suggest{From: "repos view", Field: "name"}, &Suggest{From: "repos view", Field: view.Args[1].Suggest.Field})
	assert.Equal(t, "repos list", view.Args[1].Suggest.From)

	cfg, err = loadTUIConfig(t, `<config name="x"><command name="a"><tui label="A" rows="data.items" columns="id, name ,state" title="name"/><run>true</run></command></config>`)
	require.NoError(t, err)
	assert.Equal(t, &TUIHints{Label: "A", Rows: "data.items", Columns: []string{"id", "name", "state"}, Title: "name"}, cfg.Commands[0].TUI)
}

func TestValidateTUIHints(t *testing.T) {
	cases := map[string]struct{ src, err string }{
		"a link names a command that runs": {
			`<config name="x"><command name="a"><tui><link to="nope"/></tui><run>true</run></command></config>`,
			`a: <tui> link[0]: to="nope" names no command that runs`,
		},
		"a link cannot open a group": {
			`<config name="x"><command name="g"><command name="b"><run>true</run></command></command><command name="a"><tui><link to="g"/></tui><run>true</run></command></config>`,
			`to="g" names no command that runs`,
		},
		"with names an input of the target": {
			`<config name="x"><command name="b"><run>true</run></command><command name="a"><tui><link to="b"><with name="q" field="id"/></link></tui><run>true</run></command></config>`,
			`<with name="q">: "b" declares no arg or flag of that name`,
		},
		"with takes field or expr": {
			`<config name="x"><command name="b"><arg name="q"/><run>true</run></command><command name="a"><tui><link to="b"><with name="q"/></link></tui><run>true</run></command></config>`,
			`takes field= or expr=, exactly one`,
		},
		"with is declared once": {
			`<config name="x"><command name="b"><arg name="q"/><run>true</run></command><command name="a"><tui><link to="b"><with name="q" field="a"/><with name="q" field="b"/></link></tui><run>true</run></command></config>`,
			`<with name="q"> is declared twice`,
		},
		"a group takes only label and hidden": {
			`<config name="x"><command name="g"><tui autorun="true"/><command name="b"><run>true</run></command></command></config>`,
			`<tui> on a group takes only label= and hidden=`,
		},
		"suggest names a command that runs": {
			`<config name="x"><command name="a"><arg name="q" suggest="nope"/><run>true</run></command></config>`,
			`a: arg "q": suggest="nope" names no command that runs`,
		},
		"a bool takes no suggest": {
			`<config name="x"><command name="a"><flag name="q" type="bool" suggest="a"/><run>true</run></command></config>`,
			`flag "q" is a bool, so it takes no suggest=`,
		},
		"a bool takes no choices": {
			`<config name="x"><command name="a"><flag name="q" type="bool" choices="a,b"/><run>true</run></command></config>`,
			`a bool takes no choices=`,
		},
		"an int lists integers": {
			`<config name="x"><command name="a"><arg name="q" type="int" choices="1,two"/><run>true</run></command></config>`,
			`choice "two" is not an integer`,
		},
		"each choice once": {
			`<config name="x"><command name="a"><arg name="q" choices="a,a"/><run>true</run></command></config>`,
			`choice "a" is listed twice`,
		},
		"a default is a choice": {
			`<config name="x"><command name="a"><flag name="q" choices="a,b" default="c"/><run>true</run></command></config>`,
			`default "c" is not one of the choices (a, b)`,
		},
		"tui once": {
			`<config name="x"><command name="a"><tui/><tui/><run>true</run></command></config>`,
			`<tui> is declared once`,
		},
		"tui takes links only": {
			`<config name="x"><command name="a"><tui><column/></tui><run>true</run></command></config>`,
			`<tui>: unexpected child element <column>`,
		},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := loadTUIConfig(t, c.src)
			require.Error(t, err)
			assert.Contains(t, err.Error(), c.err)
		})
	}
}

const choicesConfig = `<config name="x">
	<command name="ls">
		<arg name="kind" choices="file,dir"/>
		<flag name="state" choices="open,closed" default="open"/>
		<flag name="size" type="int" choices="10,20"/>
		<flag name="tag" type="string-slice" choices="a,b"/>
		<run>echo <value name="arg.kind"/> <value name="flag.state"/> <value name="flag.size"/> <value expr="{{ join &quot;+&quot; .flag.tag }}"/></run>
	</command>
</config>`

func TestChoicesOnTheCommandLine(t *testing.T) {
	cfg, err := loadTUIConfig(t, choicesConfig)
	require.NoError(t, err)

	code, out := execCmd(t, cfg, "ls")
	assert.Equal(t, 0, code, "an omitted input is not checked")
	assert.Contains(t, out, "open 0")

	code, out = execCmd(t, cfg, "ls", "dir", "--state=closed", "--size=20", "--tag=a", "--tag=b")
	assert.Equal(t, 0, code)
	assert.Contains(t, out, "dir closed 20 a+b")

	for _, argv := range [][]string{
		{"ls", "link"},
		{"ls", "--state=merged"},
		{"ls", "--size=0"},
		{"ls", "--tag=a", "--tag=c"},
	} {
		code, _, stderr := execCmdFull(t, cfg, argv...)
		assert.NotEqual(t, 0, code, "%v", argv)
		assert.Contains(t, stderr, "is not one of", "%v", argv)
	}
}

func TestChoicesOverMCP(t *testing.T) {
	cfg, err := loadTUIConfig(t, choicesConfig)
	require.NoError(t, err)
	node := cfg.Commands[0]
	schema := buildToolInputSchema(node)
	props := schema["properties"].(map[string]any)
	assert.Equal(t, []any{"file", "dir"}, props["kind"].(map[string]any)["enum"])
	assert.Equal(t, []any{10, 20}, props["size"].(map[string]any)["enum"])
	assert.Equal(t, []any{"a", "b"}, props["tag"].(map[string]any)["items"].(map[string]any)["enum"])

	leaf := &mcpLeaf{name: "ls", node: node, cmdTmpl: node.Command}
	out, isErr := mcpExecLeaf(leaf, map[string]any{"state": "merged"})
	assert.True(t, isErr)
	assert.Contains(t, out, `flag "state": "merged" is not one of open, closed`)
	out, isErr = mcpExecLeaf(leaf, map[string]any{"kind": "dir"})
	assert.False(t, isErr, out)
}

func TestChoicesComplete(t *testing.T) {
	cfg, err := loadTUIConfig(t, choicesConfig)
	require.NoError(t, err)
	complete := argChoiceCompletion(cfg.Commands[0])
	got, _ := complete(nil, nil, "")
	assert.Equal(t, []string{"file", "dir"}, got)
	got, _ = complete(nil, []string{"file"}, "")
	assert.Empty(t, got, "a single arg completes once")
}
