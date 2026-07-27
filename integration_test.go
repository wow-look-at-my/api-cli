package main

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// execCmd builds the root from cfg, sets argv, executes, and returns the
// process exit code (as main would compute it), plus anything written to
// the exec'd child's stdout.
func execCmd(t *testing.T, cfg *Config, argv ...string) (int, string) {
	t.Helper()
	code, out, _ := execCmdFull(t, cfg, argv...)
	return code, out
}

// execCmdFull is like execCmd but also returns what was written to stderr.
func execCmdFull(t *testing.T, cfg *Config, argv ...string) (int, string, string) {
	t.Helper()
	require.NoError(t, validate(cfg))

	var out, errBuf bytes.Buffer
	prevOut, prevErr := execStdout, execStderr
	execStdout = &out
	execStderr = &errBuf
	t.Cleanup(func() {
		execStdout = prevOut
		execStderr = prevErr
	})
	prevCode := exitCode
	exitCode = 0
	t.Cleanup(func() { exitCode = prevCode })
	prevVerbose, prevDebug := verboseMode, debugMode
	verboseMode, debugMode = false, false
	t.Cleanup(func() { verboseMode = prevVerbose; debugMode = prevDebug })

	root := newRoot(cfg)
	root.SetOut(io.Discard) // suppress cobra's own output in tests
	root.SetErr(io.Discard)
	root.SetArgs(argv)
	if err := root.Execute(); err != nil {
		return 1, out.String(), errBuf.String()
	}
	return exitCode, out.String(), errBuf.String()
}

func TestIntegration_ShellFormRendersEntry(t *testing.T) {
	cfg := &Config{
		Name:    "t",
		Vars:    map[string]any{"greeting": "hello"},
		Command: &Cmd{Shell: true, Template: `printf '%s %s %s' {{.var.greeting}} {{.entry.name}} {{.arg.id}}`},
		Commands: []Command{{
			Name:  "greet",
			Args:  []Arg{{Name: "id", Type: "string", Required: true}},
			Entry: json.RawMessage(`{"name":"ada"}`),
		}},
	}
	code, out := execCmd(t, cfg, "greet", "42")
	assert.Equal(t, 0, code)
	assert.Equal(t, "hello ada 42", out)
}

func TestIntegration_ArgvFormEcho(t *testing.T) {
	cfg := &Config{
		Name: "t",
		Commands: []Command{{
			Name: "say",
			Flags: []Flag{
				{Name: "msg", Short: "m", Type: "string", Required: true},
			},
			Command: &Cmd{Argv: []string{"echo", "{{.flag.msg}}"}},
		}},
	}
	code, out := execCmd(t, cfg, "say", "-m", "hi there")
	assert.Equal(t, 0, code)
	assert.Equal(t, "hi there\n", out)
}

func TestIntegration_LeafOverridesRootCommand(t *testing.T) {
	cfg := &Config{
		Name:    "t",
		Command: &Cmd{Shell: true, Template: `echo root`},
		Commands: []Command{
			{Name: "a"}, // inherits root
			{Name: "b", Command: &Cmd{Shell: true, Template: `echo leaf-override`}}, // overrides
		},
	}
	code, out := execCmd(t, cfg, "a")
	require.Equal(t, 0, code)
	assert.Equal(t, "root\n", out)

	code, out = execCmd(t, cfg, "b")
	require.Equal(t, 0, code)
	assert.Equal(t, "leaf-override\n", out)
}

func TestIntegration_VarsInheritanceAndOverride(t *testing.T) {
	cfg := &Config{
		Name:    "t",
		Vars:    map[string]any{"who": "world", "mood": "happy"},
		Command: &Cmd{Shell: true, Template: `printf '%s %s' {{.var.who}} {{.var.mood}}`},
		Commands: []Command{{
			Name: "sub",
			Vars: map[string]any{"mood": "grumpy"},
			Commands: []Command{
				{Name: "greet"},
			},
		}},
	}
	code, out := execCmd(t, cfg, "sub", "greet")
	require.Equal(t, 0, code)
	assert.Equal(t, "world grumpy", out)
}

func TestIntegration_VarsCanBeTemplated(t *testing.T) {
	t.Setenv("API_CLI_TEST_HOST", "example.internal")
	cfg := &Config{
		Name:     "t",
		Vars:     map[string]any{"host": `{{.env.API_CLI_TEST_HOST}}`},
		Command:  &Cmd{Shell: true, Template: `echo {{.var.host}}`},
		Commands: []Command{{Name: "show"}},
	}
	code, out := execCmd(t, cfg, "show")
	require.Equal(t, 0, code)
	assert.Equal(t, "example.internal\n", out)
}

