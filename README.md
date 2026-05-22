# api-cli

A declarative command-line alias system. You write JSON describing a tree of
commands (subcommands, args, flags, user-defined variables); the tool renders
a Go `text/template` for each leaf and executes the result. Help at every
level, shell tab completion, and strong templating come for free.

It's not an HTTP client — it just runs shell or `argv` commands — but the
canonical use case is wrapping a REST API with `curl`. Two example configs
ship in this repo:

- [`api.example.json`](./api.example.json) — a minimal demo against
  `jsonplaceholder.typicode.com`.
- [`github.example.json`](./github.example.json) — a real, useful read-only
  wrapper for the GitHub REST API with table/detail views and aggressive
  noise-trimming (drops every `*url` field with `jq`, cutting response size
  by 50–70%). See [GitHub example](#github-example) below.

## Install

```sh
go-toolchain     # runs tests + builds ./build/api-cli
```

Drop the binary on your `$PATH`.

## Recommended setup

`api-cli` is the engine; each API or alias group you wrap gets its own thin
wrapper script on your `$PATH` that pins the config. That gives you a stable
top-level command name (with help, completion, and templating all flowing
through it) without rebuilding the binary per use case.

1. Put the `api-cli` binary on your `$PATH` (see [Install](#install)).
2. Save your config somewhere stable, e.g. `~/.config/myapi/api.json`.
3. Create an executable shell script like `~/.local/bin/myapi`:

   ```bash
   #!/bin/bash
   set -euo pipefail
   api-cli --config ~/.config/myapi/api.json "$@"
   ```

Now `myapi users get 1` works from anywhere. Repeat per API to maintain
several wrappers from one `api-cli` install.

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

### Example: passthrough wrapper (wrapping commands with unknown flags)

```jsonc
{
  "name": "cicc-cache",
  "commands": [{
    "name": "exec",
    "passthrough": true,
    "flags": [
      { "name": "o", "type": "string" },
      { "name": "gen_c_file_name", "type": "string" },
      { "name": "gen_device_file_name", "type": "string" }
    ],
    "steps": [
      {
        "name": "hash",
        "command": "md5sum {{.rest | filterSuffix \".cpp1.ii\" | first}} | cut -d' ' -f1"
      }
    ],
    "command": "/usr/local/cuda/nvvm/bin/cicc.real {{spread .rest}}"
  }]
}
```

```sh
# Wrapper script at /usr/local/cuda/nvvm/bin/cicc:
exec api-cli --config /path/to/cicc-cache.json exec -- "$@"
# All unknown flags (--c++17, -arch compute_80, etc.) pass through in .rest
```

## Passthrough mode

When a leaf sets `"passthrough": true`, the command accepts arbitrary positional
args (everything after `--` in the wrapper script) and performs its own minimal
flag extraction:

1. Only explicitly declared `flags` are recognized (matched with one or two leading
   dashes, e.g. both `-o` and `--o` work).
2. Everything else — unknown flags, their values, and bare positional args — is
   collected into `.rest` (a `[]string`).
3. Extracted flags do NOT appear in `.rest`, so `{{spread .rest}}` reconstructs the
   original command line minus the captured flags.

`.rest` is available in all template contexts: steps, entry, and the leaf command.
Use `{{spread .rest}}` in argv-form commands or shell-form commands to forward
the remaining arguments.

**Constraints:**
- `passthrough` is mutually exclusive with `args` (use `.rest` instead).
- Only allowed on leaf nodes (no subcommands).
- Flags support `=` syntax (`--flag=value`, `-flag=value`) and next-arg syntax
  (`--flag value`, `-flag value`).
- `bool` flags consume no value argument (unless `--flag=true` form is used).
- `string-slice` flags accumulate across multiple occurrences.

**Helpers for filtering `.rest`:**
- `filterSuffix` — keep elements ending with a suffix: `{{.rest | filterSuffix ".ii" | first}}`
- `filterPrefix` — keep elements starting with a prefix: `{{.rest | filterPrefix "--"}}`

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

### Conditional steps

A step can include a `when` predicate — a Go template evaluated against the
current data context (`{.arg, .flag, .env, .var, .result}`). When `when` is
absent, the step runs unconditionally. When present and falsy (empty string,
`"false"`, `"0"`, `"no"`), the step is skipped entirely and `.result.<name>`
is not populated.

This lets config authors skip expensive or irrelevant API calls based on input
shape. Common pattern: if the user supplies a numeric ID use it directly;
otherwise resolve it via a lookup step.

```jsonc
{
  "name": "user-or-id",
  "args": [{ "name": "id", "required": true }],
  "steps": [
    {
      "name": "resolved",
      // Only run the lookup when the arg is NOT a bare number.
      "when": "{{not (regexMatch `^[0-9]+$` .arg.id)}}",
      "command": "curl -fsSL https://api.example.com/users?username={{.arg.id}}"
    }
  ],
  // Branch on whether the step ran.
  "command": "curl -fsSL https://api.example.com/users/{{if .result.resolved}}{{(index .result.resolved 0).id}}{{else}}{{.arg.id}}{{end}}"
}
```

A skipped step does not count toward the execution count and does not affect
subsequent steps — they see `.result.<name>` as absent (nil), which is the
zero value for `missingkey=zero` templates.

### Debugging execution

Pass `--verbose` to see what commands are being executed, their exit codes,
and condition evaluation results. All output goes to stderr, prefixed with
`[verbose]`:

```
$ apicli --verbose users get alice
[verbose] leaf "get": starting
[verbose] step "resolve": executing
[verbose] capture: /bin/sh -c curl -fsSL https://api.example.com/users?username=alice
[verbose] capture: exit code 0
[verbose] step "resolve": exit code 0
[verbose] leaf "get": executing command
[verbose] exec: /bin/sh -c curl -fsSL https://api.example.com/users/42
[verbose] exec: exit code 0
[verbose] leaf "get": exit code 0
[verbose] leaf "get": 2 executions total
{"id":42,"name":"alice"}
2 executions
```

Pass `--debug` for full detail (implies `--verbose`): the data context,
step stdout captures, format decisions, and rendered entries. These lines
are prefixed with `[debug]`:

```
$ apicli --debug users get alice
[verbose] leaf "get": starting
[debug]   leaf "get": data context: {"arg":{"name":"alice"},...}
[debug]   step "resolve": entry: {}
...
[debug]   step "resolve": stdout: [{"id":42,"name":"alice"}]
[debug]   format: none configured, streaming raw
```

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
| `formats`     | `map<string,Format>` | Named, reusable output formats; commands reference them by name. See [Output formatting](#output-formatting). |
| `commands`    | `[]Command`       | Top-level subcommands.                                            |

### Command node

| Field         | Type              | Notes                                                             |
|---------------|-------------------|-------------------------------------------------------------------|
| `name`        | string (required) | Subcommand name. Cannot be `help`, `completion`, `__complete`.    |
| `description` | string            | Shown in help.                                                    |
| `passthrough` | bool              | Leaf-only. Disables flag parsing; only declared flags are extracted from the raw args. Everything else goes into `.rest`. See [Passthrough mode](#passthrough-mode). |
| `args`        | `[]Arg`           | Positional args. Mutually exclusive with `passthrough`.           |
| `flags`       | `[]Flag`          | Named flags.                                                      |
| `vars`        | `map<string,any>` | Merged with ancestor vars (this node wins on collision).          |
| `command`     | string or `[]string` | Overrides inherited command for this subtree.                  |
| `cwd`         | string            | Overrides inherited working directory for this subtree. See [Working directory](#working-directory). |
| `stdin`       | string            | Overrides inherited stdin template for this subtree. See [Stdin](#stdin). |
| `steps`       | `[]Step`          | Leaf-only. Pre-execution stages; results exposed as `.result.*`.  |
| `entry`       | any JSON object   | Leaf-only. Arbitrary user-defined data; string leaves templated.  |
| `preconditions` | `[]string`      | Leaf-only. Templates evaluated against `{arg, flag, env, var}` before any step or command runs; if any renders to a non-empty (post-trim) string, it's treated as a fatal error message and the leaf exits 1. |
| `confirm`     | string            | Template rendered against `{arg, flag, env, var}`. If non-empty, the user is prompted `<message> [y/N]` on stderr before any step or command runs. Pass `--yes` / `-y` to bypass. Non-tty stdin without `--yes` is a hard error. Inherits down the tree like `command` and `cwd` (closest non-empty ancestor wins). |
| `format`      | string or Format  | Output format for the leaf's stdout. A string names an entry in top-level `formats`; an object is an inline definition. Inherits down the tree like `command`/`cwd`/`stdin`. See [Output formatting](#output-formatting). |
| `commands`    | `[]Command`       | Nested subcommands.                                               |

A node is a **leaf** if it has no `commands`; leaves execute. Groups just print
help.

### `steps`

| Field     | Type              | Notes                                                              |
|-----------|-------------------|--------------------------------------------------------------------|
| `name`    | string (required) | Key under `.result`; accessed as `{{.result.name}}`.              |
| `when`    | string            | Go-template predicate. When absent or truthy, the step runs normally. When falsy (empty string, `false`, `0`, `no`), the step is skipped and `.result.<name>` is not set. Same truthiness rules as `format.when`. |
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

## Output formatting

Inspired by PowerShell's `.format.ps1xml`. A `Format` is a presentation layer
between a leaf command's stdout and the user. The wrapped command emits
structured data (typically JSON); the format renders it through a Go template
for display. Authors define formats once in the config; the runtime decides
when to apply them.

### When formatting is applied

Output is formatted **iff both sides agree**: the format author allows it AND
the user allows it. Either side can veto.

- Author side: `format.when` is a Go-template predicate evaluated against
  `{.tty, .width, .data, .arg, .flag, .env, .var, .entry, .result}`. Empty
  defaults to `{{.tty}}` — only format on a terminal. Falsy values:
  empty string, `false`, `0`, `no` (case-insensitive).
- User side: enabled by default. To disable, pass `--no-format` or
  `--format=raw`, or set `NO_FORMAT=1`. To force-on (overriding `NO_FORMAT`
  in the environment), pass `--format=always` — the user "lies" about being
  on a TTY, but the author's `when` still has final say.

Precedence (top wins):

1. `--no-format` flag
2. `--format=raw|auto|always`
3. `NO_FORMAT` env var (any non-empty value)
4. `API_CLI_FORMAT=raw|auto|always`
5. Default: `auto`

### Format definition

| Field   | Type            | Notes                                                              |
|---------|-----------------|--------------------------------------------------------------------|
| `input` | `"json"`\|`"lines"`\|`"raw"` | How to parse captured stdout. Default `json`. `lines` splits on `\n` into `[]string`; `raw` is the trimmed string. |
| `when`  | string          | Predicate template; default `{{.tty}}`.                            |
| `views` | `[]View`        | At least one view. The runtime selects which one to render.        |

### View definition

| Field      | Type   | Notes                                                                   |
|------------|--------|-------------------------------------------------------------------------|
| `name`     | string | Unique within the format. Selectable via `--view=<name>`.               |
| `when`     | string | Predicate template; first matching view wins.                           |
| `default`  | bool   | If no `when` matches, the view marked default wins.                     |
| `template` | string | Go template rendered against the format context.                        |

View selection order: `--view=<name>` flag wins; else first view whose `when`
predicate is truthy; else first view with `default: true`; else first view.

### Template context

The view (and `when`) templates receive:

| Key       | Value                                                          |
|-----------|----------------------------------------------------------------|
| `.data`   | Parsed stdout. JSON: `map[string]any` / `[]any` / scalars (numbers normalized to int64/float64). Lines: `[]string`. Raw: trimmed string. |
| `.tty`    | `true` when stdout is a terminal (or when the user passes `--format=always`). |
| `.width`  | Terminal width (columns), or 80 fallback.                      |
| `.arg`, `.flag`, `.env`, `.var`, `.entry`, `.result` | Same data the leaf's `command` template sees. |

### Worked example

```json
{
  "name": "apicli",
  "command": "curl -fsSL https://jsonplaceholder.typicode.com{{.entry.path}}",
  "formats": {
    "user": {
      "input": "json",
      "when": "{{.tty}}",
      "views": [
        {
          "name": "table",
          "when": "{{ kindIs \"slice\" .data }}",
          "template": "{{ $rows := list \"ID\\tNAME\\tEMAIL\" }}{{ range .data }}{{ $rows = append $rows (printf \"%v\\t%v\\t%v\" .id .name .email) }}{{ end }}{{ tabwriter $rows }}"
        },
        {
          "name": "detail",
          "default": true,
          "template": "ID:    {{.data.id}}\nName:  {{.data.name}}\nEmail: {{.data.email}}\n"
        }
      ]
    }
  },
  "commands": [{
    "name": "users",
    "format": "user",
    "commands": [
      { "name": "get",  "args": [{"name":"id","type":"int","required":true}], "entry": {"path":"/users/{{.arg.id}}"} },
      { "name": "list", "entry": {"path":"/users"} }
    ]
  }]
}
```

### Inline format

For a one-off format that doesn't need to be reused, use the inline object
form on the command instead of a named reference:

```json
{
  "name": "ping",
  "command": "curl -fsSL https://example.test/health",
  "format": {
    "when": "true",
    "views": [
      { "name": "v", "default": true, "template": "{{.data.status}} ({{.data.uptime}}s)\n" }
    ]
  }
}
```

The same inheritance rules apply — a leaf's inline format overrides the
ancestor's named or inline format.

Behavior:

| Invocation | Output |
|---|---|
| `apicli users get 1` (interactive) | Detail view (object data; predicate doesn't match `slice`; default wins) |
| `apicli users list` (interactive) | Table view (slice data; first predicate matches) |
| `apicli users list \| jq .` | Raw JSON (not a TTY → `when` false) |
| `apicli users list --no-format` | Raw JSON (user veto) |
| `apicli users list --format=always \| less` | Table (user lies about TTY; author still says yes) |
| `apicli users get 1 --view=table` | Forces the table view |

### Streaming and large outputs

The format path captures the child's stdout into a 32 MiB buffer. If the
child's output exceeds that, the buffered prefix is streamed unmodified, the
remainder pipes straight through, and formatting is silently skipped. So
a `format`-equipped command never breaks for large outputs — it just falls
back to the streaming behavior you'd get without the format.

### Errors

If the leaf command exits non-zero while a format would have applied, the
captured body is written to stderr and the exit code propagates. Nothing is
written to stdout — the user sees the unmodified failure body.

## Template helpers

Every [sprig v3](https://masterminds.github.io/sprig/) helper is available —
that's where `toJson`, `upper`, `lower`, `trim`, `default`, `required`,
`b64enc`, `regexReplaceAll`, `hasKey`, etc. come from. On top of sprig we add:

| Helper        | Purpose                                                                                   |
|---------------|-------------------------------------------------------------------------------------------|
| `querystring` | Render a map as `?k=v&k=v` (URL-encoded). Empty values dropped. Empty map → empty string. |
| `repeatkey`   | Emit repeated query params for one key over a slice: `repeatkey "tag" .arg.tags` → `tag=a&tag=b` (URL-encoded, no leading `?`). Empty elements dropped. Works with `[]string`, `[]int`, `[]any`. |
| `shellquote`  | POSIX single-quote a value for safe interpolation into the string form of `command`.      |
| `urlpath`     | URL-escape a single path segment.                                                         |
| `spread`      | Splat a slice into multiple arguments. In argv-form commands, the element `"{{spread .arg.files}}"` becomes N argv entries (zero for an empty slice). In shell-form commands, each element is automatically shell-quoted. Works with `[]string`, `[]int`, `[]any`. |
| `fileExists`  | Returns true if the path exists and is a regular file. Useful inside `preconditions`.     |
| `dirExists`   | Returns true if the path exists and is a directory.                                       |
| `tabwriter`   | Format rows of tab-separated cells into aligned columns. Display-width aware (correct in the presence of ANSI escape codes and East Asian wide characters). Used by output formatters. |
| `padRight`    | `padRight 8 s` — pad `s` with spaces on the right to display width 8. Width-aware.        |
| `padLeft`     | `padLeft 8 s` — pad on the left to display width 8.                                       |
| `displayWidth`| Returns the visual column count of a string (ANSI escapes contribute 0; CJK wide runes contribute 2). |
| `stripANSI`   | Returns a string with all ANSI escape sequences removed.                                  |
| `filterSuffix`| Filter a `[]string` to elements ending with a suffix: `{{.rest \| filterSuffix ".ii"}}`.  |
| `filterPrefix`| Filter a `[]string` to elements starting with a prefix: `{{.rest \| filterPrefix "--"}}`. |

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

## Global flags

These persistent flags are registered on the root and inherited by every
subcommand:

| Flag              | Short | Default | Notes                                                                                     |
|-------------------|-------|---------|-------------------------------------------------------------------------------------------|
| `--config <path>` |       |         | Path to JSON config file. Falls back to `./api.json` if unset. See [Config discovery](#config-discovery). |
| `--mcp <transport>` |     |         | Run the loaded config as an MCP (Model Context Protocol) server instead of a CLI. Values: `stdio`, `http://<addr>`, `sse://<addr>`. Each leaf becomes an MCP tool. HTTP and SSE transports also expose `GET /health` → `{"status":"ok"}`. |
| `--cors <level>`  |       | `strict` | CORS policy for the MCP HTTP/SSE server. See [CORS levels](#cors-levels). Ignored for `--mcp=stdio`. |
| `--quiet`         | `-q`  | false   | Suppress the `N executions` line on stderr (printed when a leaf with `steps` runs more than one command). |
| `--yes`           | `-y`  | false   | Skip `confirm` prompts. Without this, a non-tty stdin combined with a non-empty `confirm` is a hard error. |
| `--verbose`       |       | false   | Show commands being executed, exit codes, and condition evaluation results on stderr. |
| `--debug`         |       | false   | Show full execution details on stderr: data context, captured stdout, format decisions. Implies `--verbose`. |
| `--no-format`     |       | false   | Disable output formatting. Equivalent to `--format=raw`.                                  |
| `--format <mode>` |       | `auto`  | `raw` (off), `auto` (default; format only on TTY), `always` (force `.tty=true` in predicates). See [Output formatting](#output-formatting). |
| `--view <name>`   |       |         | Pick a specific view by name, bypassing the format's predicate-based selection. Errors if the view is unknown. |

Two environment variables also affect formatting (lower precedence than flags):

| Variable          | Effect                                                                  |
|-------------------|-------------------------------------------------------------------------|
| `NO_FORMAT`       | Any non-empty value disables formatting (NO_COLOR-style).               |
| `API_CLI_FORMAT`  | `raw` / `auto` / `always` — same semantics as `--format`.               |

## CORS levels

When the MCP server runs over HTTP or SSE, `--cors <level>` controls
which browser origins may talk to it. The flag is irrelevant for
`--mcp=stdio` (no HTTP server, no browser). The default is `strict`.

| Level         | Origin allowlist                                                 | Preflight (OPTIONS)                            | When to use                                                  |
|---------------|------------------------------------------------------------------|------------------------------------------------|--------------------------------------------------------------|
| `disabled`    | Any origin (`Access-Control-Allow-Origin: *`).                   | Always 204; allows the requested method/headers.| Local prototyping. No protection — do not expose publicly.   |
| `permissive`  | `localhost`, `127.0.0.1`, `[::1]` (any port) plus same-origin.    | 204 if origin matches; 403 otherwise.          | Browser dev tools running locally hitting a remote server.   |
| `strict`      | Only same-origin (the server's bound `host:port`). When bound to `0.0.0.0`/`::`, any host with the matching port. | 204 if origin matches; 403 otherwise. | Default. Sensible for a single-tenant server with one frontend. |
| `enabled`     | Nothing — `Access-Control-Allow-Origin` is never sent.            | Always 403.                                    | Locked down. Only non-browser clients (curl, MCP SDKs) work. |

Aliases (case-insensitive): `disabled`/`off`/`none`/`open`,
`permissive`/`lax`/`loose`/`localhost`,
`strict`/`same-origin`/`sameorigin`,
`enabled`/`on`/`locked`/`lockdown`/`block`.

Notes:

- The wrapper only adds (or omits) response headers — it does not block
  the underlying request. With `strict` and a foreign origin, the MCP
  handler still runs; the browser refuses to expose the response because
  no `Access-Control-Allow-Origin` header is present.
- Requests with no `Origin` header (e.g. `curl`, server-to-server, AI
  tools) always pass through. CORS only matters for browsers.
- `Access-Control-Allow-Credentials` is not emitted at any level.
  Cookies and HTTP auth on cross-origin requests are not supported.

## Built-in subcommands

The binary includes a `docs` subcommand that prints embedded documentation
to stdout.  It works without a config file, so an LLM (or a human) can
query it before writing any JSON.

| Command                    | Output                                           |
|----------------------------|--------------------------------------------------|
| `api-cli docs`             | Full README (this file).                         |
| `api-cli docs schema`      | The JSON Schema for config files.                |
| `api-cli docs schema <key>`| A single definition or property from the schema. |
| `api-cli docs example`     | The minimal reference config (`api.example.json`).|

`docs schema <key>` looks up the key in the schema's `definitions` first,
then top-level `properties`.  If the key is not found, it prints the list
of available keys.

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

## GitHub example

[`github.example.json`](./github.example.json) wraps the read-only slice of
the GitHub REST API and is a more realistic showcase than the toy
jsonplaceholder demo. Highlights:

- **Subcommands**: `user get|repos|orgs`, `repo get|issues|issue|prs|pr|releases|release|commits|commit|branches|tags|contents|readme|languages|topics`, `org get|members|repos`, `search repos|code|issues|users`, `rate-limit`.
- **Token-aware**: picks up `$GITHUB_TOKEN` or `$GH_TOKEN` automatically (5000 req/hr authenticated vs. 60 req/hr without).
- **Enterprise-ready**: set `$GITHUB_API_URL` to target a GitHub Enterprise Server instance (defaults to `https://api.github.com`).
- **Noise stripping**: every response is piped through `jq` with a recursive `walk` that drops `*url` template links, GraphQL `node_id`s, empty `gravatar_id`s, `reactions` breakdowns, `permissions`, duplicate counts, and other metadata. On a typical repo response that's ~80% fewer bytes.
- **Format views**: each resource gets a `table` view (for list endpoints) and a `detail` view (for single-object endpoints), selected automatically by inspecting the parsed JSON shape.

### Quickstart

```sh
./build/api-cli --config github.example.json --help
./build/api-cli --config github.example.json user get octocat
./build/api-cli --config github.example.json repo get golang/go
./build/api-cli --config github.example.json repo issues cli/cli --state open -n 10
./build/api-cli --config github.example.json search repos 'language:go stars:>10000' --sort stars
./build/api-cli --config github.example.json rate-limit
```

To make it as ergonomic as `gh`, drop a wrapper on `$PATH`:

```bash
#!/bin/bash
# ~/.local/bin/ghr  (a tiny "gh-read" alias)
set -euo pipefail
api-cli --config ~/.config/ghr/github.example.json "$@"
```

Then `ghr repo get golang/go` works from anywhere. Override the endpoint or
token for a single invocation by setting env vars inline:

```sh
GITHUB_API_URL=https://ghes.example.com GITHUB_TOKEN=ghp_xxx ghr user get alice
```

### How response filtering works

The shared root command pipes every response through a `jq` filter that
strips noise recursively. Three categories of keys are removed:

1. **`*url` keys** — template links (`issues_url`, `commits_url`, ...),
   self-links (`url`, `html_url`), and avatar URLs. URL *values* in
   non-`url` keys (e.g. a user's `blog: "https://..."`) are preserved.
2. **API metadata** — `node_id` (GraphQL IDs), `gravatar_id` (always
   empty), `user_view_type`, `site_admin`, `author_association`,
   `permissions`, `custom_properties`, `temp_clone_token`, etc.
3. **Verbose objects** — `reactions` (8 emoji counters),
   `sub_issues_summary`, `issue_dependencies_summary`,
   `performed_via_github_app`.

Duplicate counts are also deduplicated: when both `forks` and
`forks_count` exist, the short name is dropped (same for `watchers`/
`watchers_count` and `open_issues`/`open_issues_count`).

The `search issues` command additionally exempts `repository_url` from the
filter (overrides `vars.filter` on that one subtree) because its table view
parses the repo name out of that field — a useful demonstration of how
`vars` cascade.

## Development

```sh
go-toolchain        # runs go mod tidy, tests, coverage, and build
```
