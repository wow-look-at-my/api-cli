package main

import (
	"bytes"
	"io"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
	code := doExec(c, "", "", data)
	assert.Equal(t, 0, code)
	assert.Equal(t, "hello-world\n", out.String())
}

func TestDoExec_ArgvFormEchoes(t *testing.T) {
	out, _ := captureExecStreams(t)
	c := &Cmd{Argv: []string{"echo", "{{.arg.a}}", "literal", "{{.arg.b}}"}}
	data := map[string]any{"arg": map[string]any{"a": "x", "b": "y z"}}
	code := doExec(c, "", "", data)
	assert.Equal(t, 0, code)
	// argv form keeps spaces inside an element as one arg.
	assert.Equal(t, "x literal y z\n", out.String())
}

func TestDoExec_PropagatesExitCode(t *testing.T) {
	captureExecStreams(t)
	c := &Cmd{Shell: true, Template: `exit 7`}
	code := doExec(c, "", "", map[string]any{})
	assert.Equal(t, 7, code)
}

func TestDoExec_MissingBinaryReturns127(t *testing.T) {
	captureExecStreams(t)
	c := &Cmd{Argv: []string{"/definitely/not/a/real/binary/xyz123"}}
	code := doExec(c, "", "", map[string]any{})
	assert.Equal(t, 127, code)
}

func TestDoExec_EmptyCmdReturns1(t *testing.T) {
	captureExecStreams(t)
	c := &Cmd{}
	code := doExec(c, "", "", map[string]any{})
	assert.Equal(t, 1, code)
}

func TestDoExec_RenderErrorReturns1(t *testing.T) {
	captureExecStreams(t)
	c := &Cmd{Shell: true, Template: `echo {{.broken`}
	code := doExec(c, "", "", map[string]any{})
	assert.Equal(t, 1, code)
}

func TestDoExec_ArgvShellNotInvoked(t *testing.T) {
	// Argv form doesn't go through a shell, so shell metacharacters in
	// rendered values stay literal — that's the safety guarantee.
	out, _ := captureExecStreams(t)
	c := &Cmd{Argv: []string{"echo", "{{.arg.x}}"}}
	data := map[string]any{"arg": map[string]any{"x": "`touch /tmp/injected`"}}
	code := doExec(c, "", "", data)
	require.Equal(t, 0, code)
	assert.Equal(t, "`touch /tmp/injected`\n", out.String())
}

func TestDoExec_ShellFormHonoursShellQuote(t *testing.T) {
	// With shell form and shellquote, shell metacharacters in values are
	// rendered as literal.
	out, _ := captureExecStreams(t)
	c := &Cmd{Shell: true, Template: `echo {{shellquote .arg.x}}`}
	data := map[string]any{"arg": map[string]any{"x": "`touch /tmp/injected`"}}
	code := doExec(c, "", "", data)
	require.Equal(t, 0, code)
	assert.Equal(t, "`touch /tmp/injected`\n", out.String())
}

func TestDoExec_StdinPassthrough(t *testing.T) {
	out, _ := captureExecStreams(t)
	prevIn := execStdin
	execStdin = bytes.NewBufferString("piped input\n")
	t.Cleanup(func() { execStdin = prevIn })
	c := &Cmd{Shell: true, Template: `cat`}
	code := doExec(c, "", "", map[string]any{})
	assert.Equal(t, 0, code)
	assert.Equal(t, "piped input\n", out.String())
}

func TestDoExec_ArgvSpread(t *testing.T) {
	// `spread` expands a slice into multiple argv slots.
	out, _ := captureExecStreams(t)
	c := &Cmd{Argv: []string{"echo", "{{spread .arg.files}}", "tail"}}
	data := map[string]any{"arg": map[string]any{"files": []string{"a", "b", "c"}}}
	code := doExec(c, "", "", data)
	require.Equal(t, 0, code)
	assert.Equal(t, "a b c tail\n", out.String())
}

func TestDoExec_ArgvSpreadEmpty(t *testing.T) {
	// Empty spread = zero argv slots; surrounding elements still pass through.
	out, _ := captureExecStreams(t)
	c := &Cmd{Argv: []string{"echo", "{{spread .arg.files}}", "only"}}
	data := map[string]any{"arg": map[string]any{"files": []string{}}}
	code := doExec(c, "", "", data)
	require.Equal(t, 0, code)
	assert.Equal(t, "only\n", out.String())
}

