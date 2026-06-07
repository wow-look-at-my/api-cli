package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Tests for the per-command/step stdin field. Stdin inherits down the tree like
// `cwd`: a node's own stdin, if non-empty, overrides any ancestor's. Steps
// inherit the leaf's effective stdin unless they override it. The stdin template
// is rendered against the same data context as the command it applies to.

func TestStdin_DoExecFeedsInput(t *testing.T) {
	out, _ := captureExecStreams(t)
	c := &Cmd{Shell: true, Template: `cat`}
	code := doExec(c, "", "hello\n", map[string]any{})
	require.Equal(t, 0, code)
	assert.Equal(t, "hello\n", out.String())
}

func TestStdin_CaptureExecFeedsInput(t *testing.T) {
	captureExecStreams(t)
	c := &Cmd{Shell: true, Template: `cat`}
	out, code := captureExec(c, "", "captured\n", map[string]any{})
	require.Equal(t, 0, code)
	assert.Equal(t, "captured\n", out)
}

func TestStdin_EmptyFallsBackToExecStdin(t *testing.T) {
	out, _ := captureExecStreams(t)
	prevIn := execStdin
	execStdin = bytes.NewBufferString("passthrough\n")
	t.Cleanup(func() { execStdin = prevIn })
	c := &Cmd{Shell: true, Template: `cat`}
	code := doExec(c, "", "", map[string]any{})
	require.Equal(t, 0, code)
	assert.Equal(t, "passthrough\n", out.String())
}

func TestStdin_OverridesExecStdin(t *testing.T) {
	out, _ := captureExecStreams(t)
	prevIn := execStdin
	execStdin = bytes.NewBufferString("should-not-appear\n")
	t.Cleanup(func() { execStdin = prevIn })
	c := &Cmd{Shell: true, Template: `cat`}
	code := doExec(c, "", "override\n", map[string]any{})
	require.Equal(t, 0, code)
	assert.Equal(t, "override\n", out.String())
}

func TestStdin_ArgvFormWithStdin(t *testing.T) {
	out, _ := captureExecStreams(t)
	c := &Cmd{Argv: []string{"cat"}}
	code := doExec(c, "", "argv-stdin\n", map[string]any{})
	require.Equal(t, 0, code)
	assert.Equal(t, "argv-stdin\n", out.String())
}

// --- End-to-end (via runLeaf) ---

func TestIntegration_LeafStdinLiteral(t *testing.T) {
	cfg := &Config{
		Name:	"t",
		Commands: []Command{{
			Name:		"echo",
			Stdin:		"hello\n",
			Command:	&Cmd{Argv: []string{"cat"}},
		}},
	}
	code, out := execCmd(t, cfg, "echo")
	require.Equal(t, 0, code)
	assert.Equal(t, "hello\n", out)
}

func TestIntegration_LeafStdinTemplated(t *testing.T) {
	cfg := &Config{
		Name:	"t",
		Commands: []Command{{
			Name:		"echo",
			Args:		[]Arg{{Name: "msg", Required: true}},
			Stdin:		"{{.arg.msg}}",
			Command:	&Cmd{Argv: []string{"cat"}},
		}},
	}
	code, out := execCmd(t, cfg, "echo", "templated-value")
	require.Equal(t, 0, code)
	assert.Equal(t, "templated-value", out)
}

func TestIntegration_StdinInheritsFromConfig(t *testing.T) {
	cfg := &Config{
		Name:		"t",
		Stdin:		"from-root\n",
		Command:	&Cmd{Argv: []string{"cat"}},
		Commands:	[]Command{{Name: "show"}},
	}
	code, out := execCmd(t, cfg, "show")
	require.Equal(t, 0, code)
	assert.Equal(t, "from-root\n", out)
}

func TestIntegration_StdinInheritsThroughGroup(t *testing.T) {
	cfg := &Config{
		Name:		"t",
		Command:	&Cmd{Argv: []string{"cat"}},
		Commands: []Command{{
			Name:	"outer",
			Stdin:	"from-group\n",
			Commands: []Command{
				{Name: "leaf"},
			},
		}},
	}
	code, out := execCmd(t, cfg, "outer", "leaf")
	require.Equal(t, 0, code)
	assert.Equal(t, "from-group\n", out)
}

