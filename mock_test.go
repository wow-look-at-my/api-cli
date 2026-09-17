package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// loadMock parses and validates a config, and fails the test on a load error.
func loadMock(t *testing.T, xml string) *Config {
	t.Helper()
	cfg, err := loadStr(t, xml)
	require.NoError(t, err)
	return cfg
}

// readFile is the assertion a mock exists for: the file the caller expects to
// find after the program ran.
func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	require.NoError(t, err, "%s should exist", path)
	return string(b)
}

const ccMock = `<config name="t">
	<command name="cc" passthrough="true">
		<flag name="o" short="o" type="string"/>
		<flag name="c" type="bool"/>
		<mock>
			<input name="src" match="\.c$" required="true"/>
			<output path="{{ .flag.o }}" when="{{ .flag.o }}"/>
			<output path="{{ stem .mock.src }}.o" when="{{ and .flag.c (not .flag.o) }}"/>
			<record path="db.jsonl"/>
		</mock>
	</command>
</config>`

func TestMock_WritesTheDeclaredOutput(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)

	code, out := execCmd(t, loadMock(t, ccMock), "cc", "--", "-c", "src/foo.c")
	assert.Equal(t, 0, code)
	assert.Empty(t, out)

	assert.FileExists(t, filepath.Join(dir, "foo.o"))
	assert.Empty(t, readFile(t, filepath.Join(dir, "foo.o")), "a mock artifact is empty by default")
}

// -o wins over the derived name, which is what the real tool does and what a
// Makefile written against the real tool expects.
func TestMock_OutputFlagWinsOverTheDerivedName(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)

	code, _ := execCmd(t, loadMock(t, ccMock), "cc", "--", "-c", "-o", "build/out.o", "src/foo.c")
	assert.Equal(t, 0, code)

	assert.FileExists(t, filepath.Join(dir, "build", "out.o"), "the parent directory is created")
	assert.NoFileExists(t, filepath.Join(dir, "foo.o"))
}

// The record is the reason this exists: a build writes its own compilation
// database on the way past.
func TestMock_RecordIsACompileCommandsEntry(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)

	code, _ := execCmd(t, loadMock(t, ccMock), "cc", "--", "-c", "src/foo.c")
	require.Equal(t, 0, code)

	lines := strings.Split(strings.TrimSpace(readFile(t, filepath.Join(dir, "db.jsonl"))), "\n")
	require.Len(t, lines, 1)

	var entry struct {
		Directory string   `json:"directory"`
		Arguments []string `json:"arguments"`
		File      string   `json:"file"`
		Output    string   `json:"output"`
	}
	require.NoError(t, json.Unmarshal([]byte(lines[0]), &entry))
	assert.Equal(t, dir, entry.Directory)
	assert.Equal(t, []string{"-c", "src/foo.c"}, entry.Arguments)
	assert.Equal(t, "src/foo.c", entry.File)
	assert.Equal(t, "foo.o", entry.Output)
}

// Every call appends, so N tools in a parallel build share one record file.
func TestMock_RecordAppendsPerCall(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	cfg := loadMock(t, ccMock)

	for _, src := range []string{"a.c", "b.c", "c.c"} {
		code, _ := execCmd(t, cfg, "cc", "--", "-c", src)
		require.Equal(t, 0, code)
	}

	lines := strings.Split(strings.TrimSpace(readFile(t, filepath.Join(dir, "db.jsonl"))), "\n")
	assert.Len(t, lines, 3)
}

// A variadic input collects every match, and an <output over=> turns that list
// into one artifact per element.
func TestMock_VariadicInputFansOutToOneOutputEach(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)

	cfg := loadMock(t, `<config name="t">
	<command name="cc" passthrough="true">
		<mock>
			<input name="sources" match="\.c$" variadic="true" required="true"/>
			<output over="mock.sources" path="build/{{ stem .item }}.o"/>
		</mock>
	</command>
</config>`)

	code, _ := execCmd(t, cfg, "cc", "--", "-c", "a.c", "b.c", "sub/c.c")
	require.Equal(t, 0, code)

	for _, name := range []string{"a.o", "b.o", "c.o"} {
		assert.FileExists(t, filepath.Join(dir, "build", name))
	}
}

