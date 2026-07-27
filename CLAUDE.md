# api-cli — repo orientation for Claude

This is a single-binary Go CLI built on **cobra**. It's a *declarative alias
system*: the user supplies a JSON config (`./api.json` by default, or
`--config <path>`), and at runtime the binary builds a cobra command tree
from that config. Each leaf renders a Go `text/template` against a data
context and executes the result. Common use case: wrapping REST APIs with
`curl`, but the system is general-purpose.

The user-facing semantics are documented exhaustively in `README.md`. This
file is a fast orientation for code changes.

## Module / dependencies

- Module: `github.com/wow-look-at-my/api-cli`, Go 1.25.0.
- CLI parsing: `github.com/spf13/cobra` v1.8.x.
- Templating: Go stdlib `text/template` + `github.com/Masterminds/sprig/v3`.
- JSON Schema validation (test only): `github.com/santhosh-tekuri/jsonschema/v5`.
- TTY / terminal width: `golang.org/x/term`.
- East Asian Wide width tables: `golang.org/x/text/width`.
- MCP server: `github.com/modelcontextprotocol/go-sdk`.
- Test assertions: `github.com/wow-look-at-my/testify` (a fork pin).

Do not add new third-party deps without a clear reason. Stdlib + sprig
covers most needs.

## File map

| File                            | Role                                                        |
|---------------------------------|-------------------------------------------------------------|
| `main.go`                       | Entrypoint, root cobra command, persistent flags, config loading. `preparseGlobalFlags` extracts `--config` / `--mcp` / `--cors` from argv via a tolerant pflag parse before the cobra tree is built. |
| `config.go`                     | Schema structs (`Config`, `Command`, `Step`, `Arg`, `Flag`, `Cmd`, `Format`, `View`, `FormatRef`); `Load`; `validate`. |
| `build.go`                      | Walks `Config.Commands` building `cobra.Command` tree. Threads inheritance for `command`/`cwd`/`stdin`/`confirm`/`format`. Implements `runLeaf` and `passthroughParse`. |
| `exec.go`                       | `doExec` (streaming), `captureExec` (steps), `captureExecCapped` (format path with 32 MiB cap), `parseResult`, `cappedTee`. |
| `render.go`                     | `renderString`, `renderEntry`, `funcMap` with sprig + custom helpers (`querystring`, `shellquote`, `urlpath`, `spread`, `fileExists`, `dirExists`, `tabwriter`, `padRight`, `padLeft`, `displayWidth`, `stripANSI`, `filterSuffix`, `filterPrefix`). |
| `format.go`                     | Format-system runtime: `resolveFormat`, `userVerdictFromFlags`, `stdoutTTY`, `formatContext`, `renderPredicate` (cached), `parseInput`, `selectView`, `execLeaf`, `runFormatted`. |
| `align.go`                      | Width-aware aligner: `displayWidth`, `stripANSI`, `alignColumns`, `padRight`, `padLeft`. ANSI-stripping state machine + East Asian Width lookup. |
| `mcp.go`                        | MCP (Model Context Protocol) server entrypoint: `runMCP` (stdio / http / sse transports), `buildMCPServer`. `mcpLeaf` carries inherited format context. |
| `mcp_exec.go`                   | MCP tool execution: `mcpExecLeaf` runs a leaf and applies formatting via `mcpFormat`. Behaves like `--format=always`: `.tty` is `true`, `.width` is 80. |
| `cors.go`                       | CORS middleware for the MCP HTTP/SSE server. `CorsLevel` (disabled/permissive/strict/enabled), `parseCorsLevel`, `withCORS`, origin matchers. |
| `debug.go`                      | Debug/verbose logging infrastructure: `logVerbose`, `logDebug`, `logDebugBlock`, helpers. Package-level `verboseMode`/`debugMode` vars set from `--verbose`/`--debug` flags in `runLeaf`. |
| `docs.go`                       | Built-in `docs` subcommand: embeds README, schema, and example via `go:embed`. Schema key lookup via `schemaLookup`. |
| `api.schema.json`               | Authoritative JSON Schema for configs. Updated alongside `config.go`. |
| `api.example.json`              | Reference config; covered by `TestExampleConfigMatchesSchema` and integration tests. |
| `github.example.json`           | Real-world example: read-only GitHub REST API wrapper with table/detail views and `jq`-based response trimming. |
| `*_test.go`                     | Unit + integration tests (testify). `integration_test.go` has the `execCmd` / `execCmdFull` helpers used by most tests. |
| `workers/`                      | Cloudflare Workers TypeScript port. Parses same JSON configs; serves leaves as HTTP endpoints via `fetch()`. See `workers/README.md`. |

## Key design rules

1. **Inheritance pattern.** `command`, `cwd`, `stdin`, `confirm`, `format`
   all inherit down the tree: closest non-empty (or `Defined()`) ancestor
   wins; a leaf overrides its ancestor for that subtree. Threading happens
   in `buildCommand` (`build.go`) via `inherited*` parameters and in
   `collectMCPLeaves` (`mcp.go`) via the same pattern; new inheritable
   fields must be threaded in both paths.
2. **Streaming fast path stays intact.** `doExec` streams the child's
   stdout straight to `execStdout`. The format path captures via
   `captureExecCapped` (32 MiB buffer; on overflow, prefix flushes and
   the rest streams through). When no format applies *or* the user has
   opted out, `runLeaf` calls `doExec` and never touches the capture
   path.
