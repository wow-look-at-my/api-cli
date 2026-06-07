package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Tests for the per-command/step cwd field. Cwd inherits down the tree like
// `command`: a node's own cwd, if non-empty, overrides any ancestor's. Steps
// inherit the leaf's effective cwd unless they override it. The cwd template
// is rendered against the same data context as the command it applies to.

func TestCwd_DoExecHonoursDir(t *testing.T) {
	dir := t.TempDir()
	out, _ := captureExecStreams(t)
	c := &Cmd{Shell: true, Template: `pwd`}
	code := doExec(c, dir, "", map[string]any{})
	require.Equal(t, 0, code)
	// macOS' /tmp is a symlink to /private/tmp; resolve both sides for comparison.
	gotResolved, err := filepath.EvalSymlinks(out.String()[:len(out.String())-1])
	require.NoError(t, err)
	wantResolved, err := filepath.EvalSymlinks(dir)
	require.NoError(t, err)
	assert.Equal(t, wantResolved, gotResolved)
}

func TestCwd_CaptureExecHonoursDir(t *testing.T) {
	dir := t.TempDir()
	captureExecStreams(t)
	c := &Cmd{Shell: true, Template: `pwd`}
	out, code := captureExec(c, dir, "", map[string]any{})
	require.Equal(t, 0, code)
	gotResolved, err := filepath.EvalSymlinks(out[:len(out)-1])
	require.NoError(t, err)
	wantResolved, err := filepath.EvalSymlinks(dir)
	require.NoError(t, err)
	assert.Equal(t, wantResolved, gotResolved)
}

func TestCwd_EmptyMeansProcessCwd(t *testing.T) {
	// An empty cwd argument should leave cmd.Dir untouched, i.e. the child
	// runs in the calling process's working directory.
	captureExecStreams(t)
	c := &Cmd{Shell: true, Template: `pwd`}
	out, code := captureExec(c, "", "", map[string]any{})
	require.Equal(t, 0, code)
	wd, err := os.Getwd()
	require.NoError(t, err)
	gotResolved, err := filepath.EvalSymlinks(out[:len(out)-1])
	require.NoError(t, err)
	wantResolved, err := filepath.EvalSymlinks(wd)
	require.NoError(t, err)
	assert.Equal(t, wantResolved, gotResolved)
}

// --- End-to-end (via runLeaf) ---

func TestIntegration_LeafCwdLiteral(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "marker"), []byte("here\n"), 0o600))
	cfg := &Config{
		Name:	"t",
		Commands: []Command{{
			Name:		"show",
			Cwd:		dir,
			Command:	&Cmd{Shell: true, Template: `cat marker`},
		}},
	}
	code, out := execCmd(t, cfg, "show")
	require.Equal(t, 0, code)
	assert.Equal(t, "here\n", out)
}

func TestIntegration_LeafCwdTemplated(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "tag"), []byte("templated\n"), 0o600))
	t.Setenv("API_CLI_CWD_TEST_DIR", dir)
	cfg := &Config{
		Name:	"t",
		Commands: []Command{{
			Name:		"show",
			Cwd:		`{{.env.API_CLI_CWD_TEST_DIR}}`,
			Command:	&Cmd{Shell: true, Template: `cat tag`},
		}},
	}
	code, out := execCmd(t, cfg, "show")
	require.Equal(t, 0, code)
	assert.Equal(t, "templated\n", out)
}

func TestIntegration_CwdInheritsFromConfig(t *testing.T) {
	// Top-level cwd flows down to every leaf that doesn't override it.
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "f"), []byte("inherited\n"), 0o600))
	cfg := &Config{
		Name:		"t",
		Cwd:		dir,
		Command:	&Cmd{Shell: true, Template: `cat f`},
		Commands:	[]Command{{Name: "show"}},
	}
	code, out := execCmd(t, cfg, "show")
	require.Equal(t, 0, code)
	assert.Equal(t, "inherited\n", out)
}

func TestIntegration_CwdInheritsThroughGroup(t *testing.T) {
	// A group node sets cwd; its leaves inherit it.
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "f"), []byte("group\n"), 0o600))
	cfg := &Config{
		Name:		"t",
		Command:	&Cmd{Shell: true, Template: `cat f`},
		Commands: []Command{{
			Name:	"outer",
			Cwd:	dir,
			Commands: []Command{
				{Name: "leaf"},
			},
		}},
	}
	code, out := execCmd(t, cfg, "outer", "leaf")
	require.Equal(t, 0, code)
	assert.Equal(t, "group\n", out)
}