// A body and a mode are what make a stand-in plausible to the next tool.
func TestMock_BodyAndModeReachTheFile(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)

	cfg := loadMock(t, `<config name="t">
	<command name="ld" passthrough="true">
		<mock>
			<input name="objects" match="\.o$" variadic="true"/>
			<output path="app" mode="0755">#!/bin/sh
<for each="mock.objects"># <value name="."/>
</for></output>
		</mock>
	</command>
</config>`)

	code, _ := execCmd(t, cfg, "ld", "--", "a.o", "b.o")
	require.Equal(t, 0, code)

	body := readFile(t, filepath.Join(dir, "app"))
	assert.Contains(t, body, "# a.o")
	assert.Contains(t, body, "# b.o")

	info, err := os.Stat(filepath.Join(dir, "app"))
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o755), info.Mode().Perm())
}

// A mock stands in for a tool that fails too, and a build must see the failure.
func TestMock_ExitCodeIsATemplate(t *testing.T) {
	chdir(t, t.TempDir())

	cfg := loadMock(t, `<config name="t">
	<command name="cc" passthrough="true">
		<mock exit="{{ if .mock.src }}0{{ else }}1{{ end }}">
			<input name="src" match="\.c$"/>
			<stderr>cc: no input files
</stderr>
		</mock>
	</command>
</config>`)

	code, _, errOut := execCmdFull(t, cfg, "cc", "--", "-v")
	assert.Equal(t, 1, code)
	assert.Equal(t, "cc: no input files\n", errOut)
}

// A required input that matches nothing is a broken invocation, not an empty
// string that renders an output path named ".o".
func TestMock_RequiredInputThatMatchesNothingFails(t *testing.T) {
	chdir(t, t.TempDir())

	code, _ := execCmd(t, loadMock(t, ccMock), "cc", "--", "-c")
	assert.Equal(t, 1, code, "a broken invocation fails rather than writing a file named .o")
	assert.NoFileExists(t, ".o")
}

// The thin-wrapper shape: the record lands, then the leaf's own run executes.
func TestMock_OwnRunMakesItAThinWrapper(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)

	cfg := loadMock(t, `<config name="t">
	<command name="cc" passthrough="true">
		<mock>
			<input name="src" match="\.c$" required="true"/>
			<record path="db.jsonl">{"file":<value expr="{{ .mock.src | toJson }}"/>}</record>
		</mock>
		<run>echo wrapped {{ .mock.src }}</run>
	</command>
</config>`)

	code, out := execCmd(t, cfg, "cc", "--", "src/foo.c")
	assert.Equal(t, 0, code)
	assert.Equal(t, "wrapped src/foo.c\n", out)
	assert.JSONEq(t, `{"file":"src/foo.c"}`, strings.TrimSpace(readFile(t, filepath.Join(dir, "db.jsonl"))))
}

// An INHERITED run is not the leaf's own, so it stays where it is: a mock leaf
// under a parent that declares a run does not fire that run on the way past.
func TestMock_InheritedRunDoesNotFire(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)

	cfg := loadMock(t, `<config name="t">
	<command name="tools">
		<run>echo inherited</run>
		<command name="cc" passthrough="true">
			<mock>
				<output path="out.o"/>
			</mock>
		</command>
	</command>
</config>`)

	code, out := execCmd(t, cfg, "tools", "cc", "--", "foo.c")
	assert.Equal(t, 0, code)
	assert.Empty(t, out, "the ancestor's run is not this leaf's action")
	assert.FileExists(t, filepath.Join(dir, "out.o"))
}

