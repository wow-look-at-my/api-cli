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

| Namespace  | Source                                                                       |
|------------|------------------------------------------------------------------------------|
| `.arg`     | Positional args by name, typed per the `args` entry.                         |
| `.flag`    | Named flags by name, typed per the `flags` entry.                            |
| `.env`     | Process environment (`{{.env.API_TOKEN}}`).                                  |
| `.var`     | Merged `vars` from the root down to this node.                               |
| `.result`  | Captured outputs of `steps`, keyed by step name. JSON outputs are structured; non-JSON outputs are strings. |
| `.entry`   | The leaf's user-defined map — arbitrary JSON, string leaves templated first. |

The closest `command` template up the ancestor chain is used. The rendered
command is executed and its exit code propagates.

### Command forms

`command` may be either:

1. **A string** — rendered as a template, executed via `/bin/sh -c <rendered>`.
   Good for pipelines; interpolated values must be shell-quoted (use the
   `shellquote` helper).
2. **An array of strings** — each element is rendered and the result is exec'd
   directly without a shell. Safe by default; no shell metacharacters are
   interpreted. To pass a variable number of arguments, use the `spread` helper
   on a single element: `"{{spread .arg.files}}"` expands to zero or more argv
   slots from a slice.

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

### Example: tar wrapper (variadic args, spread, preconditions, dynamic default)

```jsonc
{
  "name": "tar-safe",
  "commands": [
    {
      "name": "create",
      "args": [
        { "name": "archive", "required": true },
        { "name": "files", "variadic": true, "required": true,
          "description": "One or more files/directories to include." }
      ],
      "preconditions": [
        "{{if fileExists .arg.archive}}{{.arg.archive}} already exists; pick a new name or remove it{{end}}"
      ],
      // argv form: no shell, no quoting concerns. `spread` expands the
      // []string slice into N argv slots.
      "command": ["tar", "-czf", "{{.arg.archive}}", "{{spread .arg.files}}"]
    },
    {
      "name": "extract",
      "args": [
        { "name": "archive", "required": true }
      ],
      "flags": [
        // Templated default: if the user doesn't pass --to, derive it from the
        // archive name (foo.tar.gz → foo).
        { "name": "to", "default": "{{trimSuffix \".tar.gz\" .arg.archive}}" }
      ],
      "preconditions": [
        "{{if not (fileExists .arg.archive)}}archive {{.arg.archive}} does not exist{{end}}"
      ],
      "command": ["tar", "-xzf", "{{.arg.archive}}", "-C", "{{.flag.to}}"]
    }
  ]
}
```

```sh
./tar-safe create out.tar.gz src/ README.md  # variadic positional args
./tar-safe extract out.tar.gz                 # --to defaults to "out"
```

## Result reuse across calls

A leaf command can declare `steps` — pre-execution stages that run before the
leaf's own command. Each step's stdout is captured and exposed under
`.result.<name>` for subsequent steps and the leaf's own `entry`/`command`
templates. This enables patterns like **indirection** (resolve a name to an ID,
then use it), **joins** (fetch two resources and combine fields), and
**fan-out/fan-in** pipelines.

```jsonc
{
  "name": "user-posts",
  "description": "List posts for a user looked up by username.",
  "args": [{ "name": "username", "required": true }],
  "steps": [
    {
      "name": "user",
      // Inherits the root command. .result.user is set to the parsed JSON output.
      "entry": { "path": "/users", "query": { "username": "{{.arg.username}}" } }
    }
  ],
  // The leaf's own entry can now reference .result.user.
  "entry": {
    "path": "/posts",
    "query": { "userId": "{{(index .result.user 0).id}}" }
  }
}
```

```sh
./api-cli user-posts Bret          # 2 API calls; count printed to stderr
./api-cli --quiet user-posts Bret  # same, but the count is suppressed
```

### Step semantics

- Steps run in declaration order.
- Each step's `entry` is rendered against the current context, **including
  `.result.*` from steps that already ran**.
- A step's stdout is parsed as JSON (with `UseNumber`, so large integers are
  preserved). If stdout is not valid JSON, it is stored as a plain string.
- If a step exits non-zero the run aborts immediately with that exit code; the
  leaf's own command does **not** run.
- A step can override `command` just like a leaf can. If it has no `command`,
  the closest ancestor `command` template is used.
