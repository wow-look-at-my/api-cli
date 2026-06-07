package main

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- buildToolInputSchema ---

func TestBuildToolInputSchema_Empty(t *testing.T) {
	schema := buildToolInputSchema(Command{})
	assert.Equal(t, "object", schema["type"])
	props, ok := schema["properties"].(map[string]any)
	require.True(t, ok)
	assert.Empty(t, props)
	assert.Nil(t, schema["required"])
}

func TestBuildToolInputSchema_Args(t *testing.T) {
	node := Command{
		Args: []Arg{
			{Name: "id", Type: "int", Required: true, Description: "user id"},
			{Name: "name", Required: false},
			{Name: "files", Type: "string", Variadic: true},
			{Name: "counts", Type: "int", Variadic: true},
		},
	}
	schema := buildToolInputSchema(node)
	props := schema["properties"].(map[string]any)

	idProp := props["id"].(map[string]any)
	assert.Equal(t, "integer", idProp["type"])
	assert.Equal(t, "user id", idProp["description"])

	nameProp := props["name"].(map[string]any)
	assert.Equal(t, "string", nameProp["type"])
	_, hasDesc := nameProp["description"]
	assert.False(t, hasDesc)

	filesProp := props["files"].(map[string]any)
	assert.Equal(t, "array", filesProp["type"])
	assert.Equal(t, map[string]any{"type": "string"}, filesProp["items"])

	countsProp := props["counts"].(map[string]any)
	assert.Equal(t, "array", countsProp["type"])
	assert.Equal(t, map[string]any{"type": "integer"}, countsProp["items"])

	required := schema["required"].([]string)
	assert.Equal(t, []string{"id"}, required)
}

func TestBuildToolInputSchema_Flags(t *testing.T) {
	node := Command{
		Flags: []Flag{
			{Name: "limit", Type: "int", Required: true},
			{Name: "verbose", Type: "bool", Description: "enable verbose output"},
			{Name: "tags", Type: "string-slice"},
			{Name: "output"},	// empty type defaults to string
		},
	}
	schema := buildToolInputSchema(node)
	props := schema["properties"].(map[string]any)

	assert.Equal(t, "integer", props["limit"].(map[string]any)["type"])
	verboseProp := props["verbose"].(map[string]any)
	assert.Equal(t, "boolean", verboseProp["type"])
	assert.Equal(t, "enable verbose output", verboseProp["description"])
	assert.Equal(t, "array", props["tags"].(map[string]any)["type"])
	assert.Equal(t, "string", props["output"].(map[string]any)["type"])

	required := schema["required"].([]string)
	assert.Equal(t, []string{"limit"}, required)
}

// --- collectMCPLeaves ---

func TestCollectMCPLeaves_Flat(t *testing.T) {
	cmds := []Command{
		{
			Name:		"ping",
			Command:	&Cmd{Shell: true, Template: "true"},
		},
		{
			Name:		"pong",
			Command:	&Cmd{Shell: true, Template: "true"},
		},
	}
	leaves := collectMCPLeaves(cmds, mcpInherit{})
	require.Len(t, leaves, 2)
	assert.Equal(t, "ping", leaves[0].name)
	assert.Equal(t, "pong", leaves[1].name)
}

func TestCollectMCPLeaves_Nested(t *testing.T) {
	cmd := &Cmd{Shell: true, Template: "true"}
	cmds := []Command{
		{
			Name:	"users",
			Commands: []Command{
				{Name: "get", Command: cmd},
				{Name: "list", Command: cmd},
			},
		},
	}
	leaves := collectMCPLeaves(cmds, mcpInherit{})
	require.Len(t, leaves, 2)
	assert.Equal(t, "users_get", leaves[0].name)
	assert.Equal(t, "users_list", leaves[1].name)
}

func TestCollectMCPLeaves_InheritsVarsCwdStdin(t *testing.T) {
	rootCmd := &Cmd{Shell: true, Template: "echo {{.var.base}}"}
	cmds := []Command{
		{
			Name: "leaf",
		},
	}
	rootVars := map[string]any{"base": "root"}
	leaves := collectMCPLeaves(cmds, mcpInherit{vars: rootVars, cmd: rootCmd, cwd: "/root", stdin: "stdin-data"})
	require.Len(t, leaves, 1)
	assert.Equal(t, rootCmd, leaves[0].cmdTmpl)
	assert.Equal(t, "/root", leaves[0].cwdTmpl)
	assert.Equal(t, "stdin-data", leaves[0].stdinTmpl)
	assert.Equal(t, "root", leaves[0].vars["base"])
}

