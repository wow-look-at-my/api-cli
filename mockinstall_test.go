package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// writeConfig drops a config into a temp directory and returns its path.
func writeConfig(t *testing.T, xml string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "mocks.xml")
	require.NoError(t, os.WriteFile(path, []byte(xml), 0o644))
	return path
}

// captureStdout swaps the package's output channel for the duration of a test.
func captureStdout(t *testing.T) *bytes.Buffer {
	t.Helper()
	t.Serial()
	var buf bytes.Buffer
	prev := execStdout
	execStdout = &buf
	t.Cleanup(func() { execStdout = prev })
	return &buf
}

const installable = `<config name="t">
	<command name="cc" passthrough="true">
		<mock><output path="out.o"/></mock>
	</command>
	<command name="tools">
		<command name="ld" passthrough="true">
			<mock><output path="app"/></mock>
		</command>
		<command name="unrelated"><run>true</run></command>
	</command>
</config>`

// One XML in, a directory of executables out. That directory on PATH is the
// whole installation.
func TestInstallMocks_WritesOneExecutablePerMockLeaf(t *testing.T) {
	cfgPath := writeConfig(t, installable)
	dir := filepath.Join(t.TempDir(), "bin")
	out := captureStdout(t)

	var errOut bytes.Buffer
	code := run([]string{"--config", cfgPath, "--install-mocks", dir}, &errOut)
	require.Equal(t, 0, code, errOut.String())

	for _, name := range []string{"cc", "ld"} {
		info, err := os.Stat(filepath.Join(dir, name))
		require.NoError(t, err, "%s should be installed", name)
		assert.Equal(t, os.FileMode(0o755), info.Mode().Perm(), "%s must be executable", name)
	}
	assert.NoFileExists(t, filepath.Join(dir, "unrelated"), "a leaf without a <mock> installs nothing")
	assert.Contains(t, out.String(), "export PATH=")
}

// A nested leaf's script names the whole path, so the wrapper reaches the right
// leaf however deep it sits.
func TestInstallMocks_ScriptNamesTheLeafPath(t *testing.T) {
	cfgPath := writeConfig(t, installable)
	dir := filepath.Join(t.TempDir(), "bin")
	captureStdout(t)

	var errOut bytes.Buffer
	require.Equal(t, 0, run([]string{"--config", cfgPath, "--install-mocks", dir}, &errOut), errOut.String())

	body, err := os.ReadFile(filepath.Join(dir, "ld"))
	require.NoError(t, err)
	script := string(body)
	assert.Contains(t, script, "#!/bin/sh")
	assert.Contains(t, script, "'tools' 'ld'")
	assert.Contains(t, script, `-- "$@"`)
	assert.Contains(t, script, cfgPath, "the config path is absolute, for a build that runs from anywhere")
}

// Two leaves of one name would install one script, and the second would win in
// silence. A build then calls a stand-in nobody can trace back.
func TestInstallMocks_DuplicateLeafNameFails(t *testing.T) {
	cfgPath := writeConfig(t, `<config name="t">
	<command name="cc" passthrough="true">
		<mock><output path="a"/></mock>
	</command>
	<command name="alt">
		<command name="cc" passthrough="true">
			<mock><output path="b"/></mock>
		</command>
	</command>
</config>`)
	captureStdout(t)

	var errOut bytes.Buffer
	code := run([]string{"--config", cfgPath, "--install-mocks", filepath.Join(t.TempDir(), "bin")}, &errOut)
	assert.Equal(t, 2, code)
	assert.Contains(t, errOut.String(), `both named "cc"`)
}

func TestInstallMocks_ConfigWithNoMockFails(t *testing.T) {
	cfgPath := writeConfig(t, `<config name="t"><command name="c"><run>true</run></command></config>`)
	captureStdout(t)

	var errOut bytes.Buffer
	code := run([]string{"--config", cfgPath, "--install-mocks", filepath.Join(t.TempDir(), "bin")}, &errOut)
	assert.Equal(t, 2, code)
	assert.Contains(t, errOut.String(), "declares no <mock> leaf")
}

func TestCollectMockWrappers_FindsNestedLeaves(t *testing.T) {
	cfg, err := Load([]byte(installable))
	require.NoError(t, err)

	got := collectMockWrappers(cfg.Commands, nil)
	require.Len(t, got, 2)
	assert.Equal(t, []string{"cc"}, got[0].path)
	assert.Equal(t, []string{"tools", "ld"}, got[1].path)
}