func TestIntegration_CurlExampleViaQuerystring(t *testing.T) {
	// Simulates the user's motivating example: render an HTTP-shaped entry
	// and use querystring + shellquote to produce a safe shell command.
	// We run `printf` instead of curl to keep the test hermetic.
	cfg := &Config{
		Name:    "t",
		Vars:    map[string]any{"base_url": "https://api.example.com/v1"},
		Command: &Cmd{Shell: true, Template: `printf '%s' {{shellquote (printf "%s%s%s" .var.base_url .entry.path (querystring .entry.query))}}`},
		Commands: []Command{{
			Name: "list",
			Flags: []Flag{
				{Name: "limit", Type: "int", Default: float64(10)},
			},
			Entry: json.RawMessage(`{"path":"/users","query":{"limit":"{{.flag.limit}}"}}`),
		}},
	}
	code, out := execCmd(t, cfg, "list", "--limit", "3")
	require.Equal(t, 0, code)
	assert.Contains(t, out, "https://api.example.com/v1/users?limit=3")
}

func TestIntegration_ExitCodePropagated(t *testing.T) {
	cfg := &Config{
		Name:     "t",
		Command:  &Cmd{Shell: true, Template: `exit 9`},
		Commands: []Command{{Name: "fail"}},
	}
	code, _ := execCmd(t, cfg, "fail")
	assert.Equal(t, 9, code)
}

func TestIntegration_StdinPassthrough(t *testing.T) {
	prev := execStdin
	execStdin = strings.NewReader("piped\n")
	t.Cleanup(func() { execStdin = prev })

	cfg := &Config{
		Name:     "t",
		Command:  &Cmd{Shell: true, Template: `cat`},
		Commands: []Command{{Name: "echo"}},
	}
	code, out := execCmd(t, cfg, "echo")
	require.Equal(t, 0, code)
	assert.Equal(t, "piped\n", out)
}

func TestIntegration_ExampleConfigLoads(t *testing.T) {
	// Sanity check: the shipped example validates cleanly.
	cfg, err := Load("api.example.xml")
	require.NoError(t, err)
	assert.NotEmpty(t, cfg.Name)
}

// --- Steps / result-reuse tests ---

func TestIntegration_StepResultAvailableInFinalEntry(t *testing.T) {
	// Step echoes JSON; the final command uses .result.first.value.
	cfg := &Config{
		Name: "t",
		Commands: []Command{{
			Name: "run",
			Steps: []Step{{
				Name:    "first",
				Command: &Cmd{Shell: true, Template: `printf '{"value":"hello"}'`},
			}},
			Command: &Cmd{Shell: true, Template: `echo {{.result.first.value}}`},
		}},
	}
	code, out := execCmd(t, cfg, "run")
	require.Equal(t, 0, code)
	assert.Equal(t, "hello\n", out)
}

func TestIntegration_StepResultChained(t *testing.T) {
	// Two steps: the second step uses the first's result in its entry, and the
	// final command uses both.
	cfg := &Config{
		Name: "t",
		Commands: []Command{{
			Name: "run",
			Steps: []Step{
				{
					Name:    "a",
					Command: &Cmd{Shell: true, Template: `printf '{"n":3}'`},
				},
				{
					Name:    "b",
					Command: &Cmd{Shell: true, Template: `printf '{"doubled":{{mul (int .result.a.n) 2}}}'`},
				},
			},
			Command: &Cmd{Shell: true, Template: `printf '%s-%s' {{.result.a.n}} {{.result.b.doubled}}`},
		}},
	}
	code, out := execCmd(t, cfg, "run")
	require.Equal(t, 0, code)
	assert.Equal(t, "3-6", out)
}

func TestIntegration_StepEntryRenderedWithResult(t *testing.T) {
	// A step's entry template can reference .result.* from prior steps.
	cfg := &Config{
		Name: "t",
		Commands: []Command{{
			Name: "run",
			Steps: []Step{
				{
					Name:    "lookup",
					Command: &Cmd{Shell: true, Template: `printf '{"id":42}'`},
				},
				{
					Name:    "detail",
					Entry:   json.RawMessage(`{"id":"{{.result.lookup.id}}"}`),
					Command: &Cmd{Shell: true, Template: `printf '{"found":{{.entry.id}}}'`},
				},
			},
			Command: &Cmd{Shell: true, Template: `echo {{.result.detail.found}}`},
		}},
	}
	code, out := execCmd(t, cfg, "run")
	require.Equal(t, 0, code)
	assert.Equal(t, "42\n", out)
}

