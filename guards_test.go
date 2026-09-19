package main

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- the path-segment rule ---

func TestSafeSegments_AcceptsOneSegmentAndRefusesAnEscape(t *testing.T) {
	assert.True(t, safeSegments("octocat"))
	assert.True(t, safeSegments("api-cli", "v1.2.3", ".github", "a_b~c"))
	assert.True(t, safeSegments("42"))

	assert.False(t, safeSegments(".."))
	assert.False(t, safeSegments("."))
	assert.False(t, safeSegments("../../etc/passwd"))
	assert.False(t, safeSegments("a/b"))
	assert.False(t, safeSegments(`a\b`))
	assert.False(t, safeSegments("%2e%2e"))
	assert.False(t, safeSegments("a b"))
	assert.False(t, safeSegments("ok", ".."), "one bad value spoils the set")
}

// An absent value has nothing to check, which is what lets one guard cover a
// whole tree of leaves that declare different args.
func TestSafeSegments_TreatsAnAbsentValueAsNothingToCheck(t *testing.T) {
	assert.True(t, safeSegments())
	assert.True(t, safeSegments(nil))
	assert.True(t, safeSegments(""))
	assert.True(t, safeSegments([]string{}))
	assert.True(t, safeSegments([]any{}))
}

// A list contributes every element, so a variadic arg is covered by the same
// call as a scalar one.
func TestSafeSegments_ReadsEveryElementOfAList(t *testing.T) {
	assert.True(t, safeSegments([]string{"a", "b"}))
	assert.False(t, safeSegments([]string{"a", "../b"}))
	assert.False(t, safeSegments([]any{"ok", ".."}))
}

// segmentPattern and safeSegments are the same rule, so a pattern= and a
// precondition cannot disagree.
func TestSegmentPattern_MatchesWhatSafeSegmentsAccepts(t *testing.T) {
	re := regexp.MustCompile(segmentPattern())
	for _, v := range []string{"octocat", "api-cli", "v1.2.3", ".github", "a_b~c", "42", "x"} {
		assert.True(t, re.MatchString(v), v)
		assert.True(t, safeSegments(v), v)
	}
	for _, v := range []string{"", ".", "..", "../x", "a/b", "a b", "%2e%2e"} {
		assert.False(t, re.MatchString(v), v)
	}
}

// --- pattern= naming a var ---

func TestResolveArgPatterns_ReadsAVar(t *testing.T) {
	cfg := loadConfigFile(t, `<config name="t">
		<vars><var name="segment">^[a-z]+$</var></vars>
		<command name="show" description="s">
			<arg name="owner" pattern="{{ .var.segment }}"/>
			<run>printf ok</run>
		</command>
	</config>`)
	assert.Equal(t, `^[a-z]+$`, cfg.Commands[0].Args[0].Pattern)

	err := execErr(t, cfg, "show", "OCTOCAT")
	require.Error(t, err)
	assert.Contains(t, err.Error(), `^[a-z]+$`)
}

// The built-in rule needs no regular expression in the config at all.
func TestResolveArgPatterns_ReadsTheSegmentHelper(t *testing.T) {
	cfg := loadConfigFile(t, `<config name="t">
		<command name="show" description="s">
			<arg name="owner" pattern="{{ segmentPattern }}"/>
			<run>printf ok</run>
		</command>
	</config>`)
	assert.Equal(t, segmentPattern(), cfg.Commands[0].Args[0].Pattern)

	code, out, _ := execCmdFull(t, cfg, "show", "octocat")
	assert.Equal(t, 0, code)
	assert.Contains(t, out, "ok")

	err := execErr(t, cfg, "show", "../../etc/passwd")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "does not match <owner>")
}

// A var in scope at the node wins over the one above it, the same way any other
// var does.
func TestResolveArgPatterns_TakesTheNearestVar(t *testing.T) {
	cfg := loadConfigFile(t, `<config name="t">
		<vars><var name="segment">^[a-z]+$</var></vars>
		<command name="group">
			<vars><var name="segment">^[0-9]+$</var></vars>
			<command name="show" description="s">
				<arg name="id" pattern="{{ .var.segment }}"/>
				<run>printf ok</run>
			</command>
		</command>
	</config>`)
	assert.Equal(t, `^[0-9]+$`, cfg.Commands[0].Commands[0].Args[0].Pattern)
}

// A pattern that resolves to nothing matches everything, which is never what a
// config that declared one meant.
func TestResolveArgPatterns_RefusesAnEmptyResult(t *testing.T) {
	_, err := loadConfigFileErr(t, `<config name="t">
		<command name="show" description="s">
			<arg name="owner" pattern="{{ .var.missing }}"/>
			<run>printf ok</run>
		</command>
	</config>`)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "rendered empty or unresolved")
}

// --- inherited preconditions ---