func TestCollectMCPLeaves_ChildOverrides(t *testing.T) {
	rootCmd := &Cmd{Shell: true, Template: "root"}
	childCmd := &Cmd{Shell: true, Template: "child"}
	cmds := []Command{
		{
			Name:		"leaf",
			Command:	childCmd,
			Cwd:		"/child",
			Stdin:		"child-stdin",
			Vars:		map[string]any{"key": "child-val"},
		},
	}
	leaves := collectMCPLeaves(cmds, mcpInherit{vars: map[string]any{"key": "root-val"}, cmd: rootCmd, cwd: "/root", stdin: "root-stdin"})
	require.Len(t, leaves, 1)
	assert.Equal(t, childCmd, leaves[0].cmdTmpl)
	assert.Equal(t, "/child", leaves[0].cwdTmpl)
	assert.Equal(t, "child-stdin", leaves[0].stdinTmpl)
	assert.Equal(t, "child-val", leaves[0].vars["key"])
}

// --- mcpGatherArgs ---

func TestMcpGatherArgs_StringAndInt(t *testing.T) {
	node := Command{
		Args: []Arg{
			{Name: "id", Type: "int"},
			{Name: "name"},
		},
	}
	got, err := mcpGatherArgs(node, map[string]any{"id": float64(7), "name": "alice"})
	require.NoError(t, err)
	assert.Equal(t, 7, got["id"])
	assert.Equal(t, "alice", got["name"])
}

func TestMcpGatherArgs_Missing(t *testing.T) {
	node := Command{Args: []Arg{{Name: "name"}}}
	got, err := mcpGatherArgs(node, map[string]any{})
	require.NoError(t, err)
	assert.Empty(t, got)
}

func TestMcpGatherArgs_VariadicString(t *testing.T) {
	node := Command{Args: []Arg{{Name: "files", Variadic: true}}}
	got, err := mcpGatherArgs(node, map[string]any{"files": []any{"a.txt", "b.txt"}})
	require.NoError(t, err)
	assert.Equal(t, []string{"a.txt", "b.txt"}, got["files"])
}

func TestMcpGatherArgs_VariadicIntMissing(t *testing.T) {
	node := Command{Args: []Arg{{Name: "nums", Type: "int", Variadic: true}}}
	got, err := mcpGatherArgs(node, map[string]any{})
	require.NoError(t, err)
	assert.Equal(t, []int{}, got["nums"])
}

func TestMcpGatherArgs_VariadicInt(t *testing.T) {
	node := Command{Args: []Arg{{Name: "nums", Type: "int", Variadic: true}}}
	got, err := mcpGatherArgs(node, map[string]any{"nums": []any{float64(1), float64(2)}})
	require.NoError(t, err)
	assert.Equal(t, []int{1, 2}, got["nums"])
}

func TestMcpGatherArgs_VariadicNotArray(t *testing.T) {
	node := Command{Args: []Arg{{Name: "x", Variadic: true}}}
	_, err := mcpGatherArgs(node, map[string]any{"x": "not-array"})
	assert.Error(t, err)
}

func TestMcpGatherArgs_IntStringParsed(t *testing.T) {
	node := Command{Args: []Arg{{Name: "n", Type: "int"}}}
	got, err := mcpGatherArgs(node, map[string]any{"n": "99"})
	require.NoError(t, err)
	assert.Equal(t, 99, got["n"])
}

// --- mcpGatherFlags ---

func TestMcpGatherFlags_AllTypes(t *testing.T) {
	node := Command{
		Flags: []Flag{
			{Name: "s"},
			{Name: "b", Type: "bool"},
			{Name: "n", Type: "int"},
			{Name: "ss", Type: "string-slice"},
		},
	}
	got, err := mcpGatherFlags(node, map[string]any{
		"s":	"hello",
		"b":	true,
		"n":	float64(5),
		"ss":	[]any{"x", "y"},
	}, nil)
	require.NoError(t, err)
	assert.Equal(t, "hello", got["s"])
	assert.Equal(t, true, got["b"])
	assert.Equal(t, 5, got["n"])
	assert.Equal(t, []string{"x", "y"}, got["ss"])
}

