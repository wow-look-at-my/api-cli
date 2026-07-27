package main

import (
	"bytes"
	"fmt"
	"io"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

func TestDocsCommand_Schema(t *testing.T) {
	var out bytes.Buffer
	prev := execStdout
	execStdout = &out
	t.Cleanup(func() { execStdout = prev })

	cmd := docsCommand()
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"schema"})
	require.NoError(t, cmd.Execute())

	assert.Contains(t, out.String(), "xs:schema")
	assert.Contains(t, out.String(), `name="config"`)
	assert.Equal(t, schemaDoc, out.String())
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
	assert.Contains(t, out.String(), "<config")

	// The example must load and validate.
	cfg, err := parseConfigXML([]byte(out.String()))
	require.NoError(t, err)
	assert.Equal(t, "apicli", cfg.Name)
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

func TestDocsCommand_NoConfigSchema(t *testing.T) {
	chdir(t, t.TempDir())

	var out bytes.Buffer
	prev := execStdout
	execStdout = &out
	t.Cleanup(func() { execStdout = prev })

	var errBuf bytes.Buffer
	code := run([]string{"docs", "schema"}, &errBuf)

	assert.Equal(t, 0, code)
	assert.Contains(t, out.String(), "xs:schema")
}

func TestIsDocsInvocation(t *testing.T) {
	tests := []struct {
		argv []string
		want bool
	}{
		{[]string{"docs"}, true},
		{[]string{"docs", "schema"}, true},
		{[]string{"docs", "example"}, true},
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