func TestIntegration_LeafCwdOverridesAncestor(t *testing.T) {
	parent := t.TempDir()
	leafDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(parent, "f"), []byte("parent\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(leafDir, "f"), []byte("leaf\n"), 0o600))
	cfg := &Config{
		Name:		"t",
		Cwd:		parent,
		Command:	&Cmd{Shell: true, Template: `cat f`},
		Commands: []Command{
			{Name: "from-parent"},
			{Name: "from-leaf", Cwd: leafDir},
		},
	}
	code, out := execCmd(t, cfg, "from-parent")
	require.Equal(t, 0, code)
	assert.Equal(t, "parent\n", out)

	code, out = execCmd(t, cfg, "from-leaf")
	require.Equal(t, 0, code)
	assert.Equal(t, "leaf\n", out)
}

func TestIntegration_StepInheritsLeafCwd(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "src"), []byte(`{"v":"hi"}`), 0o600))
	cfg := &Config{
		Name:	"t",
		Commands: []Command{{
			Name:	"run",
			Cwd:	dir,
			Steps: []Step{{
				Name:		"load",
				Command:	&Cmd{Shell: true, Template: `cat src`},
			}},
			Command:	&Cmd{Shell: true, Template: `printf '%s' {{.result.load.v}}`},
		}},
	}
	code, out := execCmd(t, cfg, "run")
	require.Equal(t, 0, code)
	assert.Equal(t, "hi", out)
}

func TestIntegration_StepCwdOverridesLeaf(t *testing.T) {
	leafDir := t.TempDir()
	stepDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(leafDir, "marker"), []byte("leaf\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(stepDir, "marker"), []byte(`"step"`), 0o600))
	cfg := &Config{
		Name:	"t",
		Commands: []Command{{
			Name:	"run",
			Cwd:	leafDir,
			Steps: []Step{{
				Name:		"where",
				Cwd:		stepDir,
				Command:	&Cmd{Shell: true, Template: `cat marker`},
			}},
			// The leaf's own command runs in leafDir; its output proves the
			// step's override was scoped to the step.
			Command:	&Cmd{Shell: true, Template: `printf '%s|%s' {{.result.where}} $(cat marker)`},
		}},
	}
	code, out := execCmd(t, cfg, "run")
	require.Equal(t, 0, code)
	assert.Equal(t, "step|leaf", out)
}

func TestIntegration_CwdRenderedWithArgs(t *testing.T) {
	parent := t.TempDir()
	sub := filepath.Join(parent, "child")
	require.NoError(t, os.MkdirAll(sub, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(sub, "f"), []byte("ok\n"), 0o600))
	cfg := &Config{
		Name:	"t",
		Vars:	map[string]any{"root": parent},
		Commands: []Command{{
			Name:		"show",
			Args:		[]Arg{{Name: "name", Required: true}},
			Cwd:		`{{.var.root}}/{{.arg.name}}`,
			Command:	&Cmd{Shell: true, Template: `cat f`},
		}},
	}
	code, out := execCmd(t, cfg, "show", "child")
	require.Equal(t, 0, code)
	assert.Equal(t, "ok\n", out)
}

func TestIntegration_CwdRenderErrorFails(t *testing.T) {
	cfg := &Config{
		Name:	"t",
		Commands: []Command{{
			Name:		"x",
			Cwd:		`{{.broken`,
			Command:	&Cmd{Shell: true, Template: `true`},
		}},
	}
	// A broken cwd template surfaces as a RunE error from cobra; our
	// execCmdFull harness collapses any cobra-level error into exit 1.
	// (The "render cwd: ..." text goes through cobra's err writer, which the
	// test harness routes to io.Discard.)
	code, _, _ := execCmdFull(t, cfg, "x")
	assert.Equal(t, 1, code)
}

func TestIntegration_CwdMissingDirFails(t *testing.T) {
	cfg := &Config{
		Name:	"t",
		Commands: []Command{{
			Name:		"x",
			Cwd:		"/definitely/not/a/real/dir/xyz123",
			Command:	&Cmd{Shell: true, Template: `true`},
		}},
	}
	code, _, _ := execCmdFull(t, cfg, "x")
	// Go's exec.Cmd.Run returns an error when Dir doesn't exist; we surface
	// it as 127 (failed to start).
	assert.Equal(t, 127, code)
}

func TestIntegration_EntryFieldStillForbiddenOnGroup(t *testing.T) {
	// Sanity: cwd on a group is fine; entry is still leaf-only. Just confirming
	// we didn't accidentally regress group validation.
	cfg := &Config{
		Name:		"t",
		Command:	&Cmd{Shell: true, Template: `true`},
		Commands: []Command{{
			Name:		"outer",
			Cwd:		"/tmp",
			Entry:		json.RawMessage(`{"x":"y"}`),
			Commands:	[]Command{{Name: "leaf"}},
		}},
	}
	err := validate(cfg)
	assert.Error(t, err)
}