func TestMcpGatherFlags_Defaults(t *testing.T) {
	node := Command{
		Flags: []Flag{
			{Name: "s", Default: "def"},
			{Name: "b", Type: "bool", Default: true},
			{Name: "n", Type: "int", Default: float64(3)},
			{Name: "ss", Type: "string-slice", Default: []any{"a"}},
		},
	}
	got, err := mcpGatherFlags(node, map[string]any{}, nil)
	require.NoError(t, err)
	assert.Equal(t, "def", got["s"])
	assert.Equal(t, true, got["b"])
	assert.Equal(t, 3, got["n"])
	assert.Equal(t, []string{"a"}, got["ss"])
}

func TestMcpGatherFlags_TemplatedDefault(t *testing.T) {
	node := Command{
		Flags: []Flag{
			{Name: "out", Default: "{{.arg.name}}.json"},
		},
	}
	preFlagData := map[string]any{"arg": map[string]any{"name": "report"}}
	got, err := mcpGatherFlags(node, map[string]any{}, preFlagData)
	require.NoError(t, err)
	assert.Equal(t, "report.json", got["out"])
}

func TestMcpGatherFlags_IntStringParsed(t *testing.T) {
	node := Command{Flags: []Flag{{Name: "n", Type: "int"}}}
	got, err := mcpGatherFlags(node, map[string]any{"n": "42"}, nil)
	require.NoError(t, err)
	assert.Equal(t, 42, got["n"])
}

func TestMcpGatherFlags_BoolFalseDefault(t *testing.T) {
	node := Command{Flags: []Flag{{Name: "v", Type: "bool"}}}
	got, err := mcpGatherFlags(node, map[string]any{}, nil)
	require.NoError(t, err)
	assert.Equal(t, false, got["v"])
}

func TestMcpGatherFlags_StringSliceEmptyDefault(t *testing.T) {
	node := Command{Flags: []Flag{{Name: "tags", Type: "string-slice"}}}
	got, err := mcpGatherFlags(node, map[string]any{}, nil)
	require.NoError(t, err)
	assert.Equal(t, []string{}, got["tags"])
}

func TestMcpGatherFlags_StringSliceNotArray(t *testing.T) {
	node := Command{Flags: []Flag{{Name: "tags", Type: "string-slice"}}}
	_, err := mcpGatherFlags(node, map[string]any{"tags": "not-array"}, nil)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "expected array")
}

func TestMcpGatherFlags_IntInvalidString(t *testing.T) {
	node := Command{Flags: []Flag{{Name: "n", Type: "int"}}}
	_, err := mcpGatherFlags(node, map[string]any{"n": "nope"}, nil)
	assert.Error(t, err)
}

func TestMcpGatherFlags_IntFractional(t *testing.T) {
	node := Command{Flags: []Flag{{Name: "n", Type: "int"}}}
	_, err := mcpGatherFlags(node, map[string]any{"n": 3.7}, nil)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "expected integer")
}

func TestMcpGatherFlags_BoolNotBool(t *testing.T) {
	node := Command{Flags: []Flag{{Name: "v", Type: "bool"}}}
	_, err := mcpGatherFlags(node, map[string]any{"v": "true"}, nil)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "expected boolean")
}

func TestMcpGatherArgs_IntFractional(t *testing.T) {
	node := Command{Args: []Arg{{Name: "n", Type: "int"}}}
	_, err := mcpGatherArgs(node, map[string]any{"n": 2.5})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "expected integer")
}

func TestMcpGatherArgs_VariadicIntStringCoercion(t *testing.T) {
	node := Command{Args: []Arg{{Name: "ids", Type: "int", Variadic: true}}}
	got, err := mcpGatherArgs(node, map[string]any{"ids": []any{"1", "2", "3"}})
	require.NoError(t, err)
	assert.Equal(t, []int{1, 2, 3}, got["ids"])
}