- When more than one command ran (i.e., there is at least one step), the count
  is printed to **stderr** after the run:
  ```
  2 executions
  ```
  Pass `--quiet` / `-q` anywhere on the command line to suppress this.

## Config schema

A complete JSON Schema (draft-07) lives at [`api.schema.json`](./api.schema.json).
Reference it from your config for editor completion and validation:

```json
{
  "$schema": "./api.schema.json",
  "name": "apicli",
  ...
}
```

The runtime loader ignores the `$schema` field. Editors that understand JSON
Schema (VS Code, JetBrains, Neovim with `coc-json`/`yamlls`, etc.) will pick it
up automatically.

### Top level

| Field         | Type              | Notes                                                             |
|---------------|-------------------|-------------------------------------------------------------------|
| `$schema`     | string            | Optional editor hint pointing at `api.schema.json`. Ignored at runtime. |
| `name`        | string (required) | Binary's display name in help.                                    |
| `description` | string            | Shown as the CLI's header in `--help`.                            |
| `vars`        | `map<string,any>` | Shared variables inherited by all subcommands.                    |
| `command`     | string or `[]string` | Default command template for the whole CLI.                   |
| `cwd`         | string            | Default working directory template for executed commands. Inherited by every subcommand unless overridden. See [Working directory](#working-directory). |
| `stdin`       | string            | Default stdin template for executed commands. Inherited by every subcommand unless overridden. See [Stdin](#stdin). |
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
| `cwd`         | string            | Overrides inherited working directory for this subtree. See [Working directory](#working-directory). |
| `stdin`       | string            | Overrides inherited stdin template for this subtree. See [Stdin](#stdin). |
| `steps`       | `[]Step`          | Leaf-only. Pre-execution stages; results exposed as `.result.*`.  |
| `entry`       | any JSON object   | Leaf-only. Arbitrary user-defined data; string leaves templated.  |
| `preconditions` | `[]string`      | Leaf-only. Templates evaluated against `{arg, flag, env, var}` before any step or command runs; if any renders to a non-empty (post-trim) string, it's treated as a fatal error message and the leaf exits 1. |
| `commands`    | `[]Command`       | Nested subcommands.                                               |

A node is a **leaf** if it has no `commands`; leaves execute. Groups just print
help.

### `steps`

| Field     | Type              | Notes                                                              |
|-----------|-------------------|--------------------------------------------------------------------|
| `name`    | string (required) | Key under `.result`; accessed as `{{.result.name}}`.              |
| `entry`   | any JSON object   | Rendered like a leaf `entry`; available as `.entry` when the step's command runs. |
| `command` | string or `[]string` | Overrides the inherited command for this step only.             |
| `cwd`     | string            | Overrides the inherited working directory for this step only. See [Working directory](#working-directory). |
| `stdin`   | string            | Overrides the inherited stdin template for this step only. See [Stdin](#stdin). |

### `args`

| Field         | Type               | Notes                                                            |
|---------------|--------------------|------------------------------------------------------------------|
| `name`        | string (required)  | Binding name; accessed as `{{.arg.name}}`.                       |
| `type`        | `"string"`\|`"int"` | Default `string`.                                               |
| `required`    | bool               | Required args must precede optional ones.                        |
| `variadic`    | bool               | Last-only. Collects all remaining positional values into a typed slice. Pair with the `spread` helper to splat into argv form. |
| `description` | string             | Shown in help.                                                   |

### `flags`

| Field         | Type                                            | Notes                                                          |
|---------------|-------------------------------------------------|----------------------------------------------------------------|
| `name`        | string (required)                               | Long form (`--limit`); accessed as `{{.flag.limit}}`. Names starting with `no-` are reserved for bool negation. |
| `short`       | single character                                | Optional short form (`-l`).                                    |
| `type`        | `"string"`\|`"bool"`\|`"int"`\|`"string-slice"` | Default `string`.                                              |
| `default`     | any                                             | Value when the flag isn't set. For string flags, this may itself be a Go template — it is rendered against `{arg, env, var}` only when the user did not pass the flag. |
| `required`    | bool                                            | Enforced with a clear error.                                   |
| `conflicts`   | `[]string`                                      | Sibling flag names that may not be set together.               |
| `description` | string                                          | Shown in help.                                                 |

A bool flag whose `default` is `true` automatically gets a hidden `--no-NAME`
companion: `{name: "verbose", default: true}` accepts both `--verbose=false`
and `--no-verbose`.

## Template helpers

Every [sprig v3](https://masterminds.github.io/sprig/) helper is available —
that's where `toJson`, `upper`, `lower`, `trim`, `default`, `required`,
`b64enc`, `regexReplaceAll`, `hasKey`, etc. come from. On top of sprig we add:

| Helper        | Purpose                                                                                   |
|---------------|-------------------------------------------------------------------------------------------|
| `querystring` | Render a map as `?k=v&k=v` (URL-encoded). Empty values dropped. Empty map → empty string. |
| `shellquote`  | POSIX single-quote a value for safe interpolation into the string form of `command`.      |
| `urlpath`     | URL-escape a single path segment.                                                         |
| `spread`      | Argv-form only: splat a slice into multiple argv slots. The element `"{{spread .arg.files}}"` becomes N entries (zero for an empty slice). Works with `[]string`, `[]int`, `[]any`. |
| `fileExists`  | Returns true if the path exists and is a regular file. Useful inside `preconditions`.     |
| `dirExists`   | Returns true if the path exists and is a directory.                                       |

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

## Working directory

Every executed command — leaf commands and steps alike — runs in some working
directory. By default that's the calling process's cwd, exactly as if you'd
typed the command in your shell. The `cwd` field overrides that default and
inherits down the tree, mirroring how `command` works:

- `cwd` may appear at the top level of the config, on any `Command` node, and
  on any `Step`.
- The closest non-empty `cwd` up the ancestor chain wins. A leaf can override
  its group's cwd; a step can override its leaf's.
- The value is a Go template, rendered against the same context as the command
  it applies to (so `.arg`, `.flag`, `.env`, `.var`, and — where they're in
  scope — `.entry` and `.result` are all available).
- An empty/unset `cwd` means "no override" — fall through to the next
  ancestor, or ultimately to the calling process's cwd.

Typical use is a repo-scoped CLI that should always run from the repo root:

```jsonc
{
  "name": "stack",
  "cwd": "{{.env.STACKS_ROOT}}",
  "command": ["docker", "compose", "{{.entry.op}}"],
  "commands": [
    { "name": "up",   "entry": { "op": "up" } },
    { "name": "down", "entry": { "op": "down" } }
  ]
}
```

A step can override the leaf's cwd to run a one-off command somewhere else:

```jsonc
{
  "name": "deploy",
  "cwd": "{{.env.REPO_ROOT}}",
  "steps": [
    { "name": "version", "cwd": "{{.env.REPO_ROOT}}/infra", "command": "git rev-parse HEAD" }
  ],
  "command": ["./bin/deploy", "--sha", "{{.result.version}}"]
}
```

If the rendered `cwd` doesn't exist, the child fails to start and exits 127.

## Stdin

The `stdin` field feeds a rendered string to the child process's standard input.
It inherits down the tree exactly like `cwd`: the closest non-empty ancestor
wins, and a step can override its leaf's stdin.

- `stdin` may appear at the top level of the config, on any `Command` node, and
  on any `Step`.
- The value is a Go template, rendered against the same context as the command
  it applies to.
- When non-empty, the rendered string is fed to the child's stdin (and stdin
  closes after). When empty/unset, the child inherits the parent process's stdin.
- The template author controls newline handling: append `\n` in the template if
  the tool expects a trailing newline.

This is especially useful with argv-form commands that need input on stdin
without resorting to shell pipes:

```jsonc
{
  "name": "jq-tool",
  "commands": [
    {
      "name": "format",
      "flags": [
        { "name": "body", "short": "b", "type": "string", "required": true }
      ],
      // No shell, no pipe, no quoting hazards.
      "command": ["jq", "-s", "."],
      "stdin": "{{.flag.body}}"
    }
  ]
}
```

A step can override the leaf's stdin:

```jsonc
{
  "name": "deploy",
  "stdin": "default-input\n",
  "steps": [
    { "name": "check", "stdin": "step-specific-input\n", "command": ["cat"] }
  ],
  "command": ["cat"]
}
```

## Config discovery

First hit wins:

1. `--config <path>` anywhere on the command line (`--config=x` or `--config x`).
2. `api.json` in the current working directory.
3. `api.json` in any ancestor directory, walking up to the filesystem root.

This mirrors how git finds `.git` -- you can run the CLI from any subdirectory
of a repo that contains `api.json` at its root.

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
