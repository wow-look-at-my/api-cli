package main

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// helpers --------------------------------------------------------------

// formatCfg returns a config with a single leaf "run" that emits the given
// JSON, plus a single named format "f". The format's behavior is configured
// by callers via `views` and the parent-level `when` arg.
func formatCfg(emitJSON, when string, views []View) *Config {
	return &Config{
		Name: "t",
		Formats: map[string]*Format{
			"f": {Input: "json", When: when, Views: views},
		},
		Command: &Cmd{Shell: true, Template: `printf '%s' '` + emitJSON + `'`},
		Commands: []Command{{
			Name:   "run",
			Format: &FormatRef{Name: "f"},
		}},
	}
}

// integration test cases ----------------------------------------------

func TestIntegration_FormatBypassedWhenNotTTY(t *testing.T) {
	// Default `when:""` -> "{{.tty}}". With a bytes.Buffer redirect, .tty is
	// false, so format is skipped and raw JSON is streamed.
	cfg := formatCfg(`[{"id":1}]`, "", []View{
		{Name: "v", Template: `FORMATTED`},
	})
	code, out := execCmd(t, cfg, "run")
	require.Equal(t, 0, code)
	assert.Equal(t, `[{"id":1}]`, out)
}

func TestIntegration_FormatAppliesWhenAlwaysTrue(t *testing.T) {
	// when:"true" forces format on regardless of TTY.
	cfg := formatCfg(`{"name":"ada"}`, "true", []View{
		{Name: "v", Template: `Name: {{.data.name}}`},
	})
	code, out := execCmd(t, cfg, "run")
	require.Equal(t, 0, code)
	assert.Equal(t, "Name: ada", out)
}

func TestIntegration_NoFormatFlagBypasses(t *testing.T) {
	cfg := formatCfg(`{"id":1}`, "true", []View{
		{Name: "v", Template: `FORMATTED`},
	})
	code, out := execCmd(t, cfg, "--no-format", "run")
	require.Equal(t, 0, code)
	assert.Equal(t, `{"id":1}`, out)
}

func TestIntegration_FormatRawFlagBypasses(t *testing.T) {
	cfg := formatCfg(`{"id":1}`, "true", []View{
		{Name: "v", Template: `FORMATTED`},
	})
	code, out := execCmd(t, cfg, "--format=raw", "run")
	require.Equal(t, 0, code)
	assert.Equal(t, `{"id":1}`, out)
}

func TestIntegration_NoFormatEnvBypasses(t *testing.T) {
	t.Setenv("NO_FORMAT", "1")
	cfg := formatCfg(`{"id":1}`, "true", []View{
		{Name: "v", Template: `FORMATTED`},
	})
	code, out := execCmd(t, cfg, "run")
	require.Equal(t, 0, code)
	assert.Equal(t, `{"id":1}`, out)
}

func TestIntegration_FormatAlwaysFlagForcesTTYInPredicate(t *testing.T) {
	// Author's when is `{{.tty}}`. In a non-TTY test context, that's false —
	// but --format=always lies about .tty, making the predicate true.
	cfg := formatCfg(`{"name":"ada"}`, "{{.tty}}", []View{
		{Name: "v", Template: `Name: {{.data.name}}`},
	})
	code, out := execCmd(t, cfg, "--format=always", "run")
	require.Equal(t, 0, code)
	assert.Equal(t, "Name: ada", out)
}

func TestIntegration_AND_AuthorVetoBeatsUserAlways(t *testing.T) {
	// when:"false" => author always says no; --format=always cannot override.
	cfg := formatCfg(`{"id":1}`, "false", []View{
		{Name: "v", Template: `FORMATTED`},
	})
	code, out := execCmd(t, cfg, "--format=always", "run")
	require.Equal(t, 0, code)
	assert.Equal(t, `{"id":1}`, out)
}

func TestIntegration_AND_UserVetoBeatsAuthorTrue(t *testing.T) {
	cfg := formatCfg(`{"id":1}`, "true", []View{
		{Name: "v", Template: `FORMATTED`},
	})
	code, out := execCmd(t, cfg, "--no-format", "run")
	require.Equal(t, 0, code)
	assert.Equal(t, `{"id":1}`, out)
}

func TestIntegration_ViewFlagSelectsExplicitly(t *testing.T) {
	cfg := formatCfg(`{"id":1}`, "true", []View{
		{Name: "table", Default: true, Template: `T`},
		{Name: "detail", Template: `D`},
	})
	code, out := execCmd(t, cfg, "--view=detail", "run")
	require.Equal(t, 0, code)
	assert.Equal(t, "D", out)
}

func TestIntegration_ViewFlagUnknown(t *testing.T) {
	cfg := formatCfg(`{"id":1}`, "true", []View{
		{Name: "v", Template: `V`},
	})
	code, _ := execCmd(t, cfg, "--view=missing", "run")
	assert.NotEqual(t, 0, code)
}

func TestIntegration_LinesInputSplitsStdout(t *testing.T) {
	cfg := &Config{
		Name: "t",
		Formats: map[string]*Format{
			"f": {Input: "lines", When: "true", Views: []View{
				{Name: "v", Template: `{{ range .data }}- {{.}}{{"\n"}}{{ end }}`},
			}},
		},
		Command: &Cmd{Shell: true, Template: `printf 'a\nb\nc\n'`},
		Commands: []Command{{
			Name:   "run",
			Format: &FormatRef{Name: "f"},
		}},
	}
	code, out := execCmd(t, cfg, "run")
	require.Equal(t, 0, code)
	assert.Equal(t, "- a\n- b\n- c\n", out)
}