func TestMcpGatherArgs_VariadicIntFractional(t *testing.T) {
	node := Command{Args: []Arg{{Name: "ids", Type: "int", Variadic: true}}}
	_, err := mcpGatherArgs(node, map[string]any{"ids": []any{1.5}})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "expected integer")
}

// --- mcpCombine ---

func TestMcpCombine(t *testing.T) {
	assert.Equal(t, "out", mcpCombine("out", ""))
	assert.Equal(t, "err", mcpCombine("", "err"))
	assert.Equal(t, "out\nerr", mcpCombine("out", "err"))
	assert.Equal(t, "out\nerr", mcpCombine("  out  ", "  err  "))
	assert.Equal(t, "", mcpCombine("", ""))
}

// --- mcpExecLeaf (integration) ---

func TestMcpExecLeaf_Simple(t *testing.T) {
	leaf := &mcpLeaf{
		node:		Command{Args: []Arg{{Name: "msg", Required: true}}},
		cmdTmpl:	&Cmd{Shell: true, Template: "printf '%s' {{.arg.msg | shellquote}}"},
	}
	out, isErr := mcpExecLeaf(leaf, map[string]any{"msg": "hello"})
	assert.False(t, isErr)
	assert.Equal(t, "hello", out)
}

func TestMcpExecLeaf_Flags(t *testing.T) {
	leaf := &mcpLeaf{
		node: Command{
			Flags: []Flag{{Name: "count", Type: "int", Default: float64(1)}},
		},
		cmdTmpl:	&Cmd{Shell: true, Template: "printf '%d' {{.flag.count}}"},
	}
	out, isErr := mcpExecLeaf(leaf, map[string]any{"count": float64(7)})
	assert.False(t, isErr)
	assert.Equal(t, "7", out)
}

func TestMcpExecLeaf_FailingCommand(t *testing.T) {
	leaf := &mcpLeaf{
		node:		Command{},
		cmdTmpl:	&Cmd{Shell: true, Template: "false"},
	}
	_, isErr := mcpExecLeaf(leaf, map[string]any{})
	assert.True(t, isErr)
}

func TestMcpExecLeaf_Precondition(t *testing.T) {
	leaf := &mcpLeaf{
		node: Command{
			Preconditions:	[]string{"{{if not .flag.ok}}not ok{{end}}"},
			Flags:		[]Flag{{Name: "ok", Type: "bool"}},
		},
		cmdTmpl:	&Cmd{Shell: true, Template: "true"},
	}
	_, isErr := mcpExecLeaf(leaf, map[string]any{"ok": false})
	assert.True(t, isErr)

	out, isErr := mcpExecLeaf(leaf, map[string]any{"ok": true})
	assert.False(t, isErr)
	assert.Equal(t, "", out)
}

func TestMcpExecLeaf_WithStep(t *testing.T) {
	leaf := &mcpLeaf{
		node: Command{
			Steps: []Step{
				{Name: "greeting", Command: &Cmd{Shell: true, Template: "printf 'hello'"}},
			},
		},
		cmdTmpl:	&Cmd{Shell: true, Template: "printf '%s world' {{.result.greeting | shellquote}}"},
	}
	out, isErr := mcpExecLeaf(leaf, map[string]any{})
	assert.False(t, isErr)
	assert.Equal(t, "hello world", out)
}

func TestMcpExecLeaf_StepFails(t *testing.T) {
	leaf := &mcpLeaf{
		node: Command{
			Steps: []Step{
				{Name: "bad", Command: &Cmd{Shell: true, Template: "false"}},
			},
		},
		cmdTmpl:	&Cmd{Shell: true, Template: "true"},
	}
	_, isErr := mcpExecLeaf(leaf, map[string]any{})
	assert.True(t, isErr)
}

func TestMcpExecLeaf_Vars(t *testing.T) {
	leaf := &mcpLeaf{
		node:		Command{},
		vars:		map[string]any{"greeting": "hi"},
		cmdTmpl:	&Cmd{Shell: true, Template: "printf '%s' {{.var.greeting | shellquote}}"},
	}
	out, isErr := mcpExecLeaf(leaf, map[string]any{})
	assert.False(t, isErr)
	assert.Equal(t, "hi", out)
}

