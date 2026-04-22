package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/wow-look-at-my/testify/assert"
	"github.com/wow-look-at-my/testify/require"
)

// chdir switches the working directory for the duration of the test and
// restores it on cleanup. Used for config-discovery tests that need to
// control ./api.json's existence.
func chdir(t *testing.T, dir string) {
	t.Helper()
	prev, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(dir))
	t.Cleanup(func() { _ = os.Chdir(prev) })
}

func TestRun_MissingConfigWithSubcommandReturns2(t *testing.T) {
	chdir(t, t.TempDir())
	var errOut bytes.Buffer
	code := run([]string{"whatever"}, &errOut)
	assert.Equal(t, 2, code)
	assert.Contains(t, errOut.String(), "no config found")
}

func TestRun_MissingConfigBareShowsHelp(t *testing.T) {
	chdir(t, t.TempDir())
	// Replay stdout so cobra's help goes somewhere we can inspect. cobra
	// writes help to the command's OutOrStderr; without a config we get the
	// default writer which is os.Stdout, so capture that.
	prevStdout := os.Stdout
	r, w, err := os.Pipe()
	require.NoError(t, err)
	os.Stdout = w
	t.Cleanup(func() { os.Stdout = prevStdout })

	var errOut bytes.Buffer
	code := run(nil, &errOut)

	require.NoError(t, w.Close())
	out, _ := io.ReadAll(r)

	assert.Equal(t, 0, code)
	assert.Contains(t, string(out), "--config")
	assert.NotContains(t, errOut.String(), "no config found")
}

func TestRun_MissingConfigHelpFlagShowsHelp(t *testing.T) {
	chdir(t, t.TempDir())
	prevStdout := os.Stdout
	r, w, err := os.Pipe()
	require.NoError(t, err)
	os.Stdout = w
	t.Cleanup(func() { os.Stdout = prevStdout })

	var errOut bytes.Buffer
	code := run([]string{"--help"}, &errOut)

	require.NoError(t, w.Close())
	out, _ := io.ReadAll(r)

	assert.Equal(t, 0, code)
	assert.Contains(t, string(out), "--config")
}

func TestRun_InvalidConfigReturns2(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "bad.json"), []byte("{not json"), 0o600))
	var errOut bytes.Buffer
	code := run([]string{"--config", filepath.Join(dir, "bad.json")}, &errOut)
	assert.Equal(t, 2, code)
}

func TestRun_HappyPath(t *testing.T) {
	dir := t.TempDir()
	cfg := `{
      "name": "t",
      "command": "printf '%s' {{.arg.id}}",
      "commands": [{
        "name": "show",
        "args": [{"name": "id", "type": "int", "required": true}]
      }]
    }`
	p := filepath.Join(dir, "api.json")
	require.NoError(t, os.WriteFile(p, []byte(cfg), 0o600))

	prevOut := execStdout
	var buf bytes.Buffer
	execStdout = &buf
	execStderr = io.Discard
	t.Cleanup(func() {
		execStdout = prevOut
		execStderr = os.Stderr
	})
	prevCode := exitCode
	exitCode = 0
	t.Cleanup(func() { exitCode = prevCode })

	var errOut bytes.Buffer
	code := run([]string{"--config", p, "show", "42"}, &errOut)
	assert.Equal(t, 0, code)
	assert.Equal(t, "42", buf.String())
}

func TestRun_PicksUpCwdAPIJson(t *testing.T) {
	dir := t.TempDir()
	cfg := `{
      "name": "t",
      "command": "true",
      "commands": [{"name":"ping"}]
    }`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "api.json"), []byte(cfg), 0o600))
	chdir(t, dir)

	prevOut := execStdout
	execStdout = io.Discard
	execStderr = io.Discard
	t.Cleanup(func() {
		execStdout = prevOut
		execStderr = os.Stderr
	})

	var errOut bytes.Buffer
	code := run([]string{"ping"}, &errOut)
	assert.Equal(t, 0, code)
}

func TestRegisterFlag_AllTypes(t *testing.T) {
	cfg := &Config{
		Name:    "t",
		Command: &Cmd{Shell: true, Template: "true"},
		Commands: []Command{{
			Name: "x",
			Flags: []Flag{
				{Name: "s", Type: "string", Default: "hi"},
				{Name: "b", Type: "bool", Default: true},
				{Name: "n", Type: "int", Default: float64(7)},
				{Name: "tags", Type: "string-slice", Default: []any{"a", "b"}},
				{Name: "untyped"}, // default "string" fallback
			},
		}},
	}
	require.NoError(t, validate(cfg))
	root := newRoot(cfg)
	cmd, _, err := root.Find([]string{"x"})
	require.NoError(t, err)

	assert.Equal(t, "hi", cmd.Flags().Lookup("s").DefValue)
	assert.Equal(t, "true", cmd.Flags().Lookup("b").DefValue)
	assert.Equal(t, "7", cmd.Flags().Lookup("n").DefValue)
	assert.Equal(t, "[a,b]", cmd.Flags().Lookup("tags").DefValue)
	require.NotNil(t, cmd.Flags().Lookup("untyped"))
}

func TestStringSlice_PreservesCommas(t *testing.T) {
	// StringArrayVar (vs StringSliceVar) keeps commas inside values.
	cfg := &Config{
		Name:    "t",
		Command: &Cmd{Shell: true, Template: "true"},
		Commands: []Command{{
			Name:  "x",
			Flags: []Flag{{Name: "tag", Type: "string-slice"}},
		}},
	}
	require.NoError(t, validate(cfg))
	root := newRoot(cfg)
	cmd, _, err := root.Find([]string{"x"})
	require.NoError(t, err)
	require.NoError(t, cmd.Flags().Parse([]string{"--tag", "a,b", "--tag", "c"}))
	got, err := cmd.Flags().GetStringArray("tag")
	require.NoError(t, err)
	assert.Equal(t, []string{"a,b", "c"}, got)
}