func TestIntegration_LeafStdinOverridesAncestor(t *testing.T) {
	cfg := &Config{
		Name:		"t",
		Stdin:		"parent\n",
		Command:	&Cmd{Argv: []string{"cat"}},
		Commands: []Command{
			{Name: "from-parent"},
			{Name: "from-leaf", Stdin: "leaf\n"},
		},
	}
	code, out := execCmd(t, cfg, "from-parent")
	require.Equal(t, 0, code)
	assert.Equal(t, "parent\n", out)

	code, out = execCmd(t, cfg, "from-leaf")
	require.Equal(t, 0, code)
	assert.Equal(t, "leaf\n", out)
}

func TestIntegration_StepInheritsLeafStdin(t *testing.T) {
	cfg := &Config{
		Name:	"t",
		Commands: []Command{{
			Name:	"run",
			Stdin:	"step-input",
			Steps: []Step{{
				Name:		"load",
				Command:	&Cmd{Argv: []string{"cat"}},
			}},
			Command:	&Cmd{Shell: true, Template: `printf '%s' {{.result.load}}`},
		}},
	}
	code, out := execCmd(t, cfg, "run")
	require.Equal(t, 0, code)
	assert.Equal(t, "step-input", out)
}

func TestIntegration_StepStdinOverridesLeaf(t *testing.T) {
	cfg := &Config{
		Name:	"t",
		Commands: []Command{{
			Name:	"run",
			Stdin:	"leaf-stdin\n",
			Steps: []Step{{
				Name:		"s",
				Stdin:		"step-stdin",
				Command:	&Cmd{Argv: []string{"cat"}},
			}},
			Command:	&Cmd{Shell: true, Template: `printf '%s|' {{.result.s}} && cat`},
		}},
	}
	code, out := execCmd(t, cfg, "run")
	require.Equal(t, 0, code)
	assert.Equal(t, "step-stdin|leaf-stdin\n", out)
}

func TestIntegration_StdinRenderedWithArgs(t *testing.T) {
	cfg := &Config{
		Name:	"t",
		Commands: []Command{{
			Name:		"run",
			Args:		[]Arg{{Name: "body", Required: true}},
			Stdin:		"{{.arg.body}}",
			Command:	&Cmd{Argv: []string{"cat"}},
		}},
	}
	code, out := execCmd(t, cfg, "run", "dynamic-body")
	require.Equal(t, 0, code)
	assert.Equal(t, "dynamic-body", out)
}

func TestIntegration_StdinRenderErrorFails(t *testing.T) {
	cfg := &Config{
		Name:	"t",
		Commands: []Command{{
			Name:		"x",
			Stdin:		`{{.broken`,
			Command:	&Cmd{Shell: true, Template: `true`},
		}},
	}
	code, _, _ := execCmdFull(t, cfg, "x")
	assert.Equal(t, 1, code)
}

func TestIntegration_StdinEmptyPassesThrough(t *testing.T) {
	prev := execStdin
	execStdin = strings.NewReader("piped\n")
	t.Cleanup(func() { execStdin = prev })

	cfg := &Config{
		Name:		"t",
		Command:	&Cmd{Shell: true, Template: `cat`},
		Commands:	[]Command{{Name: "echo"}},
	}
	code, out := execCmd(t, cfg, "echo")
	require.Equal(t, 0, code)
	assert.Equal(t, "piped\n", out)
}

func TestIntegration_StdinWithJsonTemplate(t *testing.T) {
	cfg := &Config{
		Name:	"t",
		Commands: []Command{{
			Name:		"run",
			Flags:		[]Flag{{Name: "body", Required: true}},
			Stdin:		`{{.flag.body | toJson}}`,
			Command:	&Cmd{Argv: []string{"cat"}},
		}},
	}
	code, out := execCmd(t, cfg, "run", "--body", `hello "world"`)
	require.Equal(t, 0, code)
	assert.Equal(t, `"hello \"world\""`, out)
}

func TestIntegration_StepStdinTemplatedWithResult(t *testing.T) {
	cfg := &Config{
		Name:	"t",
		Commands: []Command{{
			Name:	"run",
			Steps: []Step{
				{
					Name:		"first",
					Command:	&Cmd{Shell: true, Template: `printf '{"key":"val"}'`},
				},
				{
					Name:		"second",
					Stdin:		"{{.result.first.key}}",
					Command:	&Cmd{Argv: []string{"cat"}},
				},
			},
			Command:	&Cmd{Shell: true, Template: `printf '%s' {{.result.second}}`},
		}},
	}
	code, out := execCmd(t, cfg, "run")
	require.Equal(t, 0, code)
	assert.Equal(t, "val", out)
}
