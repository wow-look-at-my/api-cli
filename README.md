# api-cli

A declarative command-line alias system. You write JSON describing a tree of
commands (subcommands, args, flags, user-defined variables); the tool renders
a Go `text/template` for each leaf and executes the result. Help at every
level, shell tab completion, and strong templating come for free.

It's not an HTTP client — it just runs shell or `argv` commands — but the
canonical use case is wrapping a REST API with `curl`, which the shipped
[`api.example.json`](./api.example.json) demonstrates.

## Install

```sh
go-toolchain     # runs tests + builds ./build/api-cli
```

Drop the binary on your `$PATH`.

## Quickstart

```sh
cp api.example.json api.json       # or pass --config <path>
./api-cli --help
./api-cli users get 1
./api-cli users list --limit 3
./api-cli posts create --title "hi" --body "hello"
```

## How it works

Each leaf command renders a `command` template against a data context:

| Namespace | Source                                                          |
|-----------|-----------------------------------------------------------------|
| `.arg`    | Positional args by name, typed per the `args` entry.            |
| `.flag`   | Named flags by name, typed per the `flags` entry.               |
| `.env`    | Process environment (`{{.env.API_TOKEN}}`).                     |
| `.var`    | Merged `vars` from the root down to this node.                  |
| `.entry`  | The leaf's user-defined map — arbitrary JSON, string leaves templated first. |

The closest `command` template up the ancestor chain is used. The rendered
command is executed and its exit code propagates.

### Command forms

`command` may be either:

1. **A string** — rendered as a template, executed via `/bin/sh -c <rendered>`.
   Good for pipelines; interpolated values must be shell-quoted (use the
   `shellquote` helper).
2. **An array of strings** — each element is rendered and the result is exec'd
   directly without a shell. Safe by default; no shell metacharacters are
   interpreted.

### Example: wrap a REST API

```jsonc
{
  "name": "apicli",
  "vars": { "base_url": "https://api.example.com/v1" },

  // Runs for every leaf by default; override per-leaf when it doesn't fit.
  "command": "curl -fsSL -H 'Authorization: Bearer {{.env.API_TOKEN}}' {{.var.base_url}}{{.entry.path}}{{querystring .entry.query}}",

  "commands": [
    {
      "name": "users",
      "commands": [
        {
          "name": "get",
          "args": [{ "name": "id", "type": "int", "required": true }],
          "entry": { "path": "/users/{{.arg.id}}" }
        },
        {
          "name": "list",
          "flags": [{ "name": "limit", "short": "l", "type": "int", "default": 10 }],
          "entry": {
            "path": "/users",
            "query": { "_limit": "{{.flag.limit}}" }
          }
        },
        {
          "name": "create",
          "flags": [
            { "name": "name", "type": "string", "required": true },
            { "name": "email", "type": "string", "required": true }
          ],
          // Override: argv form so the body doesn't need shell escaping.
          "command": [
            "curl", "-fsSL", "-X", "POST",
            "-H", "Content-Type: application/json",
            "-d", "{\"name\":{{.flag.name | toJson}},\"email\":{{.flag.email | toJson}}}",
            "{{.var.base_url}}/users"
          ]
        }
      ]
    }
  ]
}
```

### Example: generic aliases

The tool doesn't know or care that the command is HTTP. Here's a tiny git
wrapper:

```json
{
  "name": "gx",
  "vars": { "prefix": "feature/" },
  "command": ["git", "{{.entry.op}}", "{{.var.prefix}}{{.arg.name}}"],
  "commands": [
    { "name": "start", "args": [{"name":"name","required":true}], "entry": {"op": "checkout -b"} },
    { "name": "push",  "args": [{"name":"name","required":true}], "entry": {"op": "push -u origin"} }
  ]
}
```

## Config schema

### Top level

| Field         | Type              | Notes                                                             |
|---------------|-------------------|-------------------------------------------------------------------|
| `name`        | string (required) | Binary's display name in help.                                    |
| `description` | string            | Shown as the CLI's header in `--help`.                            |
| `vars`        | `map<string,any>` | Shared variables inherited by all subcommands.                    |
| `command`     | string or `[]string` | Default command template for the whole CLI.                   |
| `commands`    | `[]Command`       | Top-level subcommands.                                            |