func TestIntegration_RawInputPassesThroughTrimmed(t *testing.T) {
	cfg := &Config{
		Name: "t",
		Formats: map[string]*Format{
			"f": {Input: "raw", When: "true", Views: []View{
				{Name: "v", Template: `[{{.data}}]`},
			}},
		},
		Command: &Cmd{Shell: true, Template: `printf 'hello\n\n'`},
		Commands: []Command{{
			Name:   "run",
			Format: &FormatRef{Name: "f"},
		}},
	}
	code, out := execCmd(t, cfg, "run")
	require.Equal(t, 0, code)
	assert.Equal(t, "[hello]", out)
}

func TestIntegration_NamedFormatReferencedFromLeaf(t *testing.T) {
	cfg := &Config{
		Name: "t",
		Formats: map[string]*Format{
			"u": {When: "true", Views: []View{
				{Name: "v", Template: `id={{.data.id}}`},
			}},
		},
		Command: &Cmd{Shell: true, Template: `printf '%s' '{"id":7}'`},
		Commands: []Command{{
			Name:   "run",
			Format: &FormatRef{Name: "u"},
		}},
	}
	code, out := execCmd(t, cfg, "run")
	require.Equal(t, 0, code)
	assert.Equal(t, "id=7", out)
}

func TestIntegration_FormatInheritedDownTree(t *testing.T) {
	cfg := &Config{
		Name: "t",
		Formats: map[string]*Format{
			"f": {When: "true", Views: []View{
				{Name: "v", Template: `id={{.data.id}}`},
			}},
		},
		Command: &Cmd{Shell: true, Template: `printf '%s' '{"id":3}'`},
		Commands: []Command{{
			Name:   "users",
			Format: &FormatRef{Name: "f"},
			Commands: []Command{{
				Name: "get",
			}},
		}},
	}
	code, out := execCmd(t, cfg, "users", "get")
	require.Equal(t, 0, code)
	assert.Equal(t, "id=3", out)
}

func TestIntegration_LeafOverridesAncestorFormat(t *testing.T) {
	cfg := &Config{
		Name: "t",
		Formats: map[string]*Format{
			"parent": {When: "true", Views: []View{
				{Name: "v", Template: `PARENT`},
			}},
			"child": {When: "true", Views: []View{
				{Name: "v", Template: `CHILD`},
			}},
		},
		Command: &Cmd{Shell: true, Template: `printf '{}'`},
		Commands: []Command{{
			Name:   "users",
			Format: &FormatRef{Name: "parent"},
			Commands: []Command{{
				Name:   "get",
				Format: &FormatRef{Name: "child"},
			}},
		}},
	}
	code, out := execCmd(t, cfg, "users", "get")
	require.Equal(t, 0, code)
	assert.Equal(t, "CHILD", out)
}

func TestIntegration_FormatExitCodePropagatedOnChildFailure(t *testing.T) {
	cfg := &Config{
		Name: "t",
		Formats: map[string]*Format{
			"f": {When: "true", Views: []View{
				{Name: "v", Template: `FORMATTED`},
			}},
		},
		// Print to stdout, then exit 7.
		Command: &Cmd{Shell: true, Template: `printf 'oops'; exit 7`},
		Commands: []Command{{
			Name:   "run",
			Format: &FormatRef{Name: "f"},
		}},
	}
	code, out, errOut := execCmdFull(t, cfg, "run")
	assert.Equal(t, 7, code)
	// On failure, captured stdout goes to stderr; nothing on stdout.
	assert.Equal(t, "", out)
	assert.Equal(t, "oops", errOut)
}

func TestIntegration_BackcompatNoFormatBlockBehavesAsBefore(t *testing.T) {
	// A config with no `formats` and no `format` should behave exactly as before.
	cfg := &Config{
		Name:     "t",
		Command:  &Cmd{Shell: true, Template: `printf '%s' '{"id":1,"name":"ada"}'`},
		Commands: []Command{{Name: "run"}},
	}
	code, out := execCmd(t, cfg, "run")
	require.Equal(t, 0, code)
	assert.Equal(t, `{"id":1,"name":"ada"}`, out)
}

func TestIntegration_OverflowSkipsFormatting(t *testing.T) {
	// Stress the cap by making it tiny and verifying that an overflow output
	// streams unmodified instead of being formatted.
	prev := execStdout
	t.Cleanup(func() { execStdout = prev })

	// Use a real temp file to capture both buffered and streamed bytes.
	tmp, err := os.CreateTemp(t.TempDir(), "out")
	require.NoError(t, err)
	t.Cleanup(func() { tmp.Close() })
	execStdout = tmp

	// Simulate by overriding defaultFormatCap via a one-off call. We can do
	// this by making the child output more than 32 bytes of JSON-shaped data;
	// but defaultFormatCap is 32 MiB so we'd need a big payload. Instead, use
	// captureExecCapped directly to verify the overflow path here, since
	// that's what runFormatted relies on.
	bigBody := strings.Repeat("X", 100)
	out, overflowed, code := captureExecCapped(
		&Cmd{Shell: true, Template: `printf '%s' '` + bigBody + `'`},
		"", "", map[string]any{}, 10,
	)
	assert.True(t, overflowed)
	assert.Equal(t, 0, code)
	assert.Equal(t, "", out)

	// The streamed bytes are now in the temp file.
	require.NoError(t, tmp.Sync())
	_, err = tmp.Seek(0, 0)
	require.NoError(t, err)
	got := make([]byte, len(bigBody))
	n, err := tmp.Read(got)
	require.NoError(t, err)
	assert.Equal(t, len(bigBody), n)
	assert.Equal(t, bigBody, string(got))
}
