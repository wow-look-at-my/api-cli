package main

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Tests for the post-1.0 feature additions: variadic args, preconditions,
// templated flag defaults, bool-default-true negation, and flag conflicts.
// These live in their own file to keep integration_test.go focused on the
// original integration scenarios.

// --- Variadic args ---

func TestIntegration_VariadicStringArgs(t *testing.T) {
	cfg := &Config{
		Name:	"t",
		Commands: []Command{{
			Name:	"echo",
			Args: []Arg{
				{Name: "first", Required: true},
				{Name: "rest", Variadic: true},
			},
			Command:	&Cmd{Argv: []string{"echo", "{{.arg.first}}", "{{spread .arg.rest}}"}},
		}},
	}
	code, out := execCmd(t, cfg, "echo", "a", "b", "c", "d")
	require.Equal(t, 0, code)
	assert.Equal(t, "a b c d\n", out)
}

func TestIntegration_VariadicEmptyOptional(t *testing.T) {
	cfg := &Config{
		Name:	"t",
		Commands: []Command{{
			Name:	"echo",
			Args: []Arg{
				{Name: "rest", Variadic: true},
			},
			Command:	&Cmd{Argv: []string{"echo", "{{spread .arg.rest}}", "done"}},
		}},
	}
	code, out := execCmd(t, cfg, "echo")
	require.Equal(t, 0, code)
	assert.Equal(t, "done\n", out)
}

func TestIntegration_VariadicRequiredRejectsZero(t *testing.T) {
	cfg := &Config{
		Name:	"t",
		Commands: []Command{{
			Name:	"echo",
			Args: []Arg{
				{Name: "rest", Required: true, Variadic: true},
			},
			Command:	&Cmd{Argv: []string{"echo", "{{spread .arg.rest}}"}},
		}},
	}
	code, _ := execCmd(t, cfg, "echo")
	assert.Equal(t, 1, code)
}

func TestIntegration_VariadicIntArgs(t *testing.T) {
	cfg := &Config{
		Name:	"t",
		Commands: []Command{{
			Name:	"sum",
			Args: []Arg{
				{Name: "nums", Type: "int", Variadic: true},
			},
			Command:	&Cmd{Shell: true, Template: `printf '%d' {{add (index .arg.nums 0) (index .arg.nums 1)}}`},
		}},
	}
	code, out := execCmd(t, cfg, "sum", "3", "4")
	require.Equal(t, 0, code)
	assert.Equal(t, "7", out)
}

func TestIntegration_VariadicIntRejectsNonNumeric(t *testing.T) {
	cfg := &Config{
		Name:	"t",
		Commands: []Command{{
			Name:	"sum",
			Args: []Arg{
				{Name: "nums", Type: "int", Variadic: true},
			},
			Command:	&Cmd{Shell: true, Template: `true`},
		}},
	}
	code, _ := execCmd(t, cfg, "sum", "3", "oops")
	assert.NotEqual(t, 0, code)
}

// --- Preconditions ---

func TestIntegration_PreconditionPasses(t *testing.T) {
	cfg := &Config{
		Name:		"t",
		Command:	&Cmd{Shell: true, Template: `echo ran`},
		Commands: []Command{{
			Name:		"go",
			Args:		[]Arg{{Name: "n", Type: "int", Required: true}},
			Preconditions:	[]string{`{{if le .arg.n 0}}n must be positive{{end}}`},
		}},
	}
	code, out := execCmd(t, cfg, "go", "5")
	require.Equal(t, 0, code)
	assert.Equal(t, "ran\n", out)
}

func TestIntegration_PreconditionFails(t *testing.T) {
	cfg := &Config{
		Name:		"t",
		Command:	&Cmd{Shell: true, Template: `echo should-not-run`},
		Commands: []Command{{
			Name:		"go",
			Args:		[]Arg{{Name: "n", Type: "int", Required: true}},
			Preconditions:	[]string{`{{if le .arg.n 0}}n must be positive (got {{.arg.n}}){{end}}`},
		}},
	}
	code, out, errOut := execCmdFull(t, cfg, "go", "0")
	assert.Equal(t, 1, code)
	assert.Empty(t, out)
	assert.Contains(t, errOut, "n must be positive (got 0)")
}

