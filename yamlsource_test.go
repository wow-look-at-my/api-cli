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

func TestExpandLeadingTabs(t *testing.T) {
	// Leading tabs become spaces (2 each); a tab after content is left alone.
	in := "a:\n\tb: 1\n\t\tc: 2\nd:\te\n"
	want := "a:\n  b: 1\n    c: 2\nd:\te\n"
	assert.Equal(t, want, string(expandLeadingTabs([]byte(in), 2)))

	// Tab-free input is returned unchanged.
	assert.Equal(t, "x: 1\n", string(expandLeadingTabs([]byte("x: 1\n"), 2)))
}

func TestSourceToJSON_TypesAndKeys(t *testing.T) {
	j, err := sourceToJSON([]byte("a: 1\nb: true\nc: hi\nd:\n  - x\n  - y\n"))
	require.NoError(t, err)

	var m map[string]any
	require.NoError(t, json.Unmarshal(j, &m))
	assert.Equal(t, float64(1), m["a"])	// JSON numbers decode to float64
	assert.Equal(t, true, m["b"])
	assert.Equal(t, "hi", m["c"])
	assert.Equal(t, []any{"x", "y"}, m["d"])
}

func TestLoad_JSONStillLoads(t *testing.T) {
	cfg, err := loadFrom(t, "api.json",
		`{"name":"x","command":"echo hi","commands":[{"name":"go","entry":{}}]}`)
	require.NoError(t, err)
	assert.Equal(t, "x", cfg.Name)
	require.NotNil(t, cfg.Command)
	assert.True(t, cfg.Command.Shell)
	require.Len(t, cfg.Commands, 1)
}

// TestLoad_TabAndSpaceYAMLEquivalent is the heart of the feature: the same
// document indented with one tab per level and with two spaces per level must
// load to byte-identical configs.
func TestLoad_TabAndSpaceYAMLEquivalent(t *testing.T) {
	spaces := "name: demo\n" +
		"command: echo {{.var.x}}\n" +
		"vars:\n" +
		"  x: hello\n" +
		"commands:\n" +
		"  - name: get\n" +
		"    args:\n" +
		"      - name: id\n" +
		"        type: int\n" +
		"        required: true\n" +
		"    flags:\n" +
		"      - name: limit\n" +
		"        type: int\n" +
		"        default: 30\n" +
		"    entry:\n" +
		"      path: '/u/{{.arg.id}}'\n" +
		"      query:\n" +
		"        n: '{{.flag.limit}}'\n"

	tabs := "name: demo\n" +
		"command: echo {{.var.x}}\n" +
		"vars:\n" +
		"\tx: hello\n" +
		"commands:\n" +
		"\t- name: get\n" +
		"\t\targs:\n" +
		"\t\t\t- name: id\n" +
		"\t\t\t\ttype: int\n" +
		"\t\t\t\trequired: true\n" +
		"\t\tflags:\n" +
		"\t\t\t- name: limit\n" +
		"\t\t\t\ttype: int\n" +
		"\t\t\t\tdefault: 30\n" +
		"\t\tentry:\n" +
		"\t\t\tpath: '/u/{{.arg.id}}'\n" +
		"\t\t\tquery:\n" +
		"\t\t\t\tn: '{{.flag.limit}}'\n"

	cfgSpace, err := loadFrom(t, "s.yaml", spaces)
	require.NoError(t, err)
	cfgTab, err := loadFrom(t, "t.yaml", tabs)
	require.NoError(t, err)
	assert.Equal(t, cfgSpace, cfgTab)
}

func TestLoad_RejectsUnknownField(t *testing.T) {
	_, err := loadFrom(t, "bad.yaml",
		"name: x\ncommand: echo\nbogus: 1\ncommands:\n  - name: g\n    entry: {}\n")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "bogus")
}

// TestLoad_CommandSequence proves Cmd.UnmarshalJSON (array form) still fires
// through the YAML->JSON pipeline.
func TestLoad_CommandSequence(t *testing.T) {
	cfg, err := loadFrom(t, "x.yaml",
		"name: x\n"+
			"commands:\n"+
			"  - name: g\n"+
			"    command:\n"+
			"      - echo\n"+
			"      - hi\n"+
			"    entry: {}\n")
	require.NoError(t, err)
	require.Len(t, cfg.Commands, 1)
	leaf := cfg.Commands[0]
	require.NotNil(t, leaf.Command)
	assert.False(t, leaf.Command.Shell)
	assert.Equal(t, []string{"echo", "hi"}, leaf.Command.Argv)
}

// TestLoad_FormatInlineAndNamed proves FormatRef.UnmarshalJSON (string name vs
// inline object) still fires through the YAML->JSON pipeline.
func TestLoad_FormatInlineAndNamed(t *testing.T) {
	cfg, err := loadFrom(t, "f.yaml",
		"name: x\n"+
			"command: echo\n"+
			"formats:\n"+
			"  u:\n"+
			"    views:\n"+
			"      - name: v\n"+
			"        default: true\n"+
			"        template: '{{.data}}'\n"+
			"commands:\n"+
			"  - name: named\n"+
			"    format: u\n"+
			"    entry: {}\n"+
			"  - name: inline\n"+
			"    format:\n"+
			"      views:\n"+
			"        - name: w\n"+
			"          default: true\n"+
			"          template: '{{.data}}'\n"+
			"    entry: {}\n")
	require.NoError(t, err)
	require.Len(t, cfg.Commands, 2)
	assert.Equal(t, "u", cfg.Commands[0].Format.Name)
	require.NotNil(t, cfg.Commands[1].Format.Inline)
	require.Len(t, cfg.Commands[1].Format.Inline.Views, 1)
	assert.Equal(t, "w", cfg.Commands[1].Format.Inline.Views[0].Name)
}

func TestLoad_NumberAndBoolDefaults(t *testing.T) {
	cfg, err := loadFrom(t, "n.yaml",
		"name: x\n"+
			"command: echo\n"+
			"commands:\n"+
			"  - name: g\n"+
			"    flags:\n"+
			"      - name: limit\n"+
			"        type: int\n"+
			"        default: 30\n"+
			"      - name: verbose\n"+
			"        type: bool\n"+
			"        default: true\n"+
			"    entry: {}\n")
	require.NoError(t, err)
	require.Len(t, cfg.Commands[0].Flags, 2)
	assert.Equal(t, float64(30), cfg.Commands[0].Flags[0].Default)
	assert.Equal(t, true, cfg.Commands[0].Flags[1].Default)
}
