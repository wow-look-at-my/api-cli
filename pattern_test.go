package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- pattern= on the MCP path ---

// The tool schema states the pattern, so a caller reads the shape of a legal
// value instead of seeing a bare string.
func TestBuildToolInputSchema_CarriesThePattern(t *testing.T) {
	node := Command{Args: []Arg{
		{Name: "post", Pattern: `^[0-9]+$`},
		{Name: "count", Type: "int", Pattern: `^[0-9]+$`},
		{Name: "paths", Variadic: true, Pattern: `^[A-Za-z0-9._-]+$`},
		{Name: "free"},
	}}
	props := buildToolInputSchema(node)["properties"].(map[string]any)

	assert.Equal(t, `^[0-9]+$`, props["post"].(map[string]any)["pattern"])
	assert.Equal(t, map[string]any{"type": "string", "pattern": `^[A-Za-z0-9._-]+$`},
		props["paths"].(map[string]any)["items"])

	_, onInt := props["count"].(map[string]any)["pattern"]
	assert.False(t, onInt, "an integer property has no string pattern to state")
	_, onFree := props["free"].(map[string]any)["pattern"]
	assert.False(t, onFree)
}

// A traversal value the CLI rejects must not reach the run over MCP either.
func TestMCPGatherArgs_RejectsAValueThePatternExcludes(t *testing.T) {
	node := Command{Args: []Arg{{Name: "post", Pattern: `^[0-9]+$`}}}

	_, err := mcpGatherArgs(node, map[string]any{"post": "../../etc/passwd"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), `arg "post"`)
	assert.Contains(t, err.Error(), `^[0-9]+$`)

	out, err := mcpGatherArgs(node, map[string]any{"post": "42"})
	require.NoError(t, err)
	assert.Equal(t, "42", out["post"])
}

// An omitted optional arg holds its unset value, which no pattern has to match.
func TestMCPGatherArgs_SkipsAnOmittedArg(t *testing.T) {
	node := Command{Args: []Arg{{Name: "post", Pattern: `^[0-9]+$`}}}
	out, err := mcpGatherArgs(node, map[string]any{})
	require.NoError(t, err)
	assert.Equal(t, "", out["post"])
}

// Each element of a variadic arg carries the pattern, the same way the CLI
// validator applies it to every supplied positional.
func TestMCPGatherArgs_ChecksEveryVariadicElement(t *testing.T) {
	node := Command{Args: []Arg{{Name: "ids", Variadic: true, Pattern: `^[0-9]+$`}}}

	_, err := mcpGatherArgs(node, map[string]any{"ids": []any{"1", "../x"}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), `"../x"`)

	out, err := mcpGatherArgs(node, map[string]any{"ids": []any{"1", "2"}})
	require.NoError(t, err)
	assert.Equal(t, []string{"1", "2"}, out["ids"])
}

// An int arg's pattern reads the value as text, so a negative number a pattern
// excludes fails before the conversion.
func TestMCPGatherArgs_ChecksAnIntArgAsText(t *testing.T) {
	node := Command{Args: []Arg{{Name: "n", Type: "int", Pattern: `^[0-9]+$`}}}

	_, err := mcpGatherArgs(node, map[string]any{"n": -3})
	require.Error(t, err)

	out, err := mcpGatherArgs(node, map[string]any{"n": float64(7)})
	require.NoError(t, err)
	assert.Equal(t, 7, out["n"])
}

// The whole MCP leaf path refuses the call, so nothing runs.
func TestMCPExecLeaf_RefusesAValueThePatternExcludes(t *testing.T) {
	cfg, err := parseConfigXML([]byte(`<config name="t">
		<command name="show" description="s">
			<arg name="post" pattern="^[0-9]+$"/>
			<run>
				<argv>printf</argv>
				<argv>ran:<value name="arg.post"/></argv>
			</run>
		</command>
	</config>`))
	require.NoError(t, err)
	require.NoError(t, validate(cfg))

	leaves := collectMCPLeaves(cfg.Commands, mcpInherit{vars: cfg.Vars})
	require.Len(t, leaves, 1)

	out, isErr := mcpExecLeaf(&leaves[0], map[string]any{"post": "../../etc/passwd"})
	assert.True(t, isErr)
	assert.NotContains(t, out, "ran:")

	out, isErr = mcpExecLeaf(&leaves[0], map[string]any{"post": "5"})
	assert.False(t, isErr)
	assert.Contains(t, out, "ran:5")
}
