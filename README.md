# api-cli

A single Go binary that turns a JSON file into a full command-line client for an HTTP API. Drop in a config, get a CLI with `--help` at every level and shell tab completion — no hand-written commands.

## What it does

Given a JSON file describing your API's commands, subcommands, arguments, and flags, `api-cli` builds a [cobra](https://github.com/spf13/cobra)-backed CLI at runtime. Each leaf command maps the invocation to an HTTP request (method, path, query, headers, body) against your API and streams the response to stdout.

Help text and tab-completion scripts are generated automatically from the config — you describe the API once.

## Install

```sh
go-toolchain         # builds ./api-cli (and runs tests)
```

Or check out the binary into your `$PATH`.

## Quickstart

Drop a config file named `api.json` in the current directory (or pass `--config <path>`). A worked example lives at [`api.example.json`](./api.example.json) and targets the public `jsonplaceholder.typicode.com` fake REST API.

```sh
cp api.example.json api.json
./api-cli --help
./api-cli users get 1
./api-cli users list --limit 3
./api-cli posts list --user 1
./api-cli posts create --title "hi" --body "hello"
```

## Config schema

### Top level

| Field         | Type              | Notes                                         |
|---------------|-------------------|-----------------------------------------------|
| `name`        | string (required) | Binary's display name in help.                |
| `description` | string            | Shown as the CLI's header in `--help`.        |
| `defaults`    | object (required) | See below.                                    |
| `commands`    | array of Command  | Top-level subcommands.                        |

### `defaults`

| Field         | Type              | Notes                                                            |
|---------------|-------------------|------------------------------------------------------------------|
| `base_url`    | string (required) | Prefixed onto every leaf's `request.path`.                       |
| `headers`     | map<string,string>| Sent on every request; per-request headers override on collision.|

### A Command node

| Field         | Type              | Notes                                                           |
|---------------|-------------------|-----------------------------------------------------------------|
| `name`        | string (required) | Subcommand name. Cannot be `help`, `completion`, `__complete`.  |
| `description` | string            | Shown in help at this command's level and in parent listings.   |
| `args`        | array of Arg      | Positional args.                                                |
| `flags`       | array of Flag     | Named flags.                                                    |
| `request`     | object            | Present on leaves; describes the HTTP call.                     |
| `commands`    | array of Command  | Nested subcommands.                                             |

A node is a **leaf** (issues an HTTP call) if `request` is set. Otherwise it's a **group** that just prints help for its children.

### `args`

| Field         | Type              | Notes                                                           |
|---------------|-------------------|-----------------------------------------------------------------|
| `name`        | string (required) | Binding name. Must not collide with any flag in the same node.  |
| `type`        | `"string"`\|`"int"`| Default `string`.                                              |
| `required`    | bool              | Required args must precede optional ones.                       |
| `description` | string            | Shown in help.                                                  |

### `flags`

| Field         | Type                                           | Notes                                                    |
|---------------|------------------------------------------------|----------------------------------------------------------|
| `name`        | string (required)                              | Long form, e.g. `--limit`.                               |
| `short`       | single character                               | Optional short form, e.g. `-l`.                          |
| `type`        | `"string"`\|`"bool"`\|`"int"`\|`"string-slice"`| Default `string`.                                       |
| `default`     | any                                            | Used if the flag isn't set.                              |
| `required`    | bool                                           | Enforced by cobra with a clear error.                    |
| `description` | string                                         | Shown in help.                                           |

### `request`

| Field     | Type                   | Notes                                                              |
|-----------|------------------------|--------------------------------------------------------------------|
| `method`  | string (required)      | `GET`, `POST`, `DELETE`, ...                                       |
| `path`    | string (required)      | Appended to `defaults.base_url`. Supports template placeholders.   |
| `query`   | map<string,string>     | Each value is rendered. Empty renderings are dropped.              |
| `headers` | map<string,string>     | Each value is rendered. `\r`/`\n` rejected to block injection.     |
| `body`    | any JSON value         | String leaves are rendered; everything else passes through.        |

## Templates

All string fields in `request.path`, `request.query` values, `request.headers` values, `defaults.headers` values, and string leaves of `request.body` are rendered with Go's [`text/template`](https://pkg.go.dev/text/template). The data shape is:

- `{{.argname}}` — positional args, typed according to the `args` entry.
- `{{.flagname}}` — flag values, typed according to the `flags` entry.
- `{{.env.VARNAME}}` — process environment.

Args and flags share a single namespace; the config is rejected at load if they collide within a node.

Templates use `missingkey=error`, so a typo like `{{.idd}}` fails loudly rather than rendering an empty string.

### Auth example

```json
"defaults": {
  "base_url": "https://api.example.com",
  "headers": { "Authorization": "Bearer {{.env.API_TOKEN}}" }
}
```

Then run:

```sh
API_TOKEN=sk_live_... ./api-cli users list
```

### Body rendering

`body` is parsed as JSON first; only string leaves are template-rendered. This means you can mix literal JSON numbers/booleans with templated strings without fighting JSON escaping:

```json
"body": {
  "title": "{{.title}}",
  "userId": 1,
  "published": true
}
```

## Config discovery

First hit wins:

1. `--config <path>` on the command line.
2. `./api.json` in the current working directory.

Pass the flag anywhere; the parser accepts `--config=x` and `--config x`.

## Shell completion

Cobra generates completion scripts automatically:

```sh
./api-cli completion bash | sudo tee /etc/bash_completion.d/api-cli
# or, for the current shell only:
source <(./api-cli completion bash)
# or, for zsh:
./api-cli completion zsh > "${fpath[1]}/_api-cli"
# or, for fish:
./api-cli completion fish > ~/.config/fish/completions/api-cli.fish
```

Once installed, `api-cli <TAB>` completes subcommands and flags at every level.

## Exit codes

| Code | Meaning                         |
|------|---------------------------------|
| 0    | HTTP 2xx                        |
| 1    | Transport error, or CLI error   |
| 2    | Config not found or invalid     |
| 4    | HTTP 4xx                        |
| 5    | HTTP 5xx                        |

## Current limitations (intentional for v1)

- No `--data @file.json` or stdin body.
- No `-i` / response-header printing.
- No XDG config-directory lookup (just `--config` or `./api.json`).
- No dynamic completion against the remote API.
- Body templating substitutes string leaves only — numeric/boolean placeholders should be typed literally in the config.
- No retries or timeouts (uses `http.DefaultClient`).

## Development

```sh
go-toolchain        # runs go mod tidy, tests, coverage, and build
```

See [`CLAUDE.md`](./CLAUDE.md) for contributor guidance if present.