func TestIntegration_StepInheritsAncestorCommand(t *testing.T) {
	// A step without its own command uses the leaf's effective command (same
	// inheritance rule as the leaf itself). Here neither the step nor the leaf
	// overrides the command, so both run the root template.
	cfg := &Config{
		Name:    "t",
		Command: &Cmd{Shell: true, Template: `printf '{"k":"{{.entry.v}}"}'`},
		Commands: []Command{{
			Name: "run",
			Steps: []Step{{
				Name:  "s",
				Entry: json.RawMessage(`{"v":"hello"}`),
				// No Command — uses root command.
			}},
			// No Command override — also uses root command.
			Entry: json.RawMessage(`{"v":"{{.result.s.k}}"}`),
		}},
	}
	code, out := execCmd(t, cfg, "run")
	require.Equal(t, 0, code)
	// Step: printf '{"k":"hello"}' → .result.s = {k: "hello"}
	// Final: printf '{"k":"hello"}' (leaf entry resolves .result.s.k = "hello")
	assert.Equal(t, `{"k":"hello"}`, out)
}

func TestIntegration_StepNonJSONResultIsString(t *testing.T) {
	// When a step's output is not JSON it's stored as a plain string.
	cfg := &Config{
		Name: "t",
		Commands: []Command{{
			Name: "run",
			Steps: []Step{{
				Name:    "raw",
				Command: &Cmd{Shell: true, Template: `printf 'not-json'`},
			}},
			Command: &Cmd{Shell: true, Template: `echo {{.result.raw}}`},
		}},
	}
	code, out := execCmd(t, cfg, "run")
	require.Equal(t, 0, code)
	assert.Equal(t, "not-json\n", out)
}

func TestIntegration_StepHexHashStaysString(t *testing.T) {
	// A step whose stdout looks like a hex hash (starts with digits, contains
	// a-f) must be stored as a string, not partially parsed as a JSON number.
	cfg := &Config{
		Name: "t",
		Commands: []Command{{
			Name: "run",
			Steps: []Step{{
				Name:    "hash",
				Command: &Cmd{Shell: true, Template: `printf '3bf86b7e484a4c355f49b3e4c9d8a17c'`},
			}},
			Command: &Cmd{Shell: true, Template: `echo {{.result.hash}}`},
		}},
	}
	code, out := execCmd(t, cfg, "run")
	require.Equal(t, 0, code)
	assert.Equal(t, "3bf86b7e484a4c355f49b3e4c9d8a17c\n", out)
}

func TestIntegration_StepFailurePropagatesExitCode(t *testing.T) {
	cfg := &Config{
		Name: "t",
		Commands: []Command{{
			Name: "run",
			Steps: []Step{{
				Name:    "fail",
				Command: &Cmd{Shell: true, Template: `exit 5`},
			}},
			Command: &Cmd{Shell: true, Template: `echo should-not-run`},
		}},
	}
	code, out := execCmd(t, cfg, "run")
	assert.Equal(t, 5, code)
	assert.Empty(t, out)
}

func TestIntegration_ExecutionCountReportedOnStderr(t *testing.T) {
	cfg := &Config{
		Name: "t",
		Commands: []Command{{
			Name: "run",
			Steps: []Step{{
				Name:    "s",
				Command: &Cmd{Shell: true, Template: `printf '{}'`},
			}},
			Command: &Cmd{Shell: true, Template: `true`},
		}},
	}
	_, _, errOut := execCmdFull(t, cfg, "run")
	assert.Equal(t, "2 executions\n", errOut)
}

func TestIntegration_ExecutionCountSuppressedWithQuiet(t *testing.T) {
	cfg := &Config{
		Name: "t",
		Commands: []Command{{
			Name: "run",
			Steps: []Step{{
				Name:    "s",
				Command: &Cmd{Shell: true, Template: `printf '{}'`},
			}},
			Command: &Cmd{Shell: true, Template: `true`},
		}},
	}
	_, _, errOut := execCmdFull(t, cfg, "--quiet", "run")
	assert.Empty(t, errOut)
}

