# api-cli — repo orientation for Claude

This is a single-binary Go CLI built on **cobra**. It is a *declarative alias system*. The user supplies an **XML** config (`./api.xml` by default, or `--config <path>`). At run time the binary builds a cobra command tree from that config. Each leaf runs a command (shell or argv) or makes a first-class HTTP **request**. It then renders the result, optionally through the **fields** auto-formatter.

It is a *hybrid* tool. HTTP requests are first-class (`<run><request>`, no curl or jq subprocess). The general shell and argv execution engine stays, so non-HTTP aliases (git, tar, ...) work too.

`README.md` documents the user-facing semantics. This file is a fast orientation for code changes.

## Module / dependencies

- Module: `github.com/wow-look-at-my/api-cli`, Go 1.26.
- CLI parsing: `github.com/spf13/cobra`.
- The spec language: `github.com/wow-look-at-my/api-dsl` — the XML DOM, the `<value>`/`<if>`/`<for>` placeholder compiler, and the renderer. This repo invented that language and api-mirror now shares it, so it lives there and this repo consumes it. `dsl.go` is the local boundary. See the file map.
- Config parsing: **XML** through api-dsl's `ParseDOM`, on top of the stdlib `encoding/xml` tokenizer. No third-party config parser. (The Go decoder reads XML 1.0 only, so api-dsl removes the leading `<?xml ... ?>` declaration before it decodes.)
- Templating: Go stdlib `text/template` plus `github.com/Masterminds/sprig/v3`. Both arrive through api-dsl's `Renderer`.
- jq (response shaping): `github.com/itchyny/gojq`, pure Go and embedded. No jq binary is necessary.
- TTY and terminal width: `golang.org/x/term`. East Asian Wide width: `golang.org/x/text/width`.
- MCP server: `github.com/modelcontextprotocol/go-sdk`.
- Timeline sink: `github.com/wow-look-at-my/ascii-timeline/timeline`, a pure-stdlib renderer. It powers `--as=timeline`.
- Screens: `github.com/wow-look-at-my/tml`, the terminal markup language, plus `charm.land/bubbletea/v2` for the watch program. Both arrive with `<tml>`. See docs and rules 17 and 18.
- Test assertions: `github.com/stretchr/testify`.
- XML validation (CI only): `wow-look-at-my/xml-validator` checks well-formedness and **XML 1.1**. Shipped files declare `version="1.1"`. CI does not give it `--schema`, because it overflows the stack on the recursive `<command>` grammar.

Do not add a new third-party dependency without a clear cause.

## File map