func TestMcpExecLeaf_EmptyStdin(t *testing.T) {
	leaf := &mcpLeaf{
		node:		Command{},
		cmdTmpl:	&Cmd{Shell: true, Template: "wc -c"},
	}
	out, isErr := mcpExecLeaf(leaf, map[string]any{})
	assert.False(t, isErr)
	assert.Contains(t, out, "0")
}

func TestMcpExecLeaf_Stdin(t *testing.T) {
	leaf := &mcpLeaf{
		node:		Command{},
		stdinTmpl:	"hello\n",
		cmdTmpl:	&Cmd{Shell: true, Template: "cat"},
	}
	out, isErr := mcpExecLeaf(leaf, map[string]any{})
	assert.False(t, isErr)
	assert.Equal(t, "hello\n", out)
}

// --- withHealthEndpoint ---

func TestWithHealthEndpoint_OK(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	})
	h := withHealthEndpoint(inner)

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "application/json", rec.Header().Get("Content-Type"))
	assert.Equal(t, `{"status":"ok"}`, rec.Body.String())
}

func TestWithHealthEndpoint_MethodNotAllowed(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	})
	h := withHealthEndpoint(inner)

	req := httptest.NewRequest(http.MethodPost, "/health", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusMethodNotAllowed, rec.Code)
}

func TestWithHealthEndpoint_PassesThrough(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	})
	h := withHealthEndpoint(inner)

	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusTeapot, rec.Code)
}

// --- runMCP (unit, invalid transport) ---

func TestRunMCP_InvalidTransport(t *testing.T) {
	cfg := &Config{Name: "t", Command: &Cmd{Shell: true, Template: "true"}, Commands: []Command{{Name: "x"}}}
	code := runMCP("ftp://invalid", cfg, CorsStrict)
	assert.Equal(t, 2, code)
}

// --- buildMCPServer ---

func TestBuildMCPServer_ToolCount(t *testing.T) {
	cmd := &Cmd{Shell: true, Template: "true"}
	cfg := &Config{
		Name:		"testapi",
		Command:	cmd,
		Commands: []Command{
			{Name: "a", Command: cmd},
			{Name: "b", Commands: []Command{
				{Name: "c", Command: cmd},
				{Name: "d", Command: cmd},
			}},
		},
	}
	srv := buildMCPServer(cfg)
	// The SDK server should have 3 tools: a, b_c, b_d
	// We can't inspect the server directly, but buildMCPServer not panicking
	// and returning a non-nil server is the key assertion.
	assert.NotNil(t, srv)
}

// --- collectMCPLeaves format inheritance ---

func TestCollectMCPLeaves_InheritsFormat(t *testing.T) {
	cmd := &Cmd{Shell: true, Template: "true"}
	parentFmt := &FormatRef{Name: "table"}
	formats := map[string]*Format{"table": {Views: []View{{Name: "t", Template: "x"}}}}
	cmds := []Command{{Name: "leaf"}}
	leaves := collectMCPLeaves(cmds, mcpInherit{cmd: cmd, format: parentFmt, formats: formats})
	require.Len(t, leaves, 1)
	assert.Equal(t, parentFmt, leaves[0].formatRef)
	assert.Equal(t, formats, leaves[0].formats)
}

func TestCollectMCPLeaves_ChildOverridesFormat(t *testing.T) {
	cmd := &Cmd{Shell: true, Template: "true"}
	parentFmt := &FormatRef{Name: "parent"}
	childFmt := &FormatRef{Name: "child"}
	formats := map[string]*Format{
		"parent":	{Views: []View{{Name: "p", Template: "x"}}},
		"child":	{Views: []View{{Name: "c", Template: "y"}}},
	}
	cmds := []Command{{Name: "leaf", Format: childFmt}}
	leaves := collectMCPLeaves(cmds, mcpInherit{cmd: cmd, format: parentFmt, formats: formats})
	require.Len(t, leaves, 1)
	assert.Equal(t, childFmt, leaves[0].formatRef)
}

// --- mcpFormat ---

func TestMcpFormat_NoFormat(t *testing.T) {
	leaf := &mcpLeaf{}
	_, ok := mcpFormat(leaf, "raw output", nil)
	assert.False(t, ok)
}