func TestIntegration_NoCountWhenNoSteps(t *testing.T) {
	// Single execution (no steps) → nothing printed to stderr.
	cfg := &Config{
		Name:     "t",
		Command:  &Cmd{Shell: true, Template: `true`},
		Commands: []Command{{Name: "run"}},
	}
	_, _, errOut := execCmdFull(t, cfg, "run")
	assert.Empty(t, errOut)
}

func TestIntegration_ArrayResultIndexed(t *testing.T) {
	// JSON array result: index into it in the final template.
	cfg := &Config{
		Name: "t",
		Commands: []Command{{
			Name: "run",
			Steps: []Step{{
				Name:    "list",
				Command: &Cmd{Shell: true, Template: `printf '[{"name":"alice"},{"name":"bob"}]'`},
			}},
			Command: &Cmd{Shell: true, Template: `echo {{(index .result.list 1).name}}`},
		}},
	}
	code, out := execCmd(t, cfg, "run")
	require.Equal(t, 0, code)
	assert.Equal(t, "bob\n", out)
}

// --- Conditional step (when) tests ---

func TestIntegration_StepWhenTruthy_Runs(t *testing.T) {
	cfg := &Config{
		Name: "t",
		Commands: []Command{{
			Name: "run",
			Args: []Arg{{Name: "x", Required: true}},
			Steps: []Step{{
				Name:    "s",
				When:    "{{.arg.x}}",
				Command: &Cmd{Shell: true, Template: `printf '{"v":"ran"}'`},
			}},
			Command: &Cmd{Shell: true, Template: `echo {{.result.s.v}}`},
		}},
	}
	code, out := execCmd(t, cfg, "run", "yes")
	require.Equal(t, 0, code)
	assert.Equal(t, "ran\n", out)
}

func TestIntegration_StepWhenFalsy_Skipped(t *testing.T) {
	cfg := &Config{
		Name: "t",
		Commands: []Command{{
			Name: "run",
			Steps: []Step{{
				Name:    "s",
				When:    "false",
				Command: &Cmd{Shell: true, Template: `printf '{"v":"ran"}'`},
			}},
			Command: &Cmd{Shell: true, Template: `echo done`},
		}},
	}
	code, out := execCmd(t, cfg, "run")
	require.Equal(t, 0, code)
	assert.Equal(t, "done\n", out)
}

func TestIntegration_StepWhenFalsy_ResultAbsent(t *testing.T) {
	cfg := &Config{
		Name: "t",
		Commands: []Command{{
			Name: "run",
			Steps: []Step{{
				Name:    "skipped",
				When:    "0",
				Command: &Cmd{Shell: true, Template: `printf '{"v":"ran"}'`},
			}},
			Command: &Cmd{Shell: true, Template: `printf '{{if .result.skipped}}found{{else}}absent{{end}}'`},
		}},
	}
	code, out := execCmd(t, cfg, "run")
	require.Equal(t, 0, code)
	assert.Equal(t, "absent", out)
}

func TestIntegration_StepWhenEmpty_Runs(t *testing.T) {
	cfg := &Config{
		Name: "t",
		Commands: []Command{{
			Name: "run",
			Steps: []Step{{
				Name:    "s",
				Command: &Cmd{Shell: true, Template: `printf '{"v":"ran"}'`},
			}},
			Command: &Cmd{Shell: true, Template: `echo {{.result.s.v}}`},
		}},
	}
	code, out := execCmd(t, cfg, "run")
	require.Equal(t, 0, code)
	assert.Equal(t, "ran\n", out)
}

func TestIntegration_StepWhenSkipped_NoExecutionCount(t *testing.T) {
	cfg := &Config{
		Name: "t",
		Commands: []Command{{
			Name: "run",
			Steps: []Step{{
				Name:    "s",
				When:    "no",
				Command: &Cmd{Shell: true, Template: `printf '{}'`},
			}},
			Command: &Cmd{Shell: true, Template: `true`},
		}},
	}
	_, _, errOut := execCmdFull(t, cfg, "run")
	assert.Empty(t, errOut)
}