| File                            | Role                                                        |
|---------------------------------|-------------------------------------------------------------|
| `main.go`                       | Entrypoint, root cobra command, persistent flags, config loading. `preparseGlobalFlags` extracts `--config` / `--mcp` / `--cors` before the cobra tree is built. Config discovery: `./api.xml`. |
| `config.go`                     | Schema structs (`Config`, `Command`, `Step`, `Arg`, `Flag`, `Cmd`, `Request`, `Param`, `Header`, `Response`, `Fields`, `Field`, `Format`, `View`, `FormatRef`); `Load` (bytes → `parseConfigXML` → `validate`); `validate`/`validateCommand`/`validateRequest`. |
| `dsl.go`                        | The api-dsl boundary: `xnode` = `apidsl.Node`, plus `parseDOM`/`checkAttrs`/`compileContent`/`compileTextElem`/`textOf`/`isPlaceholder`/`envMap`/`lookupPath`/`mergeVars`/`isTruthy`/`templateTruthy`. An element's name is the method `Name()`, never a field. |
| `xmlsource.go`                  | `parseConfigXML` + config builders (`buildConfig`, `buildCommandNode`/`addCommandChild`, `buildRun`, `buildFields`, `buildEntry`, ...). `<entry>` is converted to a `json.RawMessage`. |
| `xmlrequest.go`                 | The `<request>` half of the source: `buildRequest`, `buildQuery`/`buildParam`, `buildHeader`, and `parseAllowStatus` for `allow-status=`. |
| `runnable.go`                   | A parent that also runs: `Command.executes`, `validateRunnable` (a pattern may not match a subcommand name), and the cobra arg validators (`matchArgPatterns`, `chainArgs`). |
| `build.go`                      | Builds the `cobra.Command` tree. Threads inheritance for run (`*Cmd`/`*Request`), `cwd`/`stdin`/`confirm`/`format`. `runLeaf`, `resolveContext` (the leaf's data context: two var passes around the flag gather), `renderVars` (fixpoint — vars may reference other vars). |
| `flags.go`                      | Declared `<arg>`/`<flag>` on both sides of a run: `registerFlag`/`registerConflicts` on the cobra command, `gatherArgs`/`gatherFlags`/`passthroughParse` back out into `.arg`/`.flag`. |
| `exec.go`                       | Shell/argv execution: `doExec` (streaming), `captureExec` (steps), `captureExecCapped` (format path, 32 MiB cap), `parseResult`, `cappedTee`. `resolveArgv` renders a `*Cmd` to its final argv, for the caller that cannot execute where it renders (a download's transport). |
| `request.go`                    | First-class HTTP: `prepareRequest` renders URL/query/headers/body into a `preparedRequest`; `runRequest` sends it via `doHTTP` (net/http) or a transport, then `applyJQ` (embedded gojq) for `<response jq=>`, whose program `jqProgram` resolves (template / context path / literal). `httpClient` is a package var (tests swap it for httptest). |
| `transport.go`                  | `<transports>`: parsing (`buildTransports`), the package-level registry (`installTransports`, published by `newRoot`/`buildMCPServer`), selection (`resolveTransportNamed`: `transport=` > registry default > built-in; no runtime override by design), `runViaTransport` (requests), and `prepareDownloadTransport` (downloads: renders the program's argv at plan time). `preparedRequest.context` exposes `.request` to the program's argv. |
| `steps.go`                      | `runSteps`: the one step loop, shared by the CLI and MCP paths (they take a `stepCapture` and an errOut). A step runs its own command/request or inherits the leaf's. `runStepOver` repeats a step per element of a `over=` list and pairs each element with its response. |
| `fields/`                       | The `<fields>` auto-formatter, as an importable package. `fields.go` represents one declaration as table / list / lines / raw / json / markdown / csv / timeline, with `show_in` gating, `@key`/`@value` map walking, and priority-based column dropping; `records.go` resolves paths and selects records (`lookupData` walks maps by key and lists by index, `overSource` reads the body then the context and fails loud); `timeline.go` is the `--as=timeline` sink over `ascii-timeline`; `align.go` is the width-aware aligner; `json.go` holds the shared JSON helpers. `types.go` is the exported surface — see docs/fields-package.md. |
| `download.go`                   | The `<download>` hand-off: `Downloads`/`Download` structs, `buildDownloads`/`buildDownload`, validation, settings resolution (config + flags), and `planDownloads` (renders declarations into `downloadSpec`s, expanding `over=`). `renderHash` normalizes an optional `<hash>` and rejects one that is not a well-formed digest. |
| `downloader.go`                 | The shared queue: a process-wide worker pool (`sharedQueue`) plus a per-invocation `downloadBatch`. `fetch` (net/http) and `fetchViaTransport` (a program's stdout) both write through `partFile` — `.part` sibling, byte count, digest, rename. Retries transient failures at a fixed cadence; names dir destinations from `Content-Disposition` or the URL. Progress fields are atomic (`tallyDownloads`, `progressOf`). |
| `join.go`                       | The `<join>` half of a download: the grammar (`Join`, `buildJoin`, `validateJoin`, `planJoin`) and the runtime `joiner`, which concatenates each group as its own last part lands. |
| `downloadrun.go`                | `downloadSession` ties a leaf to the queue: settings, TTY detection (`stdoutSize`), swapping the output channels to the TUI, drain, and the summary. `mcpRunDownloads` is the MCP path. |
| `tmlview.go`                    | The `<tml>` presentation: `TML`/`TMLProp`/`TMLField`, `buildTML`, validation, the `configDir` publication, the view cache (`loadTMLView`), the props conversion (`tmlProps`, `tmlScalar`), and `renderTMLFrame`. |
| `tmlrun.go`                     | `--watch` on a `<tml>` leaf: a Bubble Tea program built with `tml.NewProgram`, one leaf run per tick, on the alternate screen. |
| `watch.go`                      | `--watch`: `parseWatchInterval`, `runWatch` (terminal and signal setup), `watchLoop` (one frame per run), and `watchPainter` (in-place repaint, height clipping, append-only without a terminal). |
| `tui.go`                        | The download display: a bottom-pinned block of slots (`frame`), its column layout decided once per frame (`planProgressLayout`/`progressLine`), and in-place ANSI repainting. `paint` emits queued lines above the block and forgets them, so the terminal's own scrollback holds them. |
| `format.go`                     | Execution + presentation dispatch: `execLeaf` picks command-vs-request execution and fields-vs-legacy-format-vs-raw output. `captureRun`, `streamRequest`, `runFieldsFormatted`, `runFormatted`, `resolveFormat`, `selectView`. |
| `render.go`                     | `renderString` (over one shared `apidsl.NewRenderer(cliFuncs())`), `renderEntry`, and `cliFuncs` — the helpers this CLI adds to the shared set (`shellquote`, `spread`, `fileExists`, `tabwriter`, `padRight`, ...). `addQueryValue` builds a `<query from=>` value. It also holds the `fields` package bindings: `cliRenderer` (the CLI's template evaluator, injected into `fields.Render`) and the aliases the rest of the CLI calls the aligner by. |
| `mcp.go` / `mcp_exec.go`        | MCP server: one tool per leaf. Threads run (`*Cmd`/`*Request`) + format inheritance; `mcpExecLeaf` runs the leaf and applies `<fields>` (like `--format=always`: `.tty` true, width 80) or a legacy format. |
| `cors.go` / `debug.go` / `docs.go` | CORS middleware for MCP HTTP/SSE; verbose/debug logging; the `docs` subcommand (embeds `README.md`, `api.schema.xsd`, `api.example.xml`). |
| `api.schema.xsd`                | XSD reference for the XML grammar (editor aid + `docs schema`). NOT enforced at runtime; the loader is authoritative. |
| `api.example.xml`              | Reference config (jsonplaceholder); loaded by `TestExampleConfigsLoad`. Exercises the grammar end to end, including a non-default `curl` transport, a request-step chain (`posts by-user`), and a step-to-queue download hand-off (`archive`). |
| `samples/github/github.xml`     | Read-only GitHub REST API wrapper in XML: first-class requests, jq noise-trimming, fields views. Used by the CI demo; loaded by `TestGithubSampleLoads`. |
| `*_test.go`                     | Unit + integration tests. `integration_test.go` has `execCmd`/`execCmdFull`; `request_test.go`/`request_integration_test.go` use httptest via `swapHTTPClient`. |

## The XML config model

The root is `<config name="..."><command>...</command></config>`. Element content can mix text with **placeholders** that compile to Go templates.

- `<value name="var.x"/>` → `{{ .var.x }}`. `default=` adds `| default "..."` and `as=` wraps the value in a function. `expr="..."` is a verbatim template.
- `<if test="path" [eq="lit"]>...<else/>...</if>` → `{{ if truthy .path }}...`, or `{{ if eq (printf "%v" .path) "lit" }}...` with `eq=`.
- `<for each="path">...</for>` → `{{ range ... }}...{{ end }}`. `.` rebinds to each element. `each=` is the only attribute api-dsl accepts here.

`<run>` is the executable, and it inherits: a `<request>`, an `<argv>` list, or shell text. `<entry>` (path, query, or arbitrary keys) becomes `.entry`. `<fields>` declares the output shape. `<vars>` and `<var>` define `.var`, resolved to a fixpoint. Top-level `<transports>` names the programs that make requests in place of net/http. Top-level `<downloads>` configures the shared download queue that a leaf-level `<download>` feeds.

## Key design rules

1. **Inheritance.** `<run>` (a command *or* a request), `cwd`, `stdin`, `confirm` and `format` inherit down the tree. The closest non-empty ancestor wins. A node's `<run>` of one kind clears the inherited run of the other kind. `buildCommand` (`build.go`) and `collectMCPLeaves` (`mcp.go`) each thread it, so a new inheritable field needs both paths.
2. **Placeholders compile to templates.** The node language is sugar on top of `text/template`. Every render goes through `renderString`.
3. **Execution is a command OR a request.** `execLeaf` streams raw output through `doExec` or `streamRequest` unless a formatter applies. Steps take the same fork. A step that declares neither one inherits the leaf's effective run.
4. **A request travels over net/http or a `<transport>` program**, and `resolveTransport` chooses. Both take the same `preparedRequest`, and the response path (`<response jq=>`, `<fields>`) is identical. A transport is therefore never a second code path to keep in sync.
5. **A `<download>` leaf hands off in place of a run.** The declarations are the leaf's action, after its steps, so no command and no request executes for it. An inherited `<run>` stays where it is. One process-wide queue serves every hand-off. A `downloadBatch` scopes one invocation's items on that queue, which keeps a long-lived MCP server from mixing tool calls together.
6. **A download travels over net/http or a `<transport>`**, selected exactly as a request's is. The program gets the same `.request` context. Only the return path differs: its stdout streams into the file rather than into a buffered body, because a file need not fit in memory. Both paths then share `partFile` (a `.part` sibling, a byte count, a digest, and a rename).
7. **Formatting precedence.** `<fields>` wins always, unless the user opts out. A legacy `<format>` comes next, and needs the author `when` AND the user verdict. Raw output is last. `--no-format`, `--format=raw` and `NO_FORMAT` each veto. `--as=<sink>` forces a fields representation.
8. **Fields scoping.** A `<field>` body is a record-relative path. `@key` and `@value` are the entry when `over=` walks a map. `expr=` sees the record promoted to the top level, plus the whole context through `$` (`$.var`, `$.data`). A field path and `over=` both resolve through `lookupData`, so a numeric step indexes a list. `over=` reads the body first, then the whole context.
9. **A projection that finds nothing says so.** An `over=` that points at a missing path or at a scalar fails the run. A `<response jq=>` that points at a missing var fails it too. These paths once rendered one empty record over exit 0, which reads as an empty API rather than as a broken config.
10. **Templates use `missingkey=zero`.** Do not change this default. The context maps hold `any`, so a missing key renders `<no value>`, not "".
11. **A jq program is a template.** `jq=` renders against the leaf context. A bare dotted name is a context path to a string instead (`jqProgram`, `request.go`).
12. **Vars resolve twice per invocation** (`resolveContext`, `build.go`). The first pass is flag-blind, and it feeds a templated `<flag default=>`. The second pass runs over the finished flag map. `.var` downstream is that second pass, so it sees this run's flags. The CLI path and the MCP path both go through it.
13. **Test redirection.** `execStdin`, `execStdout`, `execStderr`, `httpClient` and `downloadClient` are package-level vars that tests swap. The download queue is process-wide, so a test that swaps its client also calls `resetSharedQueue`. See `swapDownloadClient`.
14. **A test that swaps one of those calls `t.Serial()` first.** The toolchain's testing package runs tests in parallel by default, and two tests cannot hold one global at once. `execCmdFull`, `chdir`, `swapHTTPClient`, `swapDownloadClient` and `captureExecStreams` each call it, so a test that goes through one of them is covered. A test that assigns a global itself needs the call of its own. A parent whose subtests run in parallel closes its server with `t.Cleanup`, never `defer`: a deferred close beats the subtests to it.
15. **A watch frame is a whole leaf run.** `--watch` loops `runLeafOnce` (`build.go`) and captures each run's stdout and stderr into one buffer, which `watchPainter` draws over the last frame. Nothing is cached between frames. `ttyOverride` (`format.go`) makes `stdoutTTY`/`stdoutSize` report the real terminal while the frame sits in that buffer, so the formatter still picks the terminal representation. `watchable` refuses a `<download>` leaf, and refuses a `confirm` leaf without `--yes`.
16. **Downloads use their own HTTP client.** `downloadClient` carries a connect deadline and a response-header deadline, and no overall timeout. `httpClient`'s 60s cap is an API deadline, and on a download it becomes a ceiling on file size.
17. **A `<tml>` leaf draws a screen, and a screen needs a terminal.** `execLeaf` renders one frame through `renderTMLFrame` when stdout is a terminal, or under `--format=always`. Piped, it falls through to the rest of the presentation chain, and `--as=<sink>` wins over it. A leaf declares `<tml>` or `<fields>`, never both. Every prop crosses as a string, because tml's `coerce` re-reads a string as the property's declared type. A `<prop over=>` builds a record list, and the record holds exactly the named fields: `bindProps` rejects a field the data template never declared.
18. **`--watch` on a `<tml>` leaf is a Bubble Tea program, not the repaint loop.** `tml.DriveGrace` panics a process that paints for 5s with nothing able to drive it, and only `tml.NewProgram` builds a program the library can reach. `runTMLProgram` (`tmlrun.go`) owns that loop and reuses `captureInto`. A tick is still one whole `runLeafOnce`. Interaction inside the component (focus, click, scroll) is not routed yet.
19. **A repeated step is one call per element** (`<step over=>`, `runStepOver`). The element is `.item` and its position `.index`, both restored afterwards, and the result is a list of `{item, result}` pairs. A failing element fails the step, because a board short one card reads as a shorter queue. A `<prop over=>` then walks that one list, and a `<field expr=>` sees the element's keys promoted plus `$` for the run.
20. **A one-shot `<tml>` frame is a page. A watch frame is a screen.** `execLeaf` lays a single frame out at `tmlPageHeight` and trims the blank rows, so a board is never cut off at the terminal's last row. Under `--watch`, `ttyOverride` is set and the real terminal height applies.
21. **A leaf carries a list of `<fields>` blocks** (`[]FieldsBlock`, each with an optional `when=`). `renderFieldsBlocks` (`format.go`) renders every block whose predicate holds, in order, and the CLI and MCP paths share it. No block matching prints the raw body, exactly as no `<fields>` does.
22. **Every declared arg is in `.arg`.** `zeroArg` (`flags.go`) fills an omitted optional arg with the zero value of its type, on the CLI side and the MCP side. A helper that takes a string, such as `urlpath`, therefore gets one instead of nil.
23. **`allow-status=` keeps an error status.** `preparedRequest.allows` (`request.go`) makes `doHTTP` return that body with code 0. A `<transport>` request that declares it fails, because a program reports an exit code rather than a status.
24. **A precondition runs before the steps**, so `validate` rejects one that reads `.result` (`config.go`). Move the check into a `<step when=>`.
25. **A step with `until=` polls** (`runStepAction`, `steps.go`). It repeats its own run until the predicate holds, and the predicate reads the last body promoted onto the run context (`.status`, plus `.body`). `interval=` never grows, `attempts=` caps the loop, and exhaustion fails the run with the last body. A non-zero exit ends the poll at once. `over=` and `until=` compose, so one invocation polls N jobs.
26. **`over=` walks any slice, and takes a template.** `asList` (`render.go`) normalizes a `[]string` from a variadic arg the same as a `[]any`. `overSource` renders an `over=` that carries `{{`, and reads the output as JSON, which is how a fan-out result reshapes without a shell. `collect` gathers one path out of every element of that result.
27. **A `<join>` makes one file out of a group of parts** (`join.go`). Membership is decided at plan time, so the `joiner` knows every group's size before the first transfer and needs no registration racing the queue. `downloadBatch.onDone` hands it each finished item, and the last member of a group starts that group's concatenation on its own goroutine. A group with a failed member is never written. `order=` sorts numerically, and `contiguous=` reports the whole numbers missing between the lowest and the highest.
28. **A `<download>` leaf gets a scratch directory** at `.run.tmpdir` (`openScratch`, `downloadrun.go`), removed when the run ends. Parts that a join consumes belong there. The CLI path and the MCP path both open one.
29. **A node runs when `executes()` says so** (`runnable.go`): a leaf, or a parent with `runnable="true"`. That predicate gates `RunE` (`build.go`), the MCP tool list (`mcp.go`) and every "leaf-only" validation. `validateRunnable` keeps dispatch decidable. Each arg of a runnable node needs a `pattern=`. A pattern that matches a subcommand name, its own or one cobra owns, is a load error.

## Adding a new field to the config

1. Add it to the relevant struct in `config.go`.
2. Parse it in the relevant `build*` function in `xmlsource.go`. Reject an unknown attribute with `checkAttrs`.
3. If it inherits, thread it in `buildCommand` (`build.go`) and in `collectMCPLeaves` (`mcp.go`).
4. If it needs validation, extend `validate` or `validateCommand`.
5. Document it in `api.schema.xsd` and in `README.md`. Exercise it in `api.example.xml` when an integration test needs it.
6. A grammar limit goes in the README's "Limits and workarounds" table, with the shape to write instead. A limit the loader can catch also gets a `validate` error that names that alternative.
7. Add tests: a unit test that parses and validates it in `xmlsource_test.go`, plus an integration test.

## Common gotchas

- **Line budget.** go-toolchain warns at 500 lines and **fails at 750**. Several files sit near the warning. Extract a topical file rather than grow one past 750.
- **XML 1.1.** A shipped `*.xml` or `*.xsd` must declare `version="1.1"`. The CI `xml-validator` rejects XML 1.0 and a missing declaration. api-dsl removes the declaration before it decodes. An inline test snippet can therefore leave it out.
- **The language is not ours to edit here.** A change to `<value>`/`<if>`/`<for>`, to the DOM, or to a shared template helper belongs in api-dsl. A helper that is only meaningful to a CLI belongs in `cliFuncs` (`render.go`).
- **Sets are `github.com/wow-look-at-my/go-containers/set`.** Use `set.Of(...)` for a fixed membership list, and `set.New[T]()` for one the code builds up. The code sometimes asks a `map[...]bool` or a `[]string` for membership only. That is a vet error, not a style note. go-toolchain rewrites the slice form in place. This dependency puts the module on go 1.26.
- **`spread` sentinel.** NUL and SOH markers delimit the spread elements. See `render.go` and `exec.go`.
- **Number normalization.** `parseResult` (`exec.go`) normalizes a JSON number to `int64` or `float64`. `displayValue` (`fields.go`) renders it without a trailing `.0`. gojq output goes back through the same path: the code marshals it, then parses it again.
- **`when` against `test`.** `when=`, on a step, a view or a format, is a full template predicate. `test=`, on an `<if>`, is a context path that the code checks for truthiness.

## Tooling

- `go-toolchain` runs `go mod tidy`, vet, all tests with coverage, and the build. **Always run `go-toolchain`, never a bare `go ...`.** Coverage minimum 80%.
- CI is `.github/workflows/ci.yml`. The `test` job runs `ste-lint` over the markdown, then go-toolchain, then the demo. A second job, `validate-xml`, checks the XML. The build names no `os` and no `arch`, so it produces one fat APE that autoreleases to buildhost.
- `ste-lint` (`wow-look-at-my/actions@ste-lint#latest`) checks every `*.md` file against the mechanical subset of ASD-STE100. One paragraph is one line, and a sentence caps at 25 words. A contraction, a semicolon, a comma splice or the word "would" fails the job.

## Conventions

- Lowercase `lint`, `test` and the rest. go-toolchain handles them.
- Write a commit message as a clear "what and why" summary. Do not open with "Add" for a refactor.
- Branch naming for Claude sessions: `claude/<descriptor>-<short-id>`.
- A pull request squashes to one commit. Do not rebase and do not force-push to tidy the history.
