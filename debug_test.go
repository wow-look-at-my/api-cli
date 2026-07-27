package main

import (
	"strings"
	"testing"

	"github.com/wow-look-at-my/testify/assert"
)

func TestVerbose_ShowsCommandAndExitCode(t *testing.T) {
	cfg := &Config{
		Name:    "t",
		Command: &Cmd{Shell: true, Template: `echo hello`},
		Commands: []Command{{
			Name: "greet",
		}},
	}
	code, stdout, stderr := execCmdFull(t, cfg, "--verbose", "greet")
	assert.Equal(t, 0, code)
	assert.Equal(t, "hello\n", stdout)
	assert.Contains(t, stderr, "[verbose]")
	assert.Contains(t, stderr, "exit code 0")
	assert.Contains(t, stderr, "echo hello")
}

func TestDebug_ImpliesVerbose(t *testing.T) {
	cfg := &Config{
		Name:    "t",
		Command: &Cmd{Shell: true, Template: `echo hi`},
		Commands: []Command{{
			Name: "say",
		}},
	}
	code, _, stderr := execCmdFull(t, cfg, "--debug", "say")
	assert.Equal(t, 0, code)
	assert.Contains(t, stderr, "[verbose]")
	assert.Contains(t, stderr, "[debug]")
}

func TestVerbose_ShowsStepWhenPredicate(t *testing.T) {
	cfg := &Config{
		Name:    "t",
		Command: &Cmd{Shell: true, Template: `echo done`},
		Commands: []Command{{
			Name: "run",
			Steps: []Step{
				{
					Name:    "maybe",
					When:    "{{if .arg.skip}}false{{else}}true{{end}}",
					Command: &Cmd{Shell: true, Template: `echo step-ran`},
				},
			},
			Args: []Arg{{Name: "skip", Type: "string"}},
		}},
	}

	code, _, stderr := execCmdFull(t, cfg, "--verbose", "run", "yes")
	assert.Equal(t, 0, code)
	assert.Contains(t, stderr, `step "maybe": when`)
	assert.Contains(t, stderr, "skipped")
}

func TestVerbose_ShowsStepExecution(t *testing.T) {
	cfg := &Config{
		Name:    "t",
		Command: &Cmd{Shell: true, Template: `echo final`},
		Commands: []Command{{
			Name: "run",
			Steps: []Step{{
				Name:    "lookup",
				Command: &Cmd{Shell: true, Template: `echo step-output`},
			}},
		}},
	}

	code, stdout, stderr := execCmdFull(t, cfg, "--verbose", "run")
	assert.Equal(t, 0, code)
	assert.Equal(t, "final\n", stdout)
	assert.Contains(t, stderr, `step "lookup": executing`)
	assert.Contains(t, stderr, `step "lookup": exit code 0`)
}

func TestDebug_ShowsStepStdout(t *testing.T) {
	cfg := &Config{
		Name:    "t",
		Command: &Cmd{Shell: true, Template: `echo final`},
		Commands: []Command{{
			Name: "run",
			Steps: []Step{{
				Name:    "lookup",
				Command: &Cmd{Shell: true, Template: `echo captured-value`},
			}},
		}},
	}

	code, _, stderr := execCmdFull(t, cfg, "--debug", "run")
	assert.Equal(t, 0, code)
	assert.Contains(t, stderr, "captured-value")
	assert.Contains(t, stderr, `step "lookup": stdout`)
}

func TestDebug_ShowsDataContext(t *testing.T) {
	cfg := &Config{
		Name:    "t",
		Command: &Cmd{Shell: true, Template: `echo {{.arg.name}}`},
		Commands: []Command{{
			Name: "greet",
			Args: []Arg{{Name: "name", Type: "string", Required: true}},
		}},
	}

	code, _, stderr := execCmdFull(t, cfg, "--debug", "greet", "ada")
	assert.Equal(t, 0, code)
	assert.Contains(t, stderr, "[debug]")
	assert.Contains(t, stderr, "data context")
	assert.Contains(t, stderr, "ada")
}

func TestNoFlags_NoDebugOutput(t *testing.T) {
	cfg := &Config{
		Name:    "t",
		Command: &Cmd{Shell: true, Template: `echo quiet`},
		Commands: []Command{{
			Name: "run",
		}},
	}

	code, stdout, stderr := execCmdFull(t, cfg, "run")
	assert.Equal(t, 0, code)
	assert.Equal(t, "quiet\n", stdout)
	assert.NotContains(t, stderr, "[verbose]")
	assert.NotContains(t, stderr, "[debug]")
}

func TestVerbose_OutputGoesToStderr(t *testing.T) {
	cfg := &Config{
		Name:    "t",
		Command: &Cmd{Shell: true, Template: `echo stdout-only`},
		Commands: []Command{{
			Name: "run",
		}},
	}

	code, stdout, _ := execCmdFull(t, cfg, "--verbose", "run")
	assert.Equal(t, 0, code)
	assert.Equal(t, "stdout-only\n", stdout)
	assert.NotContains(t, stdout, "[verbose]")
}

func TestVerbose_ShowsNonZeroExitCode(t *testing.T) {
	cfg := &Config{
		Name:    "t",
		Command: &Cmd{Shell: true, Template: `exit 42`},
		Commands: []Command{{
			Name: "fail",
		}},
	}

	code, _, stderr := execCmdFull(t, cfg, "--verbose", "fail")
	assert.Equal(t, 42, code)
	assert.Contains(t, stderr, "exit code 42")
}

func TestVerbose_ShowsPreconditionEval(t *testing.T) {
	cfg := &Config{
		Name:    "t",
		Command: &Cmd{Shell: true, Template: `echo ok`},
		Commands: []Command{{
			Name:          "check",
			Preconditions: []string{`{{if .arg.bad}}nope{{end}}`},
			Args:          []Arg{{Name: "bad", Type: "string"}},
		}},
	}

	code, _, stderr := execCmdFull(t, cfg, "--verbose", "check", "yes")
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "precondition[0]")
}

func TestVerbose_FormatDecision(t *testing.T) {
	cfg := &Config{
		Name:    "t",
		Command: &Cmd{Shell: true, Template: `echo '{"x":1}'`},
		Formats: map[string]*Format{
			"f": {
				When: "true",
				Views: []View{{
					Name:     "v",
					Template: "formatted: {{.data.x}}",
				}},
			},
		},
		Commands: []Command{{
			Name:   "show",
			Format: &FormatRef{Name: "f"},
		}},
	}

	code, _, stderr := execCmdFull(t, cfg, "--verbose", "--format=always", "show")
	assert.Equal(t, 0, code)
	assert.Contains(t, stderr, "format:")
}

func TestDebug_NoVerboseLines(t *testing.T) {
	cfg := &Config{
		Name:    "t",
		Command: &Cmd{Shell: true, Template: `echo x`},
		Commands: []Command{{
			Name: "run",
		}},
	}

	_, _, stderr := execCmdFull(t, cfg, "--debug", "run")
	for _, line := range strings.Split(stderr, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		hasPrefix := strings.HasPrefix(line, "[verbose]") || strings.HasPrefix(line, "[debug]")
		assert.True(t, hasPrefix, "unexpected stderr line without debug/verbose prefix: %q", line)
	}
}