func TestIntegration_PreconditionFileExists(t *testing.T) {
	dir := t.TempDir()
	target := dir + "/already-here"
	require.NoError(t, os.WriteFile(target, []byte("x"), 0o600))
	cfg := &Config{
		Name:		"t",
		Command:	&Cmd{Shell: true, Template: `echo created`},
		Commands: []Command{{
			Name:	"create",
			Args:	[]Arg{{Name: "out", Required: true}},
			Preconditions: []string{
				`{{if fileExists .arg.out}}{{.arg.out}} already exists{{end}}`,
			},
		}},
	}
	code, _, errOut := execCmdFull(t, cfg, "create", target)
	assert.Equal(t, 1, code)
	assert.Contains(t, errOut, "already exists")
}

// --- Templated flag defaults ---

func TestIntegration_TemplatedStringDefault(t *testing.T) {
	cfg := &Config{
		Name:	"t",
		Commands: []Command{{
			Name:	"show",
			Args:	[]Arg{{Name: "archive", Required: true}},
			Flags: []Flag{
				{Name: "to", Type: "string", Default: `{{trimSuffix ".tar.gz" .arg.archive}}`},
			},
			Command:	&Cmd{Argv: []string{"echo", "{{.flag.to}}"}},
		}},
	}
	code, out := execCmd(t, cfg, "show", "foo.tar.gz")
	require.Equal(t, 0, code)
	assert.Equal(t, "foo\n", out)
}

func TestIntegration_TemplatedDefaultOverriddenByUser(t *testing.T) {
	cfg := &Config{
		Name:	"t",
		Commands: []Command{{
			Name:	"show",
			Args:	[]Arg{{Name: "archive", Required: true}},
			Flags: []Flag{
				{Name: "to", Type: "string", Default: `{{.arg.archive}}-default`},
			},
			Command:	&Cmd{Argv: []string{"echo", "{{.flag.to}}"}},
		}},
	}
	code, out := execCmd(t, cfg, "show", "x", "--to", "explicit")
	require.Equal(t, 0, code)
	assert.Equal(t, "explicit\n", out)
}

// --- Bool negation ---

func TestIntegration_BoolDefaultTrueNegated(t *testing.T) {
	cfg := &Config{
		Name:	"t",
		Commands: []Command{{
			Name:	"show",
			Flags: []Flag{
				{Name: "verbose", Type: "bool", Default: true},
			},
			Command:	&Cmd{Shell: true, Template: `echo {{if .flag.verbose}}LOUD{{else}}quiet{{end}}`},
		}},
	}
	code, out := execCmd(t, cfg, "show")
	require.Equal(t, 0, code)
	assert.Equal(t, "LOUD\n", out)

	code, out = execCmd(t, cfg, "show", "--no-verbose")
	require.Equal(t, 0, code)
	assert.Equal(t, "quiet\n", out)
}

func TestIntegration_BoolDefaultFalseHasNoNegation(t *testing.T) {
	cfg := &Config{
		Name:		"t",
		Command:	&Cmd{Shell: true, Template: `true`},
		Commands: []Command{{
			Name:	"x",
			Flags: []Flag{
				{Name: "verbose", Type: "bool", Default: false},
			},
		}},
	}
	require.NoError(t, validate(cfg))
	root := newRoot(cfg)
	cmd, _, err := root.Find([]string{"x"})
	require.NoError(t, err)
	assert.Nil(t, cmd.Flags().Lookup("no-verbose"))
}

// --- Conflicts ---

