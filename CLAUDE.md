# api-cli — repo orientation for Claude

This is a single-binary Go CLI built on **cobra**. It's a *declarative alias
system*: the user supplies an **XML** config (`./api.xml` by default, or
`--config <path>`), and at runtime the binary builds a cobra command tree from
that config. Each leaf either runs a command (shell or argv) or performs a
first-class HTTP **request**, then renders the result — optionally through the
**fields** auto-formatter.

It is a *hybrid* tool: HTTP requests are first-class (`<run><request>`, no
curl/jq subprocess), but the general shell/argv execution engine is fully
retained, so non-HTTP aliases (git, tar, ...) still work.

The user-facing semantics are documented in `README.md`. This file is a fast
orientation for code changes.

## Module / dependencies

- Module: `github.com/wow-look-at-my/api-cli`, Go 1.25.0.
- CLI parsing: `github.com/spf13/cobra`.
- Config parsing: **XML** via the stdlib `encoding/xml` tokenizer (`xmldom.go`).
  No third-party config parser. (The Go decoder only supports XML 1.0, so the
  leading `<?xml ... ?>` declaration is stripped before decoding — see
  `stripXMLDecl`.)
- Templating: Go stdlib `text/template` + `github.com/Masterminds/sprig/v3`.
- jq (response shaping): `github.com/itchyny/gojq` (pure Go, embedded — no jq
  binary needed).
- TTY / terminal width: `golang.org/x/term`. East Asian Wide width:
  `golang.org/x/text/width`.
- MCP server: `github.com/modelcontextprotocol/go-sdk`.
- Test assertions: `github.com/stretchr/testify`.
- XML validation (CI only): `wow-look-at-my/xml-validator` — well-formedness,
  **XML 1.1** (shipped files declare `version="1.1"`). Not used with `--schema`:
  it stack-overflows on the recursive `<command>` grammar.

Do not add new third-party deps without a clear reason.

## File map

| File                            | Role                                                        |
|---------------------------------|-------------------------------------------------------------|
| `main.go`                       | Entrypoint, root cobra command, persistent flags, config loading. `preparseGlobalFlags` extracts `--config` / `--mcp` / `--cors` before the cobra tree is built. Config discovery: `./api.xml`. |
| `config.go`                     | Schema structs (`Config`, `Command`, `Step`, `Arg`, `Flag`, `Cmd`, `Request`, `Param`, `Header`, `Response`, `Fields`, `Field`, `Format`, `View`, `FormatRef`); `Load` (bytes → `parseConfigXML` → `validate`); `validate`/`validateCommand`/`validateRequest`. |
| `xmldom.go`                     | XML tokenizer → order-preserving DOM (`xnode`): preserves mixed content, CDATA, attribute order. `stripXMLDecl`, `checkAttrs` (rejects unknown attributes). |
| `xmlcompile.go`                 | Placeholder compiler: `<value>`/`<if>`/`<else>`/`<for>` (+ surrounding text) → Go `text/template` source. `cleanText`/`dedentTabs` handle structural-tab whitespace. |
| `xmlsource.go`                  | `parseConfigXML` + config builders (`buildConfig`, `buildCommandNode`/`addCommandChild`, `buildRun`, `buildRequest`, `buildFields`, `buildEntry`, ...). `<entry>` is converted to a `json.RawMessage`. |
| `build.go`                      | Builds the `cobra.Command` tree. Threads inheritance for run (`*Cmd`/`*Request`), `cwd`/`stdin`/`confirm`/`format`. `runLeaf`, `passthroughParse`, `renderVars` (fixpoint — vars may reference other vars). |
| `exec.go`                       | Shell/argv execution: `doExec` (streaming), `captureExec` (steps), `captureExecCapped` (format path, 32 MiB cap), `parseResult`, `cappedTee`. |
| `request.go`                    | First-class HTTP: `runRequest` (net/http) builds URL/query/headers/body from templates; `applyJQ` (embedded gojq) for `<response jq=>`. `httpClient` is a package var (tests swap it for httptest). |
| `fields.go`                     | The `<fields>` auto-formatter: `renderFields` represents one declaration as table / list / lines / raw / json / markdown / csv, with `show_in` gating, `@key`/`@value` map walking, and priority-based column dropping. Reuses `align.go`. |
| `format.go`                     | Execution + presentation dispatch: `execLeaf` picks command-vs-request execution and fields-vs-legacy-format-vs-raw output. `captureRun`, `streamRequest`, `runFieldsFormatted`, `runFormatted`, `resolveFormat`, `selectView`. |
| `render.go`                     | `renderString`, `renderEntry`, `lookupPath`, `funcMap` (sprig + custom helpers incl. `truthy`, `querystring`, `urlpath`, `spread`, ...). |
| `align.go`                      | Width-aware aligner: `displayWidth`, `stripANSI`, `alignColumns`, `padRight`/`padLeft`. |
| `mcp.go` / `mcp_exec.go`        | MCP server: one tool per leaf. Threads run (`*Cmd`/`*Request`) + format inheritance; `mcpExecLeaf` runs the leaf and applies `<fields>` (like `--format=always`: `.tty` true, width 80) or a legacy format. |
| `cors.go` / `debug.go` / `docs.go` | CORS middleware for MCP HTTP/SSE; verbose/debug logging; the `docs` subcommand (embeds `README.md`, `api.schema.xsd`, `api.example.xml`). |
| `api.schema.xsd`                | XSD reference for the XML grammar (editor aid + `docs schema`). NOT enforced at runtime; the loader is authoritative. |
| `api.example.xml`              | Reference config (jsonplaceholder); loaded by `TestExampleConfigsLoad`. |
| `samples/github/github.xml`     | Read-only GitHub REST API wrapper in XML: first-class requests, jq noise-trimming, fields views. Used by the Docker image and CI demo; loaded by `TestGithubSampleLoads`. |
| `samples/github/Dockerfile.github` | Alpine image: ships `api-cli` + `github.xml`; ENTRYPOINT runs `--mcp`. No curl/jq (requests + gojq built in). |
| `*_test.go`                     | Unit + integration tests. `integration_test.go` has `execCmd`/`execCmdFull`; `request_test.go`/`request_integration_test.go` use httptest via `swapHTTPClient`. |