### Command node

| Field         | Type              | Notes                                                             |
|---------------|-------------------|-------------------------------------------------------------------|
| `name`        | string (required) | Subcommand name. Cannot be `help`, `completion`, `__complete`.    |
| `description` | string            | Shown in help.                                                    |
| `args`        | `[]Arg`           | Positional args.                                                  |
| `flags`       | `[]Flag`          | Named flags.                                                      |
| `vars`        | `map<string,any>` | Merged with ancestor vars (this node wins on collision).          |
| `command`     | string or `[]string` | Overrides inherited command for this subtree.                  |
| `entry`       | any JSON object   | Leaf-only. Arbitrary user-defined data; string leaves templated.  |
| `commands`    | `[]Command`       | Nested subcommands.                                               |

A node is a **leaf** if it has no `commands`; leaves execute. Groups just print
help.

### `args`

| Field         | Type               | Notes                                                            |
|---------------|--------------------|------------------------------------------------------------------|
| `name`        | string (required)  | Binding name; accessed as `{{.arg.name}}`.                       |
| `type`        | `"string"`\|`"int"` | Default `string`.                                               |
| `required`    | bool               | Required args must precede optional ones.                        |
| `description` | string             | Shown in help.                                                   |

### `flags`

| Field         | Type                                            | Notes                                                          |
|---------------|-------------------------------------------------|----------------------------------------------------------------|
| `name`        | string (required)                               | Long form (`--limit`); accessed as `{{.flag.limit}}`.          |
| `short`       | single character                                | Optional short form (`-l`).                                    |
| `type`        | `"string"`\|`"bool"`\|`"int"`\|`"string-slice"` | Default `string`.                                              |
| `default`     | any                                             | Value when the flag isn't set.                                 |
| `required`    | bool                                            | Enforced with a clear error.                                   |
| `description` | string                                          | Shown in help.                                                 |

## Template helpers

Every [sprig v3](https://masterminds.github.io/sprig/) helper is available —
that's where `toJson`, `upper`, `lower`, `trim`, `default`, `required`,
`b64enc`, `regexReplaceAll`, `hasKey`, etc. come from. On top of sprig we add:

| Helper        | Purpose                                                                                   |
|---------------|-------------------------------------------------------------------------------------------|
| `querystring` | Render a map as `?k=v&k=v` (URL-encoded). Empty values dropped. Empty map → empty string. |
| `shellquote`  | POSIX single-quote a value for safe interpolation into the string form of `command`.      |
| `urlpath`     | URL-escape a single path segment.                                                         |

Example:

```
command: "curl {{.var.base_url}}/search?q={{urlpath .arg.q}}"
```

## Template semantics

- Parsed with `missingkey=zero` — missing map keys do not error; for
  `map[string]interface{}` they render as `<no value>`. Use `{{default "" .x}}`
  for an empty fallback, `{{if .x}}...{{end}}` for conditionals, and
  `{{required "msg" .x}}` when you want the error.
- `entry` is rendered first (without `.entry` in scope). Every string leaf of
  the JSON is rendered independently; numbers/booleans/nulls pass through. The
  result is exposed as `.entry` for the command template.
- `vars` are rendered the same way as `entry`, with `{arg, flag, env}` in scope.
- Template errors on either stage abort the run with a clear message.

## Config discovery

First hit wins:

1. `--config <path>` anywhere on the command line (`--config=x` or `--config x`).
2. `./api.json` in the current working directory.

## Shell completion

Cobra generates completion scripts automatically:

```sh
source <(./api-cli completion bash)
./api-cli completion zsh  > "${fpath[1]}/_api-cli"
./api-cli completion fish > ~/.config/fish/completions/api-cli.fish
```

## Exit codes

The CLI inherits the exit code of the executed child command. Additionally:

| Code | Meaning                                    |
|------|--------------------------------------------|
| 0    | Child exited 0.                            |
| 1    | Render error, cobra usage error, or empty command. |
| 2    | Config not found or invalid.               |
| 127  | Child binary not found or failed to start. |
| N    | Any other value is the child's exit code.  |

## Development

```sh
go-toolchain        # runs go mod tidy, tests, coverage, and build
```
