# The `fields` package

`github.com/wow-look-at-my/api-cli/fields` is the `<fields>` auto-formatter, importable without the CLI. A program gives it decoded JSON and a declaration. It gets back a table, a `Label: value` list, lines, raw text, JSON, Markdown, CSV, or a timeline. The package parses no XML, reads no flag, and touches no terminal, so a caller keeps its own configuration language.

```go
import "github.com/wow-look-at-my/api-cli/fields"

f := &fields.Fields{Over: "items", List: []fields.Field{
	{Name: "id", Path: "id"},
	{Name: "name", Path: "name"},
}}
out, err := fields.Render(nil, f, body, map[string]any{"data": body}, "", 0)
```

## The exported surface

`types.go` is the whole API. Every entry there wraps the unexported implementation, which leaves the internals free to move.

| Name | Role |
|---|---|
| `Fields`, `Field` | The declaration. `Over` selects records, and a `Field` reads `Path` or computes `Expr`. |
| `Render` | Represent a declaration as a sink. An empty sink follows the data's shape. |
| `Renderer` | The template evaluator the caller injects (see below). |
| `KnownSink`, `Sinks` | The representation names `Render` accepts. |
| `Lookup`, `LookupValue` | Resolve a dotted path through decoded JSON, maps by key and lists by index. |
| `MarshalJSON`, `SortedKeys`, `JSONTypeName` | The JSON helpers the sinks use, exported because a caller formatting alongside them wants the same output. |
| `DisplayWidth`, `StripANSI`, `AlignColumns`, `PadRight`, `PadLeft` | The width-aware aligner, usable on its own for any terminal table. |

## A column sink gets one line per cell

`table` and `markdown` fold every cell onto one line. A run of whitespace that holds a newline or a tab becomes one space. A value that holds neither is untouched, so ordinary spacing inside a cell survives.

The reason is structural. A row is a line. A column is a position on that line. A cell that keeps its newlines puts every later column at the left margin, and a tab opens a column that the declaration never named. Both defeat a reader who scans down a column.

The `list` sink keeps the whole value instead, and indents the later lines under the first, so a paragraph reads as one value. `raw`, `json` and `csv` carry the value exactly, and `csv` quotes it per RFC 4180.

To show less than the whole value, use `firstline="true"` for the first line, or `truncate="N"` for a prefix.

## The renderer is injected, not built

A `Field`'s `Expr` and a `Fields`' `Footer` are Go templates. The caller decides which helper functions they see. api-cli's own templates call `shellquote`, `spread` and `tabwriter`, which mean nothing to another program. So `Render` takes a `Renderer` instead of building one.

A declaration that carries neither `Expr` nor `Footer` may pass `nil`. One that carries either gets an error naming the field, not a nil dereference.

api-cli's own binding lives in `render.go`:

```go
var cliRenderer fields.Renderer = func(tmpl string, data map[string]any) (string, error) {
	return renderString(tmpl, data)
}
```

## What stayed in the CLI

`config.go` keeps `Fields` and `Field` as type aliases, so the XML loader, the MCP path and the format dispatcher read as they always did. `render.go` keeps one-line aliases for the aligner, because `cliFuncs` registers those same names as template helpers.

The CLI's `renderFields` wraps `fields.Render` and supplies `cliRenderer`. The call sites in `format.go` and `mcp_exec.go` are unchanged.
