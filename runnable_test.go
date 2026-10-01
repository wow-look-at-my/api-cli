package main

import (
	"io"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// execErr runs the root and returns cobra's own error, which is where an
// argument rejection lands. execCmdFull discards it with cobra's output.
func execErr(t *testing.T, cfg *Config, argv ...string) error {
	t.Helper()
	t.Serial()
	require.NoError(t, validate(cfg))
	prevTransports, prevDefault := transports, defaultTransport
	t.Cleanup(func() { transports, defaultTransport = prevTransports, prevDefault })

	root := newRoot(cfg)
	root.SetOut(io.Discard)
	root.SetErr(io.Discard)
	root.SetArgs(argv)
	return root.Execute()
}

// runnableConfig is `thing` as a runnable parent: `thing` and `thing <id>` run
// the parent, and `thing list` runs the child.
func runnableConfig() *Config {
	return &Config{
		Name: "t",
		Commands: []Command{{
			Name:     "thing",
			Runnable: true,
			Args:     []Arg{{Name: "id", Pattern: `^[0-9]+$`}},
			Command:  &Cmd{Shell: true, Template: `printf 'parent:%s' '{{ .arg.id }}'`},
			Commands: []Command{{
				Name:    "list",
				Command: &Cmd{Shell: true, Template: `printf 'child'`},
			}},
		}},
	}
}

func TestRunnable_ParentRunsWithNoArg(t *testing.T) {
	code, out := execCmd(t, runnableConfig(), "thing")
	require.Equal(t, 0, code)
	assert.Equal(t, "parent:", out)
}

func TestRunnable_ParentRunsWithAnArg(t *testing.T) {
	code, out := execCmd(t, runnableConfig(), "thing", "42")
	require.Equal(t, 0, code)
	assert.Equal(t, "parent:42", out)
}

func TestRunnable_SubcommandStillWins(t *testing.T) {
	code, out := execCmd(t, runnableConfig(), "thing", "list")
	require.Equal(t, 0, code)
	assert.Equal(t, "child", out)
}

// A value that is neither a subcommand nor a pattern match names both ways out.
func TestRunnable_UnmatchedValueNamesBothOptions(t *testing.T) {
	err := execErr(t, runnableConfig(), "thing", "bogus")
	require.Error(t, err)
	assert.Contains(t, err.Error(), `"bogus" does not match <id>`)
	assert.Contains(t, err.Error(), `^[0-9]+$`)
	assert.Contains(t, err.Error(), "list")
}

// "--" ends the subcommand lookup, which is how a value that starts with a dash
// reaches the parent as an argument.
func TestRunnable_DashDashEndsTheLookup(t *testing.T) {
	cfg := runnableConfig()
	cfg.Commands[0].Args = []Arg{{Name: "id", Pattern: `^-?[0-9]+$`}}
	code, out := execCmd(t, cfg, "thing", "--", "-5")
	require.Equal(t, 0, code)
	assert.Equal(t, "parent:-5", out)
}

// The steps, entry and fields a leaf carries work on a runnable parent too.
func TestRunnable_ParentCarriesStepsAndFields(t *testing.T) {
	cfg := &Config{
		Name: "t",
		Commands: []Command{{
			Name:     "thing",
			Runnable: true,
			Args:     []Arg{{Name: "id", Pattern: `^[0-9]+$`}},
			Steps: []Step{{
				Name:    "seed",
				Command: &Cmd{Shell: true, Template: `printf '{"n":"one"}'`},
			}},
			Command: &Cmd{Shell: true, Template: `printf '[{"name":"a"}]'`},
			Fields: []FieldsBlock{
				{Fields: &Fields{List: []Field{{Name: "name", Path: "name"}}}},
				{Fields: &Fields{Over: "result.seed", List: []Field{{Name: "seeded", Path: "n"}}}},
			},
			Commands: []Command{{
				Name:    "list",
				Command: &Cmd{Shell: true, Template: `printf 'child'`},
			}},
		}},
	}
	code, out := execCmd(t, cfg, "thing")
	require.Equal(t, 0, code)
	assert.Contains(t, out, "name")
	assert.Contains(t, out, "seeded")
}

// A runnable parent is an MCP tool of its own, alongside its children.
func TestRunnable_ParentIsAnMCPTool(t *testing.T) {
	cfg := runnableConfig()
	require.NoError(t, validate(cfg))
	leaves := collectMCPLeaves(cfg.Commands, mcpInherit{vars: cfg.Vars, request: cfg.Request, formats: cfg.Formats})
	var names []string
	for _, l := range leaves {
		names = append(names, l.name)
	}
	assert.Equal(t, []string{"thing", "thing_list"}, names)
}

// A pattern is validation on a leaf as well, where there is no dispatch to
// disambiguate.
func TestArgPattern_EnforcedOnALeaf(t *testing.T) {
	cfg := &Config{
		Name: "t",
		Commands: []Command{{
			Name:    "get",
			Args:    []Arg{{Name: "sha", Pattern: `^[0-9a-f]{7,40}$`}},
			Command: &Cmd{Shell: true, Template: `printf 'ok'`},
		}},
	}
	code, out := execCmd(t, cfg, "get", "abc1234")
	require.Equal(t, 0, code)
	assert.Equal(t, "ok", out)

	err := execErr(t, cfg, "get", "zzz")
	require.Error(t, err)
	assert.Contains(t, err.Error(), `does not match <sha>`)
	assert.NotContains(t, err.Error(), "subcommands")
}

func TestValidate_RunnableErrors(t *testing.T) {
	cases := map[string]struct{ src, want string }{
		"no subcommands": {
			`<config name="x"><command name="c" runnable="true"><run>x</run></command></config>`,
			"runnable= needs subcommands",
		},
		"arg without pattern": {
			`<config name="x"><command name="c" runnable="true"><arg name="id"/><run>x</run>
				<command name="list"><run>y</run></command></command></config>`,
			"needs a pattern=",
		},
		"pattern matches a subcommand": {
			`<config name="x"><command name="c" runnable="true"><arg name="id" pattern=".+"/><run>x</run>
				<command name="list"><run>y</run></command></command></config>`,
			`matches the subcommand name "list"`,
		},
		"pattern matches a cobra name": {
			`<config name="x"><command name="c" runnable="true"><arg name="id" pattern="^[a-z]+$"/><run>x</run>
				<command name="list2"><run>y</run></command></command></config>`,
			`matches the subcommand name "completion"`,
		},
		"pattern does not compile": {
			`<config name="x"><command name="c"><arg name="id" pattern="^([0-9]+$"/><run>x</run></command></config>`,
			"does not compile",
		},
		"runnable and passthrough": {
			`<config name="x"><command name="c" runnable="true" passthrough="true"><run>x</run>
				<command name="list"><run>y</run></command></command></config>`,
			"passthrough is only allowed on leaves",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := loadStr(t, tc.src)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
		})
	}
}

// A parent without runnable= stays help-only, and its leaf-shaped children are
// still rejected.
func TestValidate_FieldsStillNeedANodeThatRuns(t *testing.T) {
	_, err := loadStr(t, `<config name="x"><command name="c"><run>x</run>
		<fields><field name="a">a</field></fields>
		<command name="list"><run>y</run></command></command></config>`)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "needs a node that runs")
}

func TestParseXML_RunnableAndPattern(t *testing.T) {
	cfg := mustParse(t, `<config name="x"><command name="c" runnable="true">
		<arg name="id" pattern="^[0-9]+$"/><run>x</run>
		<command name="list"><run>y</run></command></command></config>`)
	assert.True(t, cfg.Commands[0].Runnable)
	assert.Equal(t, "^[0-9]+$", cfg.Commands[0].Args[0].Pattern)
}