func TestIntegration_StepWhenUsesResultFromPriorStep(t *testing.T) {
	cfg := &Config{
		Name: "t",
		Commands: []Command{{
			Name: "run",
			Steps: []Step{
				{
					Name:    "check",
					Command: &Cmd{Shell: true, Template: `printf '{"need_resolve":true}'`},
				},
				{
					Name:    "resolve",
					When:    "{{.result.check.need_resolve}}",
					Command: &Cmd{Shell: true, Template: `printf '{"id":42}'`},
				},
			},
			Command: &Cmd{Shell: true, Template: `echo {{.result.resolve.id}}`},
		}},
	}
	code, out := execCmd(t, cfg, "run")
	require.Equal(t, 0, code)
	assert.Equal(t, "42\n", out)
}

func TestIntegration_StepWhenSkipsMidChain(t *testing.T) {
	cfg := &Config{
		Name: "t",
		Commands: []Command{{
			Name: "run",
			Steps: []Step{
				{
					Name:    "a",
					Command: &Cmd{Shell: true, Template: `printf '{"x":"first"}'`},
				},
				{
					Name:    "b",
					When:    "false",
					Command: &Cmd{Shell: true, Template: `printf '{"x":"should-not-run"}'`},
				},
				{
					Name:    "c",
					Command: &Cmd{Shell: true, Template: `printf '{"x":"third"}'`},
				},
			},
			Command: &Cmd{Shell: true, Template: `printf '%s-%s-%s' {{.result.a.x}} {{if .result.b}}{{.result.b.x}}{{else}}skipped{{end}} {{.result.c.x}}`},
		}},
	}
	code, out := execCmd(t, cfg, "run")
	require.Equal(t, 0, code)
	assert.Equal(t, "first-skipped-third", out)
}

func TestIntegration_StepWhenBadTemplate_Errors(t *testing.T) {
	cfg := &Config{
		Name: "t",
		Commands: []Command{{
			Name: "run",
			Steps: []Step{{
				Name:    "s",
				When:    "{{.nonexistent | badFunc}}",
				Command: &Cmd{Shell: true, Template: `true`},
			}},
			Command: &Cmd{Shell: true, Template: `true`},
		}},
	}
	code, _ := execCmd(t, cfg, "run")
	assert.NotEqual(t, 0, code)
}

func TestIntegration_Passthrough_Basic(t *testing.T) {
	cfg := &Config{
		Name: "t",
		Commands: []Command{{
			Name:        "wrap",
			Passthrough: true,
			Flags: []Flag{
				{Name: "o", Type: "string"},
				{Name: "verbose", Type: "bool"},
			},
			Command: &Cmd{Shell: true, Template: `printf 'out=%s verbose=%s rest=%s' {{.flag.o}} {{.flag.verbose}} '{{join " " .rest}}'`},
		}},
	}
	code, out := execCmd(t, cfg, "wrap", "--", "--verbose", "--unknown-flag", "-o", "/tmp/out.ptx", "positional.ii")
	assert.Equal(t, 0, code)
	assert.Equal(t, "out=/tmp/out.ptx verbose=true rest=--unknown-flag positional.ii", out)
}

func TestIntegration_Passthrough_SpreadRest(t *testing.T) {
	cfg := &Config{
		Name: "t",
		Commands: []Command{{
			Name:        "wrap",
			Passthrough: true,
			Flags: []Flag{
				{Name: "o", Type: "string"},
			},
			Command: &Cmd{Argv: []string{"echo", "{{spread .rest}}"}},
		}},
	}
	code, out := execCmd(t, cfg, "wrap", "--", "--some-flag", "val", "-o", "/tmp/out", "input.ii")
	assert.Equal(t, 0, code)
	assert.Equal(t, "--some-flag val input.ii\n", out)
}

func TestIntegration_Passthrough_SpreadRest_ShellForm(t *testing.T) {
	cfg := &Config{
		Name: "t",
		Commands: []Command{{
			Name:        "wrap",
			Passthrough: true,
			Flags: []Flag{
				{Name: "o", Type: "string"},
			},
			Command: &Cmd{Shell: true, Template: `printf '%s\n' {{spread .rest}}`},
		}},
	}
	code, out := execCmd(t, cfg, "wrap", "--", "--c++17", "-arch", "compute_80", "--generate-code=arch=compute_75,code=[compute_75]", "-o", "/tmp/output.ptx", "/tmp/input.cpp1.ii")
	assert.Equal(t, 0, code)
	assert.Equal(t, "--c++17\n-arch\ncompute_80\n--generate-code=arch=compute_75,code=[compute_75]\n/tmp/input.cpp1.ii\n", out)
}