// from= copies a real artifact, which is how a mock returns a fixture that has
// to parse downstream.
func TestMock_FromCopiesAFixture(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "fixture.o"), []byte("ELF-ish"), 0o644))

	cfg := loadMock(t, `<config name="t">
	<command name="cc" passthrough="true">
		<mock>
			<output path="out.o" from="fixture.o"/>
		</mock>
	</command>
</config>`)

	code, _ := execCmd(t, cfg, "cc", "--", "foo.c")
	require.Equal(t, 0, code)
	assert.Equal(t, "ELF-ish", readFile(t, filepath.Join(dir, "out.o")))
}

func TestMock_LoadErrors(t *testing.T) {
	cases := map[string]struct{ xml, want string }{
		"no input name": {
			`<config name="t"><command name="c" passthrough="true"><mock><input match="x"/></mock></command></config>`,
			`"name" is required`,
		},
		"no input match": {
			`<config name="t"><command name="c" passthrough="true"><mock><input name="src"/></mock></command></config>`,
			`"match" is required`,
		},
		"bad pattern": {
			`<config name="t"><command name="c" passthrough="true"><mock><input name="src" match="([a-z"/></mock></command></config>`,
			"error parsing regexp",
		},
		"required and default": {
			`<config name="t"><command name="c" passthrough="true"><mock><input name="s" match="x" required="true" default="y"/><output path="o"/></mock></command></config>`,
			"required= and default= cannot both hold",
		},
		"reserved input name": {
			`<config name="t"><command name="c" passthrough="true"><mock><input name="argv" match="x"/><output path="o"/></mock></command></config>`,
			`name "argv" is reserved`,
		},
		"duplicate input": {
			`<config name="t"><command name="c" passthrough="true"><mock><input name="s" match="a"/><input name="s" match="b"/><output path="o"/></mock></command></config>`,
			`duplicate input name "s"`,
		},
		"no output path": {
			`<config name="t"><command name="c" passthrough="true"><mock><output/></mock></command></config>`,
			`"path" is required`,
		},
		"from and body": {
			`<config name="t"><command name="c" passthrough="true"><mock><output path="o" from="f">text</output></mock></command></config>`,
			"from= and a text body cannot both hold",
		},
		"bad mode": {
			`<config name="t"><command name="c" passthrough="true"><mock><output path="o" mode="rwxr-xr-x"/></mock></command></config>`,
			"must be octal",
		},
		"unknown child": {
			`<config name="t"><command name="c" passthrough="true"><mock><nope/></mock></command></config>`,
			"unknown element <nope>",
		},
		"unknown attribute": {
			`<config name="t"><command name="c" passthrough="true"><mock><output path="o" unknown="x"/></mock></command></config>`,
			"unknown",
		},
		"stands in for nothing": {
			`<config name="t"><command name="c" passthrough="true"><mock><input name="s" match="x"/></mock></command></config>`,
			"stands in for nothing",
		},
		"not a leaf": {
			`<config name="t"><command name="p"><mock><output path="o"/></mock><command name="c"><run>x</run></command></command></config>`,
			"<mock> is only allowed on leaves",
		},
		"mock and request": {
			`<config name="t"><command name="c"><mock><output path="o"/></mock><run><request><url>http://x</url></request></run></command></config>`,
			"<mock> and <request> cannot both hold",
		},
		"mock and download": {
			`<config name="t"><command name="c"><mock><output path="o"/></mock><download><url>http://x</url></download></command></config>`,
			"<mock> and <download> cannot both hold",
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := loadStr(t, tc.xml)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
		})
	}
}

func TestParseFileMode(t *testing.T) {
	for in, want := range map[string]os.FileMode{"0644": 0o644, "644": 0o644, "0755": 0o755, "": defaultMockMode} {
		got, err := parseFileMode(in)
		require.NoError(t, err, in)
		assert.Equal(t, want, os.FileMode(got), in)
	}
	_, err := parseFileMode("rwx")
	assert.Error(t, err)
}

func TestStem(t *testing.T) {
	assert.Equal(t, "bar", stem("src/foo/bar.cpp"))
	assert.Equal(t, "bar", stem("bar"))
	assert.Equal(t, "", stem(""))
}