3. **Format AND-semantics.** Formatting applies iff the author's
   `format.when` predicate is truthy AND the user verdict is yes.
   `--no-format` / `NO_FORMAT=1` / `--format=raw` veto from the user side.
   `--format=always` lies about `.tty` in predicate context but cannot
   override an explicit `when: "false"`.
4. **Steps capture, leaves stream (or capture if formatted).** Steps
   capture via `captureExec` (no cap — step output is expected to be
   small structured data feeding `.result.<name>`). A step with a `when`
   predicate that evaluates falsy is skipped entirely: no command runs,
   `.result.<name>` is not set, and the execution count is unaffected.
   The leaf's own command streams unless a format applies, in which case
   it goes through `captureExecCapped`.
5. **Templates use missingkey=zero.** Missing map keys render as nil (or
   `<no value>` for `map[string]any`). For strict mode, use sprig's
   `required`. Don't change this default.
6. **Test redirection.** `execStdin`, `execStdout`, `execStderr` are
   package-level `io.Reader`/`Writer` vars; tests swap them for
   `bytes.Buffer`. The TTY check (`stdoutTTY`) type-asserts to
   `*os.File` — non-files are treated as non-TTYs, which is exactly
   what the test path wants.
7. **Passthrough mode.** When `Command.Passthrough` is true, the cobra
   command accepts arbitrary args (everything after `--` in the wrapper
   script). `passthroughParse` in `build.go` extracts declared flags
   from the raw args; everything else lands in `.rest` (a `[]string`).
   The template data context gets `.rest` alongside the usual `.flag`,
   `.env`, `.var`, `.result`, `.entry` namespaces.

## Adding a new field to the config

1. Add it to the relevant struct in `config.go`.
2. If it inherits, add an `inherited<Name>` parameter to `buildCommand`
   (mirror `inheritedCmd` / `inheritedCwd` / etc.). Update the call sites
   in `main.go` and the recursive `buildCommand` self-call.
3. If it needs validation, extend `validate` / `validateCommand`.
4. Add it to `api.schema.json` (top-level or under `definitions/commandNode`).
5. Add a row to the relevant table in `README.md`.
6. Update `api.example.json` if the example should exercise it.
7. Add tests: unit (struct unmarshal, validation) + integration (end-to-end
   via `execCmd` / `execCmdFull`).

## Adding a new template helper

1. Implement the function in `render.go` (or a topical file like `align.go`).
2. Register it in `funcMap()` in `render.go`.
3. Document in the "Template helpers" table in `README.md`.
4. Add tests in `render_test.go` (renders correctly via a template).

## Common gotchas

- **`build.go` line budget.** The toolchain warns at 500 lines. If you're
  adding more than a few lines to `build.go`, consider extracting into
  `format.go` or a new topical file.
- **Schema drift.** `api.schema.json` is validated against
  `api.example.json` by `TestExampleConfigMatchesSchema`. Any new field
  needs both documentation and an example in `api.example.json` if it's
  exercised by integration tests.
- **`spread` sentinel.** The `spread` template helper uses NUL (`\x00`)
  bytes to delimit elements and SOH (`\x01`) as an end marker. In
  argv-form commands, the executor splits on NUL into separate argv
  slots. In shell-form commands, `expandSpreadForShell` (`exec.go`)
  replaces each sentinel region with shell-quoted elements.
- **Number normalization in step results.** `parseResult` (`exec.go`)
  normalizes JSON numbers to `int64` / `float64` so sprig arithmetic works
  without casts. The format path's `parseInput("json", ...)` reuses this.
- **`when` predicate reuse.** `Step.When` and `Format.When` / `View.When`
  share the same truthiness rules (`isTruthy` in `format.go`): empty,
  `"false"`, `"0"`, `"no"` are falsy, everything else is truthy. Step
  `when` is evaluated in `runLeaf` (`build.go`) before entry rendering or
  command execution; format/view `when` is evaluated in `format.go` via
  `renderPredicate`.

## Tooling

- `go-toolchain` runs `go mod tidy`, vet, all tests with coverage, and the
  build. **Do not run bare `go build` / `go test` / `go mod tidy` —
  always `go-toolchain`.**
- Coverage minimum is 80% (toolchain enforces).
- CI: `.github/workflows/ci.yml` uses `wow-look-at-my/go-toolchain@v1`.

## Cloudflare Workers port (`workers/`)

A TypeScript reimplementation that runs on Cloudflare Workers. Parses the
same JSON config and serves each leaf command as an HTTP endpoint via
`fetch()` instead of shelling out to `curl`.

Key differences from the Go CLI:
- Commands are parsed as curl invocations and converted to `fetch()` calls.
- `cwd`, `stdin`, `confirm`, `fileExists`, `dirExists` are unavailable.
- Pipe commands (e.g. `| jq ...`) are stripped; use format views instead.
- Go `text/template` engine reimplemented in TypeScript with sprig subset.

Files: see `workers/README.md` for architecture and API mapping details.

Tests: `cd workers && npm test` runs 242 tests (188 Workers pool + 54
comparative tests against the Go example configs).

## Conventions

- Lowercase `lint`, `test`, etc. — go-toolchain handles all of it.
- Commit messages: clear "what + why" 1-2 line summary; do not lead with
  "Add" if the change is a refactor.
- Branch naming for Claude sessions: `claude/<descriptor>-<short-id>`.
- Squash-merge: PRs get squashed into a single commit on merge — do not
  rebase or force-push to clean up history.