func TestDoExec_ArgvSpreadOnlyEmptyFails(t *testing.T) {
	// If spread yields zero slots and there are no other elements, the argv
	// is empty — a useful failure rather than running with no command.
	captureExecStreams(t)
	c := &Cmd{Argv: []string{"{{spread .arg.files}}"}}
	data := map[string]any{"arg": map[string]any{"files": []string{}}}
	code := doExec(c, "", "", data)
	assert.Equal(t, 1, code)
}

func TestDoExec_ShellSpread(t *testing.T) {
	out, _ := captureExecStreams(t)
	c := &Cmd{Shell: true, Template: `printf '%s\n' {{spread .rest}}`}
	data := map[string]any{"rest": []string{"--foo=bar,[baz]", "-x", "file.txt"}}
	code := doExec(c, "", "", data)
	require.Equal(t, 0, code)
	assert.Equal(t, "--foo=bar,[baz]\n-x\nfile.txt\n", out.String())
}

func TestDoExec_ShellSpreadEmpty(t *testing.T) {
	out, _ := captureExecStreams(t)
	c := &Cmd{Shell: true, Template: `echo prefix{{spread .rest}} suffix`}
	data := map[string]any{"rest": []string{}}
	code := doExec(c, "", "", data)
	require.Equal(t, 0, code)
	assert.Equal(t, "prefix suffix\n", out.String())
}

func TestDoExec_ShellSpreadSingleQuotesInArg(t *testing.T) {
	out, _ := captureExecStreams(t)
	c := &Cmd{Shell: true, Template: `printf '%s\n' {{spread .rest}}`}
	data := map[string]any{"rest": []string{"it's", "a 'test'"}}
	code := doExec(c, "", "", data)
	require.Equal(t, 0, code)
	assert.Equal(t, "it's\na 'test'\n", out.String())
}

func TestExpandSpreadForShell(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  string
	}{
		{"no spread", "echo hello", "echo hello"},
		{"single element", "cmd \x00arg\x01 tail", "cmd 'arg' tail"},
		{"multiple elements", "cmd \x00a\x00b\x00c\x01", "cmd 'a' 'b' 'c'"},
		{"empty spread", "cmd \x00\x01 tail", "cmd  tail"},
		{"metacharacters", "cmd \x00--flag=val,[baz]\x00-x\x01", "cmd '--flag=val,[baz]' '-x'"},
		{"two spreads", "cmd \x00a\x00b\x01 mid \x00c\x01", "cmd 'a' 'b' mid 'c'"},
		{"embedded single quote", "cmd \x00it's\x01", "cmd 'it'\\''s'"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, expandSpreadForShell(tc.input))
		})
	}
}

func TestParseResult_HexHash(t *testing.T) {
	// MD5/SHA hashes that start with digits must stay strings, not be
	// partially parsed as JSON numbers.
	hashes := []string{
		"3bf86b7e484a4c355f49b3e4c9d8a17c",
		"d41d8cd98f00b204e9800998ecf8427e",
		"9f86d081884c7d659a2feaa0c55ad015",
		"0e123456789abcdef0123456789abcdef",
	}
	for _, h := range hashes {
		got := parseResult(h)
		assert.Equal(t, h, got, "hash %q must stay a string", h)
	}
}

func TestParseResult_ValidJSON(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  any
	}{
		{"object", `{"a":1}`, map[string]any{"a": int64(1)}},
		{"array", `[1,2]`, []any{int64(1), int64(2)}},
		{"bare int", `42`, int64(42)},
		{"bare float", `3.14`, 3.14},
		{"bare string", `"hello"`, "hello"},
		{"bare true", `true`, true},
		{"bare false", `false`, false},
		{"null", `null`, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, parseResult(tc.input))
		})
	}
}

func TestParseResult_NonJSON(t *testing.T) {
	cases := []struct {
		name  string
		input string
	}{
		{"plain text", "hello world"},
		{"hash starting with digit", "3bf86b7e484a4c355f49b3e4c9d8a17c"},
		{"number followed by text", "42abc"},
		{"scientific notation prefix", "0e123abcdef"},
		{"number with trailing brace", "42}"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := parseResult(tc.input)
			assert.Equal(t, tc.input, got)
		})
	}
}

func TestParseResult_TrailingWhitespace(t *testing.T) {
	got := parseResult("  42  \n")
	assert.Equal(t, int64(42), got)
}

var _ io.Reader = (*bytes.Buffer)(nil) // keep io import live if unused by coverage
