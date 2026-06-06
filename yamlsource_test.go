package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// loadFrom writes content to a temp file and loads it as a config.
func loadFrom(t *testing.T, name, content string) (*Config, error) {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	require.NoError(t, os.WriteFile(p, []byte(content), 0o600))
	return Load(p)
}

// TestSourceToJSON_PassesThroughJSON: valid JSON (even multi-line) is handed to
// the decoder unchanged, never routed through the YAML parser.
func TestSourceToJSON_PassesThroughJSON(t *testing.T) {
	in := "{\n  \"a\": 1,\n  \"b\": [\"x\", \"y\"]\n}"
	out, err := sourceToJSON([]byte(in))
	require.NoError(t, err)
	assert.Equal(t, in, string(out))
}

// TestSourceToJSON_TabYAML: non-JSON input is parsed as tab-YAML and re-encoded
// as JSON with the expected scalar types.
func TestSourceToJSON_TabYAML(t *testing.T) {
	j, err := sourceToJSON([]byte("a: 1\nb: true\nc: hi\nd:\n\t- x\n\t- y\n"))
	require.NoError(t, err)

	var m map[string]any
	require.NoError(t, json.Unmarshal(j, &m))
	assert.Equal(t, float64(1), m["a"])
	assert.Equal(t, true, m["b"])
	assert.Equal(t, "hi", m["c"])
	assert.Equal(t, []any{"x", "y"}, m["d"])
}

func TestLoad_JSONSingleLine(t *testing.T) {
	cfg, err := loadFrom(t, "api.json",
		`{"name":"x","command":"echo hi","commands":[{"name":"go","entry":{}}]}`)
	require.NoError(t, err)
	assert.Equal(t, "x", cfg.Name)
	require.NotNil(t, cfg.Command)
	assert.True(t, cfg.Command.Shell)
	require.Len(t, cfg.Commands, 1)
}

// TestLoad_JSONMultiLine proves pretty-printed multi-line JSON still loads --
// it goes through the stdlib decoder, not yaml-fixed (which is line-oriented and
// does not parse multi-line flow).
func TestLoad_JSONMultiLine(t *testing.T) {
	cfg, err := loadFrom(t, "api.json", `{
  "name": "x",
  "command": "echo hi",
  "commands": [
    { "name": "go", "entry": {} }
  ]
}`)
	require.NoError(t, err)
	assert.Equal(t, "x", cfg.Name)
	require.Len(t, cfg.Commands, 1)
	assert.Equal(t, "go", cfg.Commands[0].Name)
}

// TestLoad_TabYAML is the headline: a tab-indented config loads correctly.
func TestLoad_TabYAML(t *testing.T) {
	cfg, err := loadFrom(t, "api.yaml",
		"name: demo\n"+
			"command: echo {{.var.x}}\n"+
			"vars:\n"+
			"\tx: hello\n"+
			"commands:\n"+
			"\t- name: get\n"+
			"\t  args:\n"+
			"\t\t- name: id\n"+
			"\t\t  type: int\n"+
			"\t\t  required: true\n"+
			"\t  entry:\n"+
			"\t\tpath: '/u/{{.arg.id}}'\n")
	require.NoError(t, err)
	assert.Equal(t, "demo", cfg.Name)
	require.NotNil(t, cfg.Command)
	assert.True(t, cfg.Command.Shell)
	assert.Equal(t, "hello", cfg.Vars["x"])
	require.Len(t, cfg.Commands, 1)
	require.Len(t, cfg.Commands[0].Args, 1)
	assert.Equal(t, "id", cfg.Commands[0].Args[0].Name)
	assert.Equal(t, "int", cfg.Commands[0].Args[0].Type)
	assert.True(t, cfg.Commands[0].Args[0].Required)
}

// TestLoad_SpaceYAMLRejected: space-indented YAML is not valid JSON, so it
// reaches yaml-fixed, which rejects spaces used as indentation.
func TestLoad_SpaceYAMLRejected(t *testing.T) {
	_, err := loadFrom(t, "api.yaml",
		"name: x\ncommand: echo\ncommands:\n  - name: g\n    entry: {}\n")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "indent")
}

func TestLoad_RejectsUnknownField(t *testing.T) {
	_, err := loadFrom(t, "api.yaml",
		"name: x\ncommand: echo\nbogus: 1\ncommands:\n\t- name: g\n\t  entry: {}\n")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "bogus")
}

// TestLoad_CommandSequence proves Cmd.UnmarshalJSON (array form) still fires
// through the tab-YAML -> JSON pipeline.
func TestLoad_CommandSequence(t *testing.T) {
	cfg, err := loadFrom(t, "api.yaml",
		"name: x\n"+
			"commands:\n"+
			"\t- name: g\n"+
			"\t  command:\n"+
			"\t\t- echo\n"+
			"\t\t- hi\n"+
			"\t  entry: {}\n")
	require.NoError(t, err)
	require.Len(t, cfg.Commands, 1)
	leaf := cfg.Commands[0]
	require.NotNil(t, leaf.Command)
	assert.False(t, leaf.Command.Shell)
	assert.Equal(t, []string{"echo", "hi"}, leaf.Command.Argv)
}

// TestLoad_FormatInlineAndNamed proves FormatRef.UnmarshalJSON (string name vs
// inline object) still fires through the tab-YAML -> JSON pipeline.
func TestLoad_FormatInlineAndNamed(t *testing.T) {
	cfg, err := loadFrom(t, "api.yaml",
		"name: x\n"+
			"command: echo\n"+
			"formats:\n"+
			"\tu:\n"+
			"\t\tviews:\n"+
			"\t\t\t- name: v\n"+
			"\t\t\t  default: true\n"+
			"\t\t\t  template: '{{.data}}'\n"+
			"commands:\n"+
			"\t- name: named\n"+
			"\t  format: u\n"+
			"\t  entry: {}\n"+
			"\t- name: inline\n"+
			"\t  format:\n"+
			"\t\tviews:\n"+
			"\t\t\t- name: w\n"+
			"\t\t\t  default: true\n"+
			"\t\t\t  template: '{{.data}}'\n"+
			"\t  entry: {}\n")
	require.NoError(t, err)
	require.Len(t, cfg.Commands, 2)
	assert.Equal(t, "u", cfg.Commands[0].Format.Name)
	require.NotNil(t, cfg.Commands[1].Format.Inline)
	require.Len(t, cfg.Commands[1].Format.Inline.Views, 1)
	assert.Equal(t, "w", cfg.Commands[1].Format.Inline.Views[0].Name)
}

func TestLoad_NumberAndBoolDefaults(t *testing.T) {
	cfg, err := loadFrom(t, "api.yaml",
		"name: x\n"+
			"command: echo\n"+
			"commands:\n"+
			"\t- name: g\n"+
			"\t  flags:\n"+
			"\t\t- name: limit\n"+
			"\t\t  type: int\n"+
			"\t\t  default: 30\n"+
			"\t\t- name: verbose\n"+
			"\t\t  type: bool\n"+
			"\t\t  default: true\n"+
			"\t  entry: {}\n")
	require.NoError(t, err)
	require.Len(t, cfg.Commands[0].Flags, 2)
	assert.Equal(t, float64(30), cfg.Commands[0].Flags[0].Default)
	assert.Equal(t, true, cfg.Commands[0].Flags[1].Default)
}
