# api-cli Workers

Cloudflare Workers port of `api-cli`. Parses the **same JSON config** as the
Go CLI and serves each leaf command as an HTTP endpoint, executing the
underlying API calls via `fetch()` instead of shelling out to `curl`.

## How it works

1. The config JSON is loaded from an environment variable (`API_CLI_CONFIG`)
   or a KV namespace binding (`CONFIG_KV`).
2. The command tree is flattened into leaf endpoints.
3. Each HTTP request is resolved to a leaf command via URL path.
4. The command template is rendered (exactly like the Go CLI), then **parsed
   as a curl command** to extract URL, method, headers, and body.
5. The extracted request is executed via `fetch()`.
6. If a format is configured, the response is parsed and rendered through the
   format's view templates.

## URL mapping

CLI invocations map to HTTP requests:

| CLI                                          | HTTP                                         |
|----------------------------------------------|----------------------------------------------|
| `api-cli users get 1`                        | `GET /users/get/1`                           |
| `api-cli users list --limit 3`               | `GET /users/list?limit=3`                    |
| `api-cli posts create --title hi --body hey` | `GET /posts/create?title=hi&body=hey`        |
| `api-cli user-posts Bret`                    | `GET /user-posts/Bret`                       |

### Route structure

```
GET /<command-path>[/<positional-args>...]?<flags>
```

- **Command path**: slash-joined command names (`users/get`, `repo/issues`)
- **Positional args**: appended as path segments after the command
- **Named args/flags**: query parameters (`?id=1&limit=10`)
- Path args take priority when both path and query provide the same arg

### Special routes

| Route         | Description                                            |
|---------------|--------------------------------------------------------|
| `GET /`       | Index: lists all endpoints with args, flags, types     |
| `GET /_health`| Health check: `{"status":"ok"}`                        |
| `GET /_commands` | JSON list of leaf commands with metadata            |
| `GET /_warnings` | Config compatibility warnings for Workers           |

### Special query parameters

| Param      | Values                  | Effect                                        |
|------------|-------------------------|-----------------------------------------------|
| `_format`  | `raw\|auto\|always`     | Control output formatting (like `--format`)   |
| `_view`    | `<name>`                | Force a specific view (like `--view`)         |

### Response headers

| Header                  | Description                                         |
|-------------------------|-----------------------------------------------------|
| `X-API-CLI-Executions`  | Number of fetch calls made (steps + leaf)            |
| `X-API-CLI-Warnings`    | JSON array of warnings about unsupported features    |

## What works

Everything that maps to HTTP requests:

- **Config parsing and validation** — identical to the Go CLI
- **Template rendering** — Go `text/template` engine reimplemented in TypeScript
  with full sprig function subset
- **Entry rendering** — string leaves templated, non-strings pass through
- **Vars inheritance** — child overrides parent, templates in scope
- **Command inheritance** — closest ancestor wins
- **Steps** — sequential pre-execution stages, results captured and chained
- **Conditional steps** — `when` predicates with same truthiness rules as Go
- **Preconditions** — evaluated before execution, non-empty = error
- **Output formatting** — `format.when` predicates, view selection, template rendering
- **Named and inline formats** — both supported with full inheritance
- **Format input modes** — `json`, `lines`, `raw`
- **View selection** — by flag, by predicate, by default, by position
- **Args and flags** — typed (string, int, bool, string-slice, variadic)
- **Templated flag defaults** — rendered at invocation time
- **Curl command parsing** — extracts URL, method, headers, body from rendered commands
- **Template helpers** — `querystring`, `urlpath`, `shellquote`, `tabwriter`,
  `padRight`, `padLeft`, `displayWidth`, `stripANSI`, `repeatkey`, plus ~60 sprig functions

## What doesn't work (with warnings)

Features that require a local OS and are flagged via `/_warnings`:

| Feature          | Behavior on Workers                                    |
|------------------|--------------------------------------------------------|
| `cwd`            | Ignored (no filesystem)                                |
| `stdin`          | Ignored (no process stdin)                             |
| `confirm`        | Skipped (no interactive prompts)                       |
| `fileExists`     | Always returns `false`                                 |
| `dirExists`      | Always returns `false`                                 |
| `spread`         | Works in template rendering but argv form uses curl parser |
| Non-curl commands| Error: only curl-based commands can be executed        |
| Pipe to jq/etc.  | Warning: pipe target is dropped; use format views instead |
| `-o` (file output)| Warning: file output not supported                    |
| Shell completion | N/A (HTTP, not a CLI)                                  |
| Exit codes       | Mapped to HTTP status codes (non-zero → 502)           |

## Config deployment

### Environment variable

```toml
# wrangler.toml
[vars]
API_CLI_CONFIG = '{"name":"myapi","command":"curl ...","commands":[...]}'
```

### KV namespace

```toml
# wrangler.toml
[[kv_namespaces]]
binding = "CONFIG_KV"
id = "your-kv-namespace-id"
```

Then upload the config:
```sh
wrangler kv key put --namespace-id <id> config "$(cat api.json)"
```

### Environment bindings as template vars

Any string env bindings are available as `{{.env.BINDING_NAME}}` in templates.
Set API tokens via Wrangler secrets:

```sh
wrangler secret put GITHUB_TOKEN
```

## Development

```sh
npm install
npm test             # runs Workers pool + comparative tests (242 total)
npm run test:workers # Workers pool tests only (188 tests)
npm run test:comparative  # Node.js comparative tests (54 tests)
npm run typecheck    # TypeScript type checking
npm run dev          # local dev server via wrangler
```

## Architecture

| File                  | Role                                                    |
|-----------------------|---------------------------------------------------------|
| `src/index.ts`        | Worker entry point, request routing                     |
| `src/config.ts`       | Config types and validation (port of `config.go`)       |
| `src/template.ts`     | Go `text/template` engine in TypeScript                 |
| `src/template-funcs.ts`| Sprig subset + custom helpers (port of `render.go`)   |
| `src/render.ts`       | `renderString`, `renderEntry`, `renderVars`, `mergeVars`|
| `src/curl-parser.ts`  | Extracts HTTP requests from rendered curl commands      |
| `src/router.ts`       | URL path → leaf resolution, arg/flag extraction         |
| `src/format.ts`       | Format system: views, predicates, input parsing         |
| `src/exec.ts`         | Orchestrates rendering → curl parse → fetch → format    |
| `src/align.ts`        | Width-aware column alignment (port of `align.go`)       |
| `src/warnings.ts`     | Detects unsupported features in configs                 |
