package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"testing"

	"github.com/wow-look-at-my/testify/assert"
	"github.com/wow-look-at-my/testify/require"
)

func TestDocsCommand_PrintsReadme(t *testing.T) {
	var out bytes.Buffer
	prev := execStdout
	execStdout = &out
	t.Cleanup(func() { execStdout = prev })

	cmd := docsCommand()
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	require.NoError(t, cmd.Execute())

	assert.Contains(t, out.String(), "# api-cli")
	assert.Equal(t, readmeDoc, out.String())
}

func TestDocsCommand_SchemaFull(t *testing.T) {
	var out bytes.Buffer
	prev := execStdout
	execStdout = &out
	t.Cleanup(func() { execStdout = prev })

	cmd := docsCommand()
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"schema"})
	require.NoError(t, cmd.Execute())

	assert.Contains(t, out.String(), `"$schema"`)
	assert.Equal(t, schemaDoc, out.String())
}

func TestDocsCommand_SchemaKey(t *testing.T) {
	var out bytes.Buffer
	prev := execStdout
	execStdout = &out
	t.Cleanup(func() { execStdout = prev })

	cmd := docsCommand()
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"schema", "flag"})
	require.NoError(t, cmd.Execute())

	result := out.String()
	assert.NotContains(t, result, `"$schema"`)

	var parsed map[string]any
	require.NoError(t, json.Unmarshal([]byte(result), &parsed))
	assert.Contains(t, parsed, "properties")
}

func TestDocsCommand_SchemaKeyFromProperties(t *testing.T) {
	var out bytes.Buffer
	prev := execStdout
	execStdout = &out
	t.Cleanup(func() { execStdout = prev })

	cmd := docsCommand()
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"schema", "name"})
	require.NoError(t, cmd.Execute())

	var parsed map[string]any
	require.NoError(t, json.Unmarshal([]byte(out.String()), &parsed))
	assert.Equal(t, "string", parsed["type"])
}

func TestDocsCommand_SchemaKeyNotFound(t *testing.T) {
	cmd := docsCommand()
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"schema", "nonexistent"})
	err := cmd.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown schema key")
	assert.Contains(t, err.Error(), "commandNode")
	assert.Contains(t, err.Error(), "flag")
}

func TestDocsCommand_Example(t *testing.T) {
	var out bytes.Buffer
	prev := execStdout
	execStdout = &out
	t.Cleanup(func() { execStdout = prev })

	cmd := docsCommand()
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"example"})
	require.NoError(t, cmd.Execute())

	assert.Equal(t, exampleDoc, out.String())

	var parsed map[string]any
	require.NoError(t, json.Unmarshal([]byte(out.String()), &parsed))
	assert.Contains(t, parsed, "name")
}

func TestDocsCommand_NoConfigRequired(t *testing.T) {
	chdir(t, t.TempDir())

	var out bytes.Buffer
	prev := execStdout
	execStdout = &out
	t.Cleanup(func() { execStdout = prev })

	var errBuf bytes.Buffer
	code := run([]string{"docs"}, &errBuf)

	assert.Equal(t, 0, code)
	assert.Contains(t, out.String(), "# api-cli")
	assert.NotContains(t, errBuf.String(), "no config found")
}

func TestDocsCommand_NoConfigSchemaKey(t *testing.T) {
	chdir(t, t.TempDir())

	var out bytes.Buffer
	prev := execStdout
	execStdout = &out
	t.Cleanup(func() { execStdout = prev })

	var errBuf bytes.Buffer
	code := run([]string{"docs", "schema", "commandNode"}, &errBuf)

	assert.Equal(t, 0, code)

	var parsed map[string]any
	require.NoError(t, json.Unmarshal([]byte(out.String()), &parsed))
	assert.Contains(t, parsed, "properties")
}

func TestIsDocsInvocation(t *testing.T) {
	tests := []struct {
		argv []string
		want bool
	}{
		{[]string{"docs"}, true},
		{[]string{"docs", "schema"}, true},
		{[]string{"docs", "schema", "flag"}, true},
		{[]string{"--config", "x", "docs"}, true},
		{[]string{"other"}, false},
		{[]string{"documentation"}, false},
		{nil, false},
		{[]string{}, false},
	}
	for _, tt := range tests {
		t.Run(fmt.Sprintf("%v", tt.argv), func(t *testing.T) {
			assert.Equal(t, tt.want, isDocsInvocation(tt.argv))
		})
	}
}