func TestIntegration_Passthrough_SpreadRest_ShellStep(t *testing.T) {
	cfg := &Config{
		Name: "t",
		Commands: []Command{{
			Name:        "wrap",
			Passthrough: true,
			Flags: []Flag{
				{Name: "o", Type: "string"},
			},
			Steps: []Step{{
				Name:    "run",
				Command: &Cmd{Shell: true, Template: `printf '%s\n' {{spread .rest}}`},
			}},
			Command: &Cmd{Shell: true, Template: `echo done`},
		}},
	}
	code, _ := execCmd(t, cfg, "wrap", "--", "--flag=[val]", "-o", "/tmp/out", "file.txt")
	assert.Equal(t, 0, code)
}

func TestIntegration_Passthrough_EqualsForm(t *testing.T) {
	cfg := &Config{
		Name: "t",
		Commands: []Command{{
			Name:        "wrap",
			Passthrough: true,
			Flags: []Flag{
				{Name: "gen_c_file_name", Type: "string"},
			},
			Command: &Cmd{Shell: true, Template: `printf '%s' {{.flag.gen_c_file_name}}`},
		}},
	}
	code, out := execCmd(t, cfg, "wrap", "--", "--gen_c_file_name=/tmp/foo.c", "--other")
	assert.Equal(t, 0, code)
	assert.Equal(t, "/tmp/foo.c", out)
}

func TestIntegration_Passthrough_SingleDashLongFlag(t *testing.T) {
	cfg := &Config{
		Name: "t",
		Commands: []Command{{
			Name:        "wrap",
			Passthrough: true,
			Flags: []Flag{
				{Name: "arch", Type: "string"},
			},
			Command: &Cmd{Shell: true, Template: `printf '%s' {{.flag.arch}}`},
		}},
	}
	code, out := execCmd(t, cfg, "wrap", "--", "-arch", "compute_80", "-m64")
	assert.Equal(t, 0, code)
	assert.Equal(t, "compute_80", out)
}

func TestIntegration_Passthrough_Steps(t *testing.T) {
	cfg := &Config{
		Name: "t",
		Commands: []Command{{
			Name:        "wrap",
			Passthrough: true,
			Flags: []Flag{
				{Name: "o", Type: "string"},
			},
			Steps: []Step{{
				Name:    "found",
				Command: &Cmd{Shell: true, Template: `printf '%s' '{{.rest | filterSuffix ".ii" | first}}'`},
			}},
			Command: &Cmd{Shell: true, Template: `printf 'input=%s output=%s' {{.result.found}} {{.flag.o}}`},
		}},
	}
	code, out := execCmd(t, cfg, "wrap", "--", "--c++17", "-o", "/tmp/out.ptx", "/tmp/input.cpp1.ii")
	assert.Equal(t, 0, code)
	assert.Equal(t, "input=/tmp/input.cpp1.ii output=/tmp/out.ptx", out)
}

func TestIntegration_Passthrough_StringSlice(t *testing.T) {
	cfg := &Config{
		Name: "t",
		Commands: []Command{{
			Name:        "wrap",
			Passthrough: true,
			Flags: []Flag{
				{Name: "include", Type: "string-slice"},
			},
			Command: &Cmd{Shell: true, Template: `printf '%s' '{{join "," .flag.include}}'`},
		}},
	}
	code, out := execCmd(t, cfg, "wrap", "--", "--include", "a.h", "--include", "b.h", "other")
	assert.Equal(t, 0, code)
	assert.Equal(t, "a.h,b.h", out)
}

func TestIntegration_Passthrough_NoArgsAllowed(t *testing.T) {
	cfg := &Config{
		Name: "t",
		Commands: []Command{{
			Name:        "wrap",
			Passthrough: true,
			Args:        []Arg{{Name: "file", Required: true}},
			Command:     &Cmd{Shell: true, Template: `true`},
		}},
	}
	err := validate(cfg)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "passthrough commands cannot declare args")
}

func TestIntegration_Passthrough_OnlyOnLeaves(t *testing.T) {
	cfg := &Config{
		Name: "t",
		Commands: []Command{{
			Name:        "group",
			Passthrough: true,
			Commands: []Command{{
				Name:    "child",
				Command: &Cmd{Shell: true, Template: `true`},
			}},
		}},
	}
	err := validate(cfg)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "passthrough is only allowed on leaves")
}