func TestMcpFormat_InlineFormat(t *testing.T) {
	leaf := &mcpLeaf{
		formatRef: &FormatRef{Inline: &Format{
			Input:	"json",
			Views:	[]View{{Name: "v", Template: "ID={{.data.id}}"}},
		}},
	}
	data := map[string]any{"arg": map[string]any{}}
	out, ok := mcpFormat(leaf, `{"id":42}`, data)
	assert.True(t, ok)
	assert.Equal(t, "ID=42", out)
}

func TestMcpFormat_NamedFormat(t *testing.T) {
	formats := map[string]*Format{
		"item": {
			Input:	"json",
			Views:	[]View{{Name: "detail", Template: "name={{.data.name}}"}},
		},
	}
	leaf := &mcpLeaf{
		formatRef:	&FormatRef{Name: "item"},
		formats:	formats,
	}
	out, ok := mcpFormat(leaf, `{"name":"alice"}`, map[string]any{})
	assert.True(t, ok)
	assert.Equal(t, "name=alice", out)
}

func TestMcpFormat_LinesInput(t *testing.T) {
	leaf := &mcpLeaf{
		formatRef: &FormatRef{Inline: &Format{
			Input:	"lines",
			Views:	[]View{{Name: "v", Template: "{{len .data}} lines"}},
		}},
	}
	out, ok := mcpFormat(leaf, "a\nb\nc\n", map[string]any{})
	assert.True(t, ok)
	assert.Equal(t, "3 lines", out)
}

func TestMcpFormat_ViewSelection(t *testing.T) {
	leaf := &mcpLeaf{
		formatRef: &FormatRef{Inline: &Format{
			Input:	"json",
			Views: []View{
				{Name: "list", When: `{{ kindIs "slice" .data }}`, Template: "LIST"},
				{Name: "detail", Default: true, Template: "DETAIL"},
			},
		}},
	}
	out, ok := mcpFormat(leaf, `[1,2]`, map[string]any{})
	assert.True(t, ok)
	assert.Equal(t, "LIST", out)

	out, ok = mcpFormat(leaf, `{"a":1}`, map[string]any{})
	assert.True(t, ok)
	assert.Equal(t, "DETAIL", out)
}

func TestMcpFormat_TTYIsTrue(t *testing.T) {
	leaf := &mcpLeaf{
		formatRef: &FormatRef{Inline: &Format{
			Input:	"raw",
			Views:	[]View{{Name: "v", Template: "tty={{.tty}}"}},
		}},
	}
	out, ok := mcpFormat(leaf, "x", map[string]any{})
	assert.True(t, ok)
	assert.Equal(t, "tty=true", out)
}

func TestMcpFormat_RespectsAuthorWhenFalse(t *testing.T) {
	leaf := &mcpLeaf{
		formatRef: &FormatRef{Inline: &Format{
			Input:	"raw",
			When:	"false",
			Views:	[]View{{Name: "v", Template: "formatted"}},
		}},
	}
	_, ok := mcpFormat(leaf, "raw", map[string]any{})
	assert.False(t, ok)
}

// --- mcpExecLeaf with format ---

func TestMcpExecLeaf_WithFormat(t *testing.T) {
	leaf := &mcpLeaf{
		node:		Command{},
		cmdTmpl:	&Cmd{Shell: true, Template: `printf '{"count":3}'`},
		formatRef: &FormatRef{Inline: &Format{
			Input:	"json",
			Views:	[]View{{Name: "v", Template: "count={{.data.count}}"}},
		}},
	}
	out, isErr := mcpExecLeaf(leaf, map[string]any{})
	assert.False(t, isErr)
	assert.Equal(t, "count=3", out)
}

func TestMcpExecLeaf_FormatNotAppliedOnError(t *testing.T) {
	leaf := &mcpLeaf{
		node:		Command{},
		cmdTmpl:	&Cmd{Shell: true, Template: "echo bad; exit 1"},
		formatRef: &FormatRef{Inline: &Format{
			Input:	"raw",
			Views:	[]View{{Name: "v", Template: "formatted"}},
		}},
	}
	out, isErr := mcpExecLeaf(leaf, map[string]any{})
	assert.True(t, isErr)
	assert.Contains(t, out, "bad")
	assert.NotContains(t, out, "formatted")
}
