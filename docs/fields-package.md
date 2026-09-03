# The `fields` package

`github.com/wow-look-at-my/api-cli/fields` is the `<fields>` auto-formatter,
importable without the CLI. A program that already has decoded JSON hands it a
declaration and gets back a table, a `Label: value` list, lines, raw text, JSON,
Markdown, CSV, or a timeline. Nothing in it parses XML, reads a flag, or touches
a terminal, so a caller keeps its own configuration language.

```go
import "github.com/wow-look-at-my/api-cli/fields"

f := &fields.Fields{Over: "items", List: []fields.Field{
	{Name: "id", Path: "id"},
	{Name: "name", Path: "name"},
}}
out, err := fields.Render(nil, f, body, map[string]any{"data": body}, "", 0)
```

## The exported surface

`types.go` is the whole API, and every entry there is a thin wrapper over the
unexported implementation, so the package's internals stay free to move.

| Name | Role |
|---|---|
| `Fields`, `Field` | The declaration. `Over` selects records; a `Field` reads `Path`, or computes `Expr`. |
| `Render` | Represent a declaration as a sink. An empty sink follows the data's shape. |
| `Renderer` | The template evaluator the caller injects (see below). |
| `KnownSink`, `Sinks` | The representation names `Render` accepts. |
| `Lookup`, `LookupValue` | Resolve a dotted path through decoded JSON, maps by key and lists by index. |
| `MarshalJSON`, `SortedKeys`, `JSONTypeName` | The JSON helpers the sinks use, exported because a caller formatting alongside them wants the same output. |
| `DisplayWidth`, `StripANSI`, `AlignColumns`, `PadRight`, `PadLeft` | The width-aware aligner, usable on its own for any terminal table. |

## The renderer is injected, not built

A `Field`'s `Expr` and a `Fields`' `Footer` are Go templates, and which helper
functions they see is the caller's decision: api-cli's templates call
`shellquote`, `spread` and `tabwriter`, which mean nothing to another program.
So `Render` takes a `Renderer` rather than constructing one.

Passing `nil` is legal for a declaration that carries neither `Expr` nor
`Footer`; a declaration that does carry one gets an error naming the field, not
a nil dereference.

api-cli's own binding lives in `render.go`:

```go
var cliRenderer fields.Renderer = func(tmpl string, data map[string]any) (string, error) {
	return renderString(tmpl, data)
}
```

## What stayed in the CLI

`config.go` keeps `Fields` and `Field` as type aliases, so the XML loader, the
MCP path and the format dispatcher read as they always did. `render.go` keeps
one-line aliases for the aligner (`padRight`, `displayWidth`, ...) because they
are also template helpers registered by name in `cliFuncs`.

The CLI's `renderFields` is now a wrapper that supplies `cliRenderer`. Call
sites in `format.go` and `mcp_exec.go` are unchanged.