func TestIntegration_ConflictsDetected(t *testing.T) {
	cfg := &Config{
		Name:		"t",
		Command:	&Cmd{Shell: true, Template: `true`},
		Commands: []Command{{
			Name:	"x",
			Flags: []Flag{
				{Name: "strip", Type: "bool", Conflicts: []string{"keep"}},
				{Name: "keep", Type: "bool"},
			},
		}},
	}
	code, _, _ := execCmdFull(t, cfg, "x", "--strip", "--keep")
	assert.NotEqual(t, 0, code)
}

func TestIntegration_ConflictsAllowSingle(t *testing.T) {
	cfg := &Config{
		Name:		"t",
		Command:	&Cmd{Shell: true, Template: `echo ok`},
		Commands: []Command{{
			Name:	"x",
			Flags: []Flag{
				{Name: "strip", Type: "bool", Conflicts: []string{"keep"}},
				{Name: "keep", Type: "bool"},
			},
		}},
	}
	code, out := execCmd(t, cfg, "x", "--strip")
	require.Equal(t, 0, code)
	assert.Equal(t, "ok\n", out)
}

// --- Confirm ---

func TestIntegration_ConfirmYesFlag(t *testing.T) {
	cfg := &Config{
		Name:	"t",
		Commands: []Command{{
			Name:		"rm",
			Args:		[]Arg{{Name: "name", Required: true}},
			Confirm:	"Delete {{.arg.name}}?",
			Command:	&Cmd{Argv: []string{"echo", "deleted"}},
		}},
	}
	code, out := execCmd(t, cfg, "rm", "foo", "--yes")
	require.Equal(t, 0, code)
	assert.Equal(t, "deleted\n", out)
}

func TestIntegration_ConfirmNonTTYWithoutYesFails(t *testing.T) {
	cfg := &Config{
		Name:	"t",
		Commands: []Command{{
			Name:		"rm",
			Args:		[]Arg{{Name: "name", Required: true}},
			Confirm:	"Delete {{.arg.name}}?",
			Command:	&Cmd{Argv: []string{"echo", "deleted"}},
		}},
	}
	code, out, errOut := execCmdFull(t, cfg, "rm", "foo")
	assert.Equal(t, 1, code)
	assert.Empty(t, out)
	assert.Contains(t, errOut, "refusing to run without confirmation; pass --yes")
}

func setInteractive(t *testing.T) {
	t.Helper()
	prev := isInteractive
	isInteractive = func() bool { return true }
	t.Cleanup(func() { isInteractive = prev })
}

func TestIntegration_ConfirmAcceptY(t *testing.T) {
	cfg := &Config{
		Name:	"t",
		Commands: []Command{{
			Name:		"rm",
			Confirm:	"Sure?",
			Command:	&Cmd{Argv: []string{"echo", "done"}},
		}},
	}
	setInteractive(t)
	prev := execStdin
	execStdin = strings.NewReader("y\n")
	t.Cleanup(func() { execStdin = prev })

	code, out := execCmd(t, cfg, "rm")
	require.Equal(t, 0, code)
	assert.Equal(t, "done\n", out)
}

func TestIntegration_ConfirmAcceptYes(t *testing.T) {
	cfg := &Config{
		Name:	"t",
		Commands: []Command{{
			Name:		"rm",
			Confirm:	"Sure?",
			Command:	&Cmd{Argv: []string{"echo", "done"}},
		}},
	}
	setInteractive(t)
	prev := execStdin
	execStdin = strings.NewReader("yes\n")
	t.Cleanup(func() { execStdin = prev })

	code, out := execCmd(t, cfg, "rm")
	require.Equal(t, 0, code)
	assert.Equal(t, "done\n", out)
}