## The XML config model

`<config name="..."><command>...</command></config>`. Element content may
interleave text with **placeholders** that compile to Go templates:

- `<value name="var.x"/>` → `{{ .var.x }}`; `default=`/`as=` add `| default
  "..."` / a wrapping func; `expr="..."` is a verbatim template.
- `<if test="path" [eq="lit"]>...<else/>...</if>` → `{{ if truthy .path }}...`
  (or `{{ if eq (printf "%v" .path) "lit" }}...`).
- `<for each="path" [as="x"]>...</for>` → `{{ range ... }}...{{ end }}`.

`<run>` is the executable (inherited): a `<request>`, an `<argv>` list, or shell
text. `<entry>` (path/query/arbitrary) becomes `.entry`. `<fields>` declares
output shape; `<vars>/<var>` define `.var` (fixpoint resolution).

## Key design rules

1. **Inheritance.** `<run>` (command *or* request), `cwd`, `stdin`, `confirm`,
   `format` inherit down the tree; closest non-empty ancestor wins. A node's
   `<run>` of either kind clears the inherited run of the other kind. Threaded
   in `buildCommand` (`build.go`) and `collectMCPLeaves` (`mcp.go`) — new
   inheritable fields need both paths.
2. **Placeholders compile to templates.** The node language is sugar over
   `text/template`; everything funnels through `renderString`.
3. **Execution is command OR request.** `execLeaf` streams raw (`doExec` /
   `streamRequest`) unless a formatter applies. Steps are command-only.
4. **Formatting precedence.** `<fields>` (always, unless the user opts out) >
   legacy `<format>` (author `when` AND user verdict) > raw. `--no-format` /
   `--format=raw` / `NO_FORMAT` veto. `--as=<sink>` forces a fields
   representation.
5. **Fields scoping.** A `<field>` body is a record-relative path; `@key`/
   `@value` are the entry when `over=` walks a map; `expr=` sees the record
   promoted to the top level plus the whole context via `$` (`$.var`, `$.data`).
6. **Templates use `missingkey=zero`.** Don't change this default.
7. **Test redirection.** `execStdin/Stdout/Stderr` and `httpClient` are
   package-level vars; tests swap them.

## Adding a new field to the config

1. Add it to the relevant struct in `config.go`.
2. Parse it in `xmlsource.go` (the relevant `build*` function); reject unknown
   attrs via `checkAttrs`.
3. If it inherits, thread it in `buildCommand` (`build.go`) and
   `collectMCPLeaves` (`mcp.go`).
4. If it needs validation, extend `validate` / `validateCommand`.
5. Document it in `api.schema.xsd` and `README.md`; exercise it in
   `api.example.xml` if integration tests rely on it.
6. Add tests: unit (parse + validate in `xmlsource_test.go`) + integration.

## Common gotchas

- **Line budget.** go-toolchain warns at 500 lines, **errors at 750**. Several
  files are near the warning; extract into a topical file rather than growing one
  past 750.
- **XML 1.1.** Shipped `*.xml`/`*.xsd` must declare `version="1.1"` (the CI
  `xml-validator` rejects 1.0 / missing declarations). The Go loader strips the
  declaration, so inline test snippets can omit it.
- **`spread` sentinel.** NUL/SOH markers delimit spread elements (`render.go` /
  `exec.go`).
- **Number normalization.** `parseResult` (`exec.go`) normalizes JSON numbers to
  `int64`/`float64`; `displayValue` (`fields.go`) renders them without a trailing
  `.0`. gojq output is re-marshaled then reparsed through the same path.
- **`when` vs `test`.** `when=` (step/view/format) is a full template predicate;
  `test=` (`<if>`) is a context path checked for truthiness.

## Tooling

- `go-toolchain` runs `go mod tidy`, vet, all tests with coverage, and the
  build. **Always `go-toolchain`, never bare `go ...`.** Coverage minimum 80%.
- CI: `.github/workflows/ci.yml` (go-toolchain test + demo, `validate-xml`,
  docker).

## Conventions

- Lowercase `lint`, `test`, etc. — go-toolchain handles it.
- Commit messages: clear "what + why" summary; don't lead with "Add" for a
  refactor.
- Branch naming for Claude sessions: `claude/<descriptor>-<short-id>`.
- Squash-merge: PRs squash to one commit — don't rebase/force-push to clean up.