// One guard at the top of the config runs on every leaf under it, so no leaf
// repeats it.
func TestPreconditions_ConfigLevelGuardsEveryLeaf(t *testing.T) {
	cfg := loadConfigFile(t, `<config name="t">
		<preconditions>
			<precondition>{{ if not (safeSegments .arg.owner .arg.repo) }}owner and repo must each be one path segment{{ end }}</precondition>
		</preconditions>
		<command name="repo" description="r">
			<arg name="owner"/>
			<arg name="repo"/>
			<run>printf ok</run>
		</command>
		<command name="user" description="u">
			<arg name="owner"/>
			<run>printf ok</run>
		</command>
	</config>`)

	for _, argv := range [][]string{
		{"repo", "octocat", "../../etc"},
		{"user", ".."},
	} {
		code, out, errOut := execCmdFull(t, cfg, argv...)
		assert.Equal(t, 1, code, argv)
		assert.NotContains(t, out, "ok", argv)
		assert.Contains(t, errOut, "one path segment", argv)
	}

	code, out, _ := execCmdFull(t, cfg, "repo", "octocat", "api-cli")
	assert.Equal(t, 0, code)
	assert.Contains(t, out, "ok")
}

// A leaf that declares no such arg is not caught by the guard above it, because
// an absent value has nothing to check.
func TestPreconditions_ConfigLevelGuardSpareTheLeafWithoutThatArg(t *testing.T) {
	cfg := loadConfigFile(t, `<config name="t">
		<preconditions>
			<precondition>{{ if not (safeSegments .arg.owner) }}owner must be one path segment{{ end }}</precondition>
		</preconditions>
		<command name="ping" description="p"><run>printf ok</run></command>
	</config>`)

	code, out, _ := execCmdFull(t, cfg, "ping")
	assert.Equal(t, 0, code)
	assert.Contains(t, out, "ok")
}

// A guard on a group node covers its subtree and nothing else.
func TestPreconditions_GroupGuardCoversItsSubtreeOnly(t *testing.T) {
	cfg := loadConfigFile(t, `<config name="t">
		<command name="guarded">
			<preconditions><precondition>guarded is closed</precondition></preconditions>
			<command name="inner" description="i"><run>printf inner</run></command>
		</command>
		<command name="open" description="o"><run>printf open</run></command>
	</config>`)

	code, _, errOut := execCmdFull(t, cfg, "guarded", "inner")
	assert.Equal(t, 1, code)
	assert.Contains(t, errOut, "guarded is closed")

	code, out, _ := execCmdFull(t, cfg, "open")
	assert.Equal(t, 0, code)
	assert.Contains(t, out, "open")
}

// The ancestors' guards run before the node's own, so the broader message is the
// one a reader gets first.
func TestPreconditions_AncestorsRunFirst(t *testing.T) {
	cfg := loadConfigFile(t, `<config name="t">
		<preconditions><precondition>from the config</precondition></preconditions>
		<command name="leaf" description="l">
			<preconditions><precondition>from the leaf</precondition></preconditions>
			<run>printf ok</run>
		</command>
	</config>`)

	// The declared list is untouched. Inheritance happens where the node runs.
	require.Equal(t, []string{"from the leaf"}, cfg.Commands[0].Preconditions)

	code, _, errOut := execCmdFull(t, cfg, "leaf")
	assert.Equal(t, 1, code)
	assert.Contains(t, errOut, "from the config")
	assert.NotContains(t, errOut, "from the leaf")
}

// The MCP path carries the same inherited guards, so a tool call is gated like
// the CLI invocation.
func TestPreconditions_InheritedOverMCP(t *testing.T) {
	cfg := loadConfigFile(t, `<config name="t">
		<preconditions>
			<precondition>{{ if not (safeSegments .arg.owner) }}owner must be one path segment{{ end }}</precondition>
		</preconditions>
		<command name="show" description="s">
			<arg name="owner"/>
			<run>printf ok</run>
		</command>
	</config>`)

	leaves := collectMCPLeaves(cfg.Commands, mcpInherit{vars: cfg.Vars, pre: cfg.Preconditions})
	require.Len(t, leaves, 1)

	out, isErr := mcpExecLeaf(&leaves[0], map[string]any{"owner": "../../etc"})
	assert.True(t, isErr)
	assert.Contains(t, out, "one path segment")

	out, isErr = mcpExecLeaf(&leaves[0], map[string]any{"owner": "octocat"})
	assert.False(t, isErr)
	assert.Contains(t, out, "ok")
}

// A config-level guard cannot read .result either: nothing has run when it does.
func TestPreconditions_ConfigLevelCannotReadResult(t *testing.T) {
	_, err := loadConfigFileErr(t, `<config name="t">
		<preconditions><precondition>{{ .result.x }}</precondition></preconditions>
		<command name="ping" description="p"><run>printf ok</run></command>
	</config>`)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "runs before <steps>")
}

// --- helpers ---

// loadConfigFile goes through Load, which is where a pattern= resolves and where
// a config-level guard is read.
func loadConfigFile(t *testing.T, src string) *Config {
	t.Helper()
	cfg, err := loadConfigFileErr(t, src)
	require.NoError(t, err)
	return cfg
}

func loadConfigFileErr(t *testing.T, src string) (*Config, error) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "api.xml")
	require.NoError(t, os.WriteFile(path, []byte(src), 0o600))
	return Load(path)
}
