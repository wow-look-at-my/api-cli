package main

import (
	"bytes"
	"io"
	"testing"

	"github.com/wow-look-at-my/testify/assert"
	"github.com/wow-look-at-my/testify/require"
)

func captureExecStreams(t *testing.T) (*bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	var out, err bytes.Buffer
	prevOut, prevErr := execStdout, execStderr
	execStdout = &out
	execStderr = &err
	t.Cleanup(func() {
		execStdout = prevOut
		execStderr = prevErr
	})
	return &out, &err
}

func TestDoExec_ShellFormEchoes(t *testing.T) {
	out, _ := captureExecStreams(t)
	c := &Cmd{Shell: true, Template: `echo hello-{{.arg.name}}`}
	data := map[string]any{"arg": map[string]any{"name": "world"}}
	code := doExec(c, data)
	assert.Equal(t, 0, code)
	assert.Equal(t, "hello-world\n", out.String())
}

func TestDoExec_ArgvFormEchoes(t *testing.T) {
	out, _ := captureExecStreams(t)
	c := &Cmd{Argv: []string{"echo", "{{.arg.a}}", "literal", "{{.arg.b}}"}}
	data := map[string]any{"arg": map[string]any{"a": "x", "b": "y z"}}
	code := doExec(c, data)
	assert.Equal(t, 0, code)
	// argv form keeps spaces inside an element as one arg.
	assert.Equal(t, "x literal y z\n", out.String())
}

func TestDoExec_PropagatesExitCode(t *testing.T) {
	captureExecStreams(t)
	c := &Cmd{Shell: true, Template: `exit 7`}
	code := doExec(c, map[string]any{})
	assert.Equal(t, 7, code)
}

func TestDoExec_MissingBinaryReturns127(t *testing.T) {
	captureExecStreams(t)
	c := &Cmd{Argv: []string{"/definitely/not/a/real/binary/xyz123"}}
	code := doExec(c, map[string]any{})
	assert.Equal(t, 127, code)
}

func TestDoExec_EmptyCmdReturns1(t *testing.T) {
	captureExecStreams(t)
	c := &Cmd{}
	code := doExec(c, map[string]any{})
	assert.Equal(t, 1, code)
}

func TestDoExec_RenderErrorReturns1(t *testing.T) {
	captureExecStreams(t)
	c := &Cmd{Shell: true, Template: `echo {{.broken`}
	code := doExec(c, map[string]any{})
	assert.Equal(t, 1, code)
}

func TestDoExec_ArgvShellNotInvoked(t *testing.T) {
	// Argv form doesn't go through a shell, so shell metacharacters in
	// rendered values stay literal — that's the safety guarantee.
	out, _ := captureExecStreams(t)
	c := &Cmd{Argv: []string{"echo", "{{.arg.x}}"}}
	data := map[string]any{"arg": map[string]any{"x": "`touch /tmp/injected`"}}
	code := doExec(c, data)
	require.Equal(t, 0, code)
	assert.Equal(t, "`touch /tmp/injected`\n", out.String())
}

func TestDoExec_ShellFormHonoursShellQuote(t *testing.T) {
	// With shell form and shellquote, shell metacharacters in values are
	// rendered as literal.
	out, _ := captureExecStreams(t)
	c := &Cmd{Shell: true, Template: `echo {{shellquote .arg.x}}`}
	data := map[string]any{"arg": map[string]any{"x": "`touch /tmp/injected`"}}
	code := doExec(c, data)
	require.Equal(t, 0, code)
	assert.Equal(t, "`touch /tmp/injected`\n", out.String())
}

func TestDoExec_StdinPassthrough(t *testing.T) {
	out, _ := captureExecStreams(t)
	prevIn := execStdin
	execStdin = bytes.NewBufferString("piped input\n")
	t.Cleanup(func() { execStdin = prevIn })
	c := &Cmd{Shell: true, Template: `cat`}
	code := doExec(c, map[string]any{})
	assert.Equal(t, 0, code)
	assert.Equal(t, "piped input\n", out.String())
}

func TestDoExec_ArgvSpread(t *testing.T) {
	// `spread` expands a slice into multiple argv slots.
	out, _ := captureExecStreams(t)
	c := &Cmd{Argv: []string{"echo", "{{spread .arg.files}}", "tail"}}
	data := map[string]any{"arg": map[string]any{"files": []string{"a", "b", "c"}}}
	code := doExec(c, data)
	require.Equal(t, 0, code)
	assert.Equal(t, "a b c tail\n", out.String())
}

func TestDoExec_ArgvSpreadEmpty(t *testing.T) {
	// Empty spread = zero argv slots; surrounding elements still pass through.
	out, _ := captureExecStreams(t)
	c := &Cmd{Argv: []string{"echo", "{{spread .arg.files}}", "only"}}
	data := map[string]any{"arg": map[string]any{"files": []string{}}}
	code := doExec(c, data)
	require.Equal(t, 0, code)
	assert.Equal(t, "only\n", out.String())
}

func TestDoExec_ArgvSpreadOnlyEmptyFails(t *testing.T) {
	// If spread yields zero slots and there are no other elements, the argv
	// is empty — a useful failure rather than running with no command.
	captureExecStreams(t)
	c := &Cmd{Argv: []string{"{{spread .arg.files}}"}}
	data := map[string]any{"arg": map[string]any{"files": []string{}}}
	code := doExec(c, data)
	assert.Equal(t, 1, code)
}

var _ io.Reader = (*bytes.Buffer)(nil) // keep io import live if unused by coverage