func TestIntegration_ConfirmRejectN(t *testing.T) {
	cfg := &Config{
		Name:	"t",
		Commands: []Command{{
			Name:		"rm",
			Confirm:	"Sure?",
			Command:	&Cmd{Argv: []string{"echo", "should-not-run"}},
		}},
	}
	setInteractive(t)
	prev := execStdin
	execStdin = strings.NewReader("n\n")
	t.Cleanup(func() { execStdin = prev })

	code, out := execCmd(t, cfg, "rm")
	assert.Equal(t, 1, code)
	assert.Empty(t, out)
}

func TestIntegration_ConfirmRejectEOF(t *testing.T) {
	cfg := &Config{
		Name:	"t",
		Commands: []Command{{
			Name:		"rm",
			Confirm:	"Sure?",
			Command:	&Cmd{Argv: []string{"echo", "should-not-run"}},
		}},
	}
	setInteractive(t)
	prev := execStdin
	execStdin = strings.NewReader("")
	t.Cleanup(func() { execStdin = prev })

	code, out := execCmd(t, cfg, "rm")
	assert.Equal(t, 1, code)
	assert.Empty(t, out)
}

func TestIntegration_ConfirmTemplateRendersArgs(t *testing.T) {
	cfg := &Config{
		Name:	"t",
		Commands: []Command{{
			Name:		"rm",
			Args:		[]Arg{{Name: "target", Required: true}},
			Confirm:	"Destroy {{.arg.target}} forever?",
			Command:	&Cmd{Argv: []string{"echo", "destroyed"}},
		}},
	}
	code, out, errOut := execCmdFull(t, cfg, "rm", "my-secret", "--yes")
	require.Equal(t, 0, code)
	assert.Equal(t, "destroyed\n", out)
	assert.Empty(t, errOut)
}

func TestIntegration_ConfirmEmptyRenderedSkips(t *testing.T) {
	cfg := &Config{
		Name:	"t",
		Commands: []Command{{
			Name:		"rm",
			Confirm:	"{{if eq .arg.mode \"dangerous\"}}Are you sure?{{end}}",
			Args:		[]Arg{{Name: "mode", Required: true}},
			Command:	&Cmd{Argv: []string{"echo", "ran"}},
		}},
	}
	code, out := execCmd(t, cfg, "rm", "safe")
	require.Equal(t, 0, code)
	assert.Equal(t, "ran\n", out)
}

func TestIntegration_ConfirmInherited(t *testing.T) {
	cfg := &Config{
		Name:	"t",
		Commands: []Command{{
			Name:		"secrets",
			Confirm:	"This is destructive. Continue?",
			Command:	&Cmd{Argv: []string{"echo", "ok"}},
			Commands: []Command{
				{Name: "rm", Args: []Arg{{Name: "name", Required: true}}},
				{Name: "list"},
			},
		}},
	}
	// Leaf inherits confirm from group — non-tty without --yes fails.
	code, out, errOut := execCmdFull(t, cfg, "secrets", "rm", "foo")
	assert.Equal(t, 1, code)
	assert.Empty(t, out)
	assert.Contains(t, errOut, "refusing to run without confirmation; pass --yes")

	// --yes bypasses the inherited confirm.
	code, out = execCmd(t, cfg, "secrets", "list", "--yes")
	require.Equal(t, 0, code)
	assert.Equal(t, "ok\n", out)
}

func TestIntegration_ConfirmLeafOverridesGroup(t *testing.T) {
	cfg := &Config{
		Name:	"t",
		Commands: []Command{{
			Name:		"secrets",
			Confirm:	"group confirm",
			Command:	&Cmd{Argv: []string{"echo", "ok"}},
			Commands: []Command{
				{Name: "rm", Confirm: "leaf confirm"},
			},
		}},
	}
	setInteractive(t)
	prev := execStdin
	execStdin = strings.NewReader("y\n")
	t.Cleanup(func() { execStdin = prev })

	code, _, errOut := execCmdFull(t, cfg, "secrets", "rm")
	require.Equal(t, 0, code)
	assert.Contains(t, errOut, "leaf confirm")
	assert.NotContains(t, errOut, "group confirm")
}
