# api-cli

A declarative command-line alias system. You write an **XML** config that describes a tree of commands: subcommands, args, flags and your own variables. The tool builds a cobra command tree from it. Each leaf runs a command (shell or argv) or makes a first-class HTTP **request**. It then renders the result, optionally through the **fields** auto-formatter. Help at every level, shell tab completion and strong templating come for free.

It is a *hybrid* tool. HTTP requests are first-class (`<run><request>`, with no `curl` or `jq` subprocess). The general shell and argv execution engine stays, so non-HTTP aliases (git, tar, ...) work as well.

Example configs ship in this repo:

- [`api.example.xml`](./api.example.xml) -- a minimal demo against `jsonplaceholder.typicode.com`.
- [`samples/github/github.xml`](./samples/github/github.xml) -- a real, read-only wrapper for the GitHub REST API, with table and detail views. It trims the noise hard: `jq` drops each `*url` field, which cuts the response by 50-70%. See [GitHub example](#github-example) below.

## Install

```sh
go-toolchain     # runs tests + builds ./build/api-cli
```

Drop the binary on your `$PATH`.

## Recommended setup

`api-cli` is the engine. Each API or alias group you wrap gets its own thin wrapper script on your `$PATH`, and that script pins the config. You get a stable top-level command name, with help, completion and templating behind it, and you rebuild the binary for nothing.

1. Put the `api-cli` binary on your `$PATH`.
2. Save your config somewhere stable, e.g. `~/.config/myapi/api.xml`.
3. Create an executable shell script like `~/.local/bin/myapi`:

   ```bash
   #!/bin/bash
   set -euo pipefail
   api-cli --config ~/.config/myapi/api.xml "$@"
   ```

Now `myapi users get 1` works from anywhere. Repeat the steps per API, and keep several wrappers over one `api-cli` install.

## Quickstart

```sh
cp api.example.xml api.xml          # or pass --config <path>
./api-cli --help
./api-cli users get 1
./api-cli users list --limit 3
./api-cli posts 1                   # a runnable parent: `posts` lists, `posts 1` shows one
./api-cli posts search --contains sunt
```

## How it works

Every leaf renders its templates against a data context:

| Namespace  | Source                                                                       |
|------------|------------------------------------------------------------------------------|
| `.arg`     | Positional args by name, typed per the `<arg>` declarations.                 |
| `.flag`    | Named flags by name, typed per the `<flag>` declarations.                    |
| `.env`     | Process environment (`{{.env.API_TOKEN}}`).                                  |
| `.var`     | Merged `<vars>` from the root down to this node. Vars may reference one another. |
| `.result`  | Captured outputs of `<steps>`, keyed by step name. JSON outputs are structured. |
| `.entry`   | The leaf's `<entry>` (path/query/...), with string leaves templated first.   |
| `.rest`    | Passthrough leftovers (passthrough mode only).                               |
| `.run`     | This invocation. `.run.tmpdir` is a scratch directory a `<download>` leaf gets, removed when the run ends. |

A leaf runs what its closest `<run>` ancestor gives it, then presents the output. `<run>` comes in multiple forms.

- **A request** -- `<run><request>...</request></run>` makes an HTTP call. See [Requests](#requests).
- **A shell command** -- `<run>echo hi {{.arg.x}}</run>` runs through `/bin/sh -c`.
- **An argv list** -- `<run><argv>echo</argv><argv>{{.arg.x}}</argv></run>` execs directly, with no shell. That is the safe form, because no quoting applies.

`<run>` inherits down the tree. The closest ancestor that declares one wins, and a node overrides it for its own subtree. A request clears an inherited command, and a command clears an inherited request.

## Placeholders

Element content can mix plain text with three placeholder elements that compile to Go `text/template` source. You can always drop to a raw template with `expr=`, or type `{{ ... }}` in the text.

| Placeholder | Compiles to | Notes |
|-------------|-------------|-------|
| `<value name="var.x"/>` | `{{ .var.x }}` | `name=` is a dotted context path. |
| `<value name="x" default="-"/>` | `{{ .x | default "-" }}` | Fallback for an empty value. |
| `<value name="x" as="urlpath"/>` | `{{ urlpath .x }}` | Wrap with any template helper. |
| `<value expr="{{ or .a .b }}"/>` | `{{ or .a .b }}` | Verbatim template escape hatch. |
| `<if test="var.token">A<else/>B</if>` | `{{ if truthy .var.token }}A{{ else }}B{{ end }}` | `test=` is a context path. It is truthy unless it is empty, `false`, `0` or `no`. |
| `<if test="arg.tag" eq="latest">...</if>` | `{{ if eq (printf "%v" .arg.tag) "latest" }}...{{ end }}` | `eq=` compares to a literal. |
| `<for each="items">...</for>` | `{{ range .items }}...{{ end }}` | Iterate; `.` rebinds to each element (`<value name="field"/>` reads the element). |

```xml
<path><if test="arg.username">/users/<value name="arg.username" as="urlpath"/><else/>/user</if></path>
```

## Requests

[docs/requests.md](docs/requests.md) holds this section.

## Transports

Some APIs authenticate in a way that is painful to reproduce: a signing scheme, a session dance, an mTLS setup. You usually have a program that already does it. A `<transport>` is that program, wired in as the thing that *performs* requests. Everything else stays the same: the `<request>` syntax, `<response jq=>`, `<fields>`, steps and MCP.

```xml
<transports>
	<transport name="corp" default="true">
		<run>
			<argv>corp-http</argv>
			<argv><value name="request.method"/></argv>
			<argv><value name="request.url"/></argv>
		</run>
	</transport>
</transports>
```

The program gets the fully rendered request on top of the leaf's usual context. Its **stdout is the response body**.

| Placeholder | Value |
|-------------|-------|
| `.request.method` | `GET`, `POST`, ... |
| `.request.url` | Full URL, query string included. |
| `.request.body` | Rendered body (empty when there is none). |
| `.request.headers` | Map of rendered header name -> value. |
| `.request.header_lines` | `["Accept: application/json", ...]`, for splatting. |

- **The body goes to the program's stdin** unless the transport declares its own `<stdin>`. Stdin is explicit either way. A transport never inherits your terminal, so a program that reads stdin cannot hang and wait for one.
- **A non-zero exit fails the request**, as a 4xx from the built-in client does. The program's stderr passes through untouched. The error also shows the start of its stdout, because that is frequently the error page.
- **Which transport runs**: the request's `transport=` attribute, then the registry's `default="true"` entry, then the built-in client. There is no override at run time. How a request reaches its endpoint is a property of that endpoint, not a user preference. The name `http` belongs to the built-in client, so `transport="http"` on one request opts that request out of a default transport. That is the public endpoint in an otherwise internal API.
- `<cwd>` sets the program's working directory. A transport's `<run>` must be a command. It is the thing that performs a request. It cannot be one.

To build repeated flags, assemble a list and `spread` it.

```xml
<argv><value expr="{{ $h := list }}{{ range .request.header_lines }}{{ $h = concat $h (list &quot;-H&quot; .) }}{{ end }}{{ spread $h }}"/></argv>
```

An empty `spread` contributes no argument at all. That makes it the way to write a conditional argument. `{{ spread (ternary (list "--data-binary" "@-") (list) (ne .request.body "")) }}` adds two arguments only when a body exists. A plain `<if>` leaves an empty string in the argv instead. See the `curl` transport in [`api.example.xml`](./api.example.xml) for both together.

## Downloads

[docs/downloads.md](docs/downloads.md) holds this section.

## Streaming: `<stream>`

[docs/streaming-stream.md](docs/streaming-stream.md) holds this section.

## Output: fields

[docs/output-fields.md](docs/output-fields.md) holds this section.

## Watch

`--watch <interval>` re-runs the command on an interval. It repaints the output in place, like `watch(1)`. The value is a duration (`2s`, `500ms`) or a plain number of seconds (`2`). The floor is 100ms.

```text
$ ghr repo releases golang/go --watch 30s
every 30s: ghr repo releases golang/go    13:45:07

TAG        PUBLISHED     DOWNLOADS
go1.25     Sep 2, 2026   184213
go1.25rc1  Jun 10, 2026   12044
```

A frame is one whole run of the leaf: the steps, the entry, the request and the formatter. Nothing is cached between frames, so a `<var>`, a step result and the response are all fresh each time. The frame keeps the real terminal size. A `<fields>` table therefore stays a table under a watch, rather than falling back to the piped representation.

The output of the leaf and its diagnostics both land in the frame. A failed run reports the failure in place. The watch then continues. Ctrl-C ends the watch, leaves the last frame on screen and exits 130. A frame taller than the terminal is clipped. The last row then says how many lines it dropped.

Redirected output gets no repainting: the frames append, which makes `--watch 5s ... > log` a poll log.

Leaves refuse to repeat. A `<download>` leaf transfers a file one time. `--watch` on it is an error. A leaf with a `confirm` prompt needs `--yes`, because the prompt draws into the frame where nobody can answer it.

A config can make the repeat the default. `watch="5s"` on a `<command>` says the node runs on that interval unless the flag says otherwise. It takes the same values as the flag. The loader holds it to the same floor. It inherits like `confirm=`, so one attribute on a group covers every screen under it.

```xml
<command name="board" watch="5s">
	<command name="builds">...</command>
	<command name="queue" watch="500ms">...</command>
</command>
```

`--watch <every>` overrides the config's value for one invocation. `--watch off` (or `--watch 0`) runs the leaf one time. A `<download>` leaf cannot declare `watch=`. The loader says so. A `<download>` leaf under a group that declares one runs one time, with a warning on stderr that names the interval it ignored. Over MCP a tool call is one run, and the server ignores the attribute.

## Screens: `<tml>`

[docs/screens-tml.md](docs/screens-tml.md) holds this section.

## Examples

### Wrap a REST API

```xml
<config name="apicli">
	<vars>
		<var name="base_url">https://api.example.com/v1</var>
	</vars>
	<!-- Inherited by every leaf unless overridden. -->
	<run>
		<request method="GET">
			<url><value name="var.base_url"/><value name="entry.path"/></url>
			<query from="entry.query"/>
			<header name="Authorization">Bearer <value name="env.API_TOKEN"/></header>
		</request>
	</run>
	<command name="users">
		<command name="get">
			<arg name="id" type="int" required="true"/>
			<fields>
				<field name="id">id</field>
				<field name="name">name</field>
			</fields>
			<entry><path>/users/<value name="arg.id"/></path></entry>
		</command>
		<command name="create">
			<flag name="name" required="true"/>
			<run>
				<request method="POST">
					<url><value name="var.base_url"/>/users</url>
					<header name="Content-Type">application/json</header>
					<body>{"name":{{ .flag.name | toJson }}}</body>
				</request>
			</run>
		</command>
	</command>
</config>
```

### Generic aliases (non-HTTP)

The engine does not care whether a command is HTTP. Here is a small git wrapper.

```xml
<config name="gx">
	<vars><var name="prefix">feature/</var></vars>
	<command name="start">
		<arg name="name" required="true"/>
		<run><argv>git</argv><argv>checkout</argv><argv>-b</argv><argv><value name="var.prefix"/><value name="arg.name"/></argv></run>
	</command>
	<command name="push">
		<arg name="name" required="true"/>
		<run><argv>git</argv><argv>push</argv><argv>-u</argv><argv>origin</argv><argv><value name="var.prefix"/><value name="arg.name"/></argv></run>
	</command>
</config>
```

### Tar wrapper (variadic args, spread, preconditions, dynamic default)

```xml
<config name="tar-safe">
	<command name="create">
		<arg name="archive" required="true"/>
		<arg name="files" variadic="true" required="true" description="Files/dirs to include."/>
		<preconditions>
			<precondition>{{if fileExists .arg.archive}}{{.arg.archive}} already exists{{end}}</precondition>
		</preconditions>
		<!-- argv form: no shell. `spread` splats the slice into N argv slots. -->
		<run><argv>tar</argv><argv>-czf</argv><argv><value name="arg.archive"/></argv><argv>{{spread .arg.files}}</argv></run>
	</command>
	<command name="extract">
		<arg name="archive" required="true"/>
		<!-- Templated default: foo.tar.gz -> foo. -->
		<flag name="to" default='{{trimSuffix ".tar.gz" .arg.archive}}'/>
		<run><argv>tar</argv><argv>-xzf</argv><argv><value name="arg.archive"/></argv><argv>-C</argv><argv><value name="flag.to"/></argv></run>
	</command>
</config>
```

```sh
./tar-safe create out.tar.gz src/ README.md  # variadic positional args
./tar-safe extract out.tar.gz                 # --to defaults to "out"
```

## A parent that also runs

[docs/a-parent-that-also-runs.md](docs/a-parent-that-also-runs.md) holds this section.

## Passthrough mode

A leaf that sets `passthrough="true"` accepts arbitrary positional args, which is everything after `--` in the wrapper script. It then does its own minimal flag extraction.

1. The parser recognizes a declared `<flag>` only, by name or by `short=`, with one or leading dashes. Both `-o` and `--o` match.
2. It collects everything else into `.rest`, a `[]string`. That covers an unknown flag, its value, and a bare positional.
3. An extracted flag does NOT appear in `.rest`, so `{{spread .rest}}` rebuilds the original command line minus the captured flags.
4. A bare `--`, and everything after it, goes into `.rest` verbatim. The wrapped command can mean something by it.

```xml
<config name="cicc-cache">
	<command name="exec" passthrough="true">
		<flag name="o" type="string"/>
		<flag name="gen_c_file_name" type="string"/>
		<steps>
			<step name="hash"><run>md5sum {{.rest | filterSuffix ".cpp1.ii" | first}} | cut -d' ' -f1</run></step>
		</steps>
		<run>/usr/local/cuda/nvvm/bin/cicc.real {{spread .rest}}</run>
	</command>
</config>
```

```sh
# Wrapper script: exec api-cli --config cicc-cache.xml exec -- "$@"
```

**Constraints:** `passthrough` and `<arg>` are mutually exclusive, and `passthrough` sits on a leaf only. A flag takes `=` syntax and next-arg syntax. A `bool` flag consumes no value, and a `string-slice` flag accumulates. Filter `.rest` with `filterSuffix` and `filterPrefix`.

## Mock executables

[docs/mock-executables.md](docs/mock-executables.md) holds this section.

## Result reuse across calls (steps)

[docs/result-reuse-across-calls-steps.md](docs/result-reuse-across-calls-steps.md) holds this section.

## Legacy formats and views

The older explicit `<format>` and `<view>` system stays next to `<fields>`, for a case that needs full control of the rendered template. PowerShell's `.format.ps1xml` is its ancestor. A leaf uses `<fields>` *or* `<format>`, not both.

```xml
<formats>
	<format name="user" input="json" when="{{.tty}}">
		<view name="table" when='{{ kindIs "slice" .data }}'>{{ range .data }}{{.id}}	{{.name}}
{{ end }}</view>
		<view name="detail" default="true">ID: {{.data.id}}
Name: {{.data.name}}
</view>
	</format>
</formats>
<command name="users">
	<format ref="user"/>
	...
</command>
```

`input=` is `json` (the default), `lines` or `raw`. Formatting applies only when the author `when` predicate AND the user verdict agree. `--view=<name>` forces a view. An inline `<format>`, with `<view>` children and no `ref=`, overrides an inherited one. See [Global flags](#global-flags) for `--format` and `--no-format`.

**An omitted `when=` means `{{.tty}}`.** A redirect, a pipe and the MCP server are not a terminal. Such a format prints the raw body, and everything the view added is gone. An agent that reads the output sees the unshaped response. Write `when="true"` for a view that must render everywhere, or use `<fields>`, which renders with no terminal and carries `--as` for the sink. `--format=always` forces the terminal answer for one call.

`.data` differs between the two systems, and the difference bites on a body with a `data` key. In a `<view>`, `.data` is the whole parsed body, so a JSON:API response reads `.data.data` for the resource and `.data.included` for the sideloaded records. In a `<field expr=>`, the record is `.` and its keys are promoted. The same resource therefore reads `{{.id}}`. The rest of the context stays on `$` (`$.data.included`, `$.var`, `$.arg`). A body with no `data` key follows the same rule. A view reads `.data.errors`. A field reads `errors` or `{{.errors}}`.

```xml
<!-- JSON:API: {"data":{"id":"1","attributes":{"title":"a"}},"included":[{"type":"tag","id":"9"}]} -->
<fields over="data">                                   <!-- the resource, not the body -->
	<field name="id">id</field>
	<field name="title">attributes.title</field>
	<field name="tags" expr="{{ len $.data.included }}"/>  <!-- $ is the whole context -->
</fields>
<fields over="data.included"><field name="tag">id</field></fields>
```

## Template helpers

Every [sprig v3](https://masterminds.github.io/sprig/) helper is available: `toJson`, `upper`, `default`, `required`, `regexReplaceAll` and the rest. On top of sprig you get these.

| Helper        | Purpose                                                                                   |
|---------------|-------------------------------------------------------------------------------------------|
| `truthy`      | The truthiness that `<if test=>` uses. nil, `false`, `0`, `no` and "" are falsy.           |
| `querystring` | Render a map as `?k=v&k=v`, URL-encoded. It drops an empty value.                          |
| `repeatkey`   | Repeated params for one key over a slice: `repeatkey "tag" .arg.tags`.                     |
| `shellquote`  | POSIX single-quote a value for the shell form of a command.                               |
| `urlpath`     | URL-escape a single path segment.                                                         |
| `spread`      | Splat a slice into multiple argv slots (or shell-quoted words). Works with `[]string`/`[]int`/`[]any`. |
| `fileExists` / `dirExists` | Path predicates, useful in `<preconditions>`.                              |
| `segmentPattern` | The built-in path-segment regular expression, for an `<arg pattern=>`.                 |
| `safeSegments` | The same rule as a predicate over one or more values. An absent value passes.             |
| `tabwriter`   | Align rows of tab-separated cells (display-width aware).                                  |
| `padRight` / `padLeft` / `displayWidth` / `stripANSI` | Width-aware string helpers.                  |
| `filterSuffix` / `filterPrefix` | Filter a `[]string` (used with `.rest`).                            |
| `stem`        | A path without its directory and without its extension: `stem "src/foo/bar.cpp"` is `bar`. |
| `collect`     | Gather one dotted path out of every element of a list, flattening the values that are lists: `collect "result.parts" .result.listings`. A path missing from an element is an error. |

## Template semantics

- The parser uses `missingkey=zero`, so a missing map key raises no error. Every context map holds `any`, whose zero value is nil, so a missing key **prints `<no value>`** rather than an empty string. Reach for `{{default "" .x}}`, `{{if .x}}...{{end}}` or `{{required "msg" .x}}` when that matters. A declared `<flag>` is always present, default included, so `<no value>` in practice means the path is wrong.
- `<entry>` renders first, without `.entry` in scope. Every string leaf renders on its own, and the result becomes `.entry`.
- `<vars>` resolve to a fixpoint, so one var can reference another. `var.filter` can interpolate `var.noise`.
- A var can read `.flag`. A `<flag default=>` can read `.var`. Vars therefore resolve twice per invocation. The first pass runs without flags, and a templated flag default renders against that context. The second pass runs over the finished flag map. Everything downstream -- a URL, an entry, a jq program -- sees that second pass, which carries this run's flags.

## Working directory and stdin

`<cwd>` and `<stdin>` can sit at the top level, on any `<command>`, and on any `<step>`. Both are templates, and both inherit down the tree. The closest non-empty ancestor wins, and a step overrides its leaf. `<cwd>` sets the child's working directory, and defaults to the caller's own. `<stdin>` puts a rendered string on the child's stdin, and defaults to the parent's stdin. Both apply to a shell command and to an argv command.

```xml
<config name="stack">
	<cwd><value name="env.STACKS_ROOT"/></cwd>
	<command name="up"><run><argv>docker</argv><argv>compose</argv><argv>up</argv></run></command>
</config>
```

## Config format

A config is **XML 1.1**: `<?xml version="1.1" encoding="UTF-8"?>`. Structural indentation is **tabs**. The loader removes the common leading tabs from multi-line text content. Use CDATA (`<![CDATA[ ... ]]>`) for content that holds `<` or `&`, or a foreign language such as a jq program.

```xml
<?xml version="1.1" encoding="UTF-8"?>
<config name="apicli" schema="https://raw.githubusercontent.com/wow-look-at-my/api-cli-spec/master/api-cli.xsd">
	<vars>
		<var name="base_url">https://api.example.com/v1</var>
		<var name="filter"><![CDATA[walk(if type=="object" then with_entries(select(.key|endswith("url")|not)) else . end)]]></var>
	</vars>
	...
</config>
```

An attribute value is always raw, a template or a context path. A Go template needs double quotes for `eq .x "y"`, so put `'single quotes'` around such an attribute, or escape the inner quotes. The `schema=` attribute is an editor hint that points at the XSD. The loader ignores it.

## Limits and workarounds

[docs/limits-and-workarounds.md](docs/limits-and-workarounds.md) holds this section.

## Config schema

[docs/config-schema.md](docs/config-schema.md) holds this section.

## Global flags

| Flag              | Short | Default | Notes |
|-------------------|-------|---------|-------|
| `--config <path>` |       |         | Config file (XML). Falls back to `./api.xml`. |
| `--version`       |       |         | Print the binary's version. Needs no config. |
| `--mcp <transport>` |     |         | Run the config as an MCP server: `stdio`, `http://<addr>`, `sse://<addr>`. Each leaf becomes a tool named for its command path, with underscores (`users_get`). The HTTP and SSE servers also answer `GET /health`. The server behaves as `--format=always` does, with `.tty` true and width 80. A tool's `inputSchema` carries each arg's `pattern=`, and a call is checked against it. Over stdio, a client that closes stdin right after its last request still gets that request's answer. |
| `--cors <level>`  |       | `strict`| CORS for the MCP HTTP/SSE server. See [CORS levels](#cors-levels). |
| `--install-mocks <dir>` | |        | Write one wrapper script per `<mock>` leaf into the directory, then exit. See [Mock executables](#mock-executables). |
| `--quiet`         | `-q`  | false   | Suppress the `N executions` line. |
| `--yes`           | `-y`  | false   | Skip `confirm` prompts. |
| `--verbose`       |       | false   | Show executed commands/requests, exit codes, conditions on stderr. |
| `--debug`         |       | false   | Full execution detail (implies `--verbose`). |
| `--no-format`     |       | false   | Disable output formatting (= `--format=raw`). |
| `--format <mode>` |       | `auto`  | `raw` / `auto` / `always`. |
| `--as <sink>`     |       |         | Force a `<fields>` representation: `table|list|lines|raw|json|markdown|csv|timeline`. |
| `--view <name>`   |       |         | Pick a named legacy view, bypassing predicate selection. |
| `--watch <every>` |       |         | Re-run on an interval and repaint in place: `2s`, `500ms`, or seconds (`2`). Overrides the config's `watch=`, and `off` runs once. See [Watch](#watch). |
| `--var KEY=VALUE` |       |         | Set an env var before evaluation (so `{{.env.KEY}}` sees it). Repeatable. |
| `--concurrency <n>` |     | `4`     | Parallel downloads. See [Downloads](#downloads). |
| `--download-dir <path>` | | `.`     | Base directory for `<download>` destinations. |
| `--no-tui`        |       | false   | Report download progress as plain lines instead of drawing the display. |

Env vars apply, at a lower precedence than the flags. Any value of `NO_FORMAT` turns formatting off. `API_CLI_FORMAT` takes `raw`, `auto` or `always`.

## CORS levels

With the MCP server on HTTP or SSE, `--cors <level>` controls which browser origins can talk to it. The level means nothing for `--mcp=stdio`.

| Level         | Origins allowed | When to use |
|---------------|-----------------|-------------|
| `disabled`    | Any (`*`).      | Local prototyping only. |
| `permissive`  | localhost/loopback + same-origin. | Local browser dev tools hitting a remote server. |
| `strict`      | Same-origin only (default). | Single-tenant server with one frontend. |
| `enabled`     | None. The header never goes out. | Locked down, for non-browser clients only. |

A request with no `Origin` header always passes through. That covers curl, a server-to-server call and an AI tool, because CORS matters to a browser only. The server never sends `Access-Control-Allow-Credentials`.

## Built-in subcommands

The `docs` subcommand prints the embedded documentation. It works with no config.

| Command               | Output |
|-----------------------|--------|
| `api-cli docs`        | Full README (this file). |
| `api-cli docs schema` | The XSD schema for config files. |
| `api-cli docs example`| The reference config (`api.example.xml`). |

## Config discovery

First hit wins:

1. `--config <path>` anywhere on the command line (`--config=x` or `--config x`).
2. `./api.xml` in the current working directory.

## Shell completion

```sh
source <(./api-cli completion bash)
./api-cli completion zsh  > "${fpath[1]}/_api-cli"
./api-cli completion fish > ~/.config/fish/completions/api-cli.fish
```

## Exit codes

| Code | Meaning |
|------|---------|
| 0    | Success. |
| 1    | Render error, cobra usage error, request error, failed download, or empty command. |
| 2    | Config not found or invalid. |
| 127  | Child binary not found or failed to start. |
| N    | Any other value is the child's exit code. |

## GitHub example

[`samples/github/github.xml`](./samples/github/github.xml) wraps the read-only part of the GitHub REST API in first-class requests.

- **Subcommands**: `user get|repos|orgs`, `repo get|issues|issue|prs|pr|pr-diff|pr-comments|releases|release|commits|commit|branches|tags|contents|readme|languages|topics`, `org get|members|repos`, `search repos|code|issues|users`, `rate-limit`.
- **Token-aware**: picks up `$GITHUB_TOKEN` / `$GH_TOKEN` automatically (5000 vs 60 req/hr).
- **Enterprise-ready**: set `$GITHUB_API_URL` for GitHub Enterprise Server.
- **Noise stripping**: each response goes through an embedded `jq` `walk`. It drops `*url` links, `node_id`s, `reactions`, `permissions`, duplicate counts and more. That frequently cuts about 80% of the bytes. `GITHUB_RAW=1` opts out.
- **Fields views**: list endpoints become tables, single objects become `Label: value` lists, automatically by data shape.

```sh
mkdir -p ~/.config/ghr && cp samples/github/github.xml ~/.config/ghr/github.xml
# ~/.local/bin/ghr:  exec api-cli --config ~/.config/ghr/github.xml "$@"
ghr user get octocat
ghr repo issues cli/cli --state open -n 10
ghr search repos 'language:go stars:>10000' --sort stars
ghr repo languages golang/go --as=json
```

## Cloudflare Workers

A TypeScript port lives in `workers/` and serves the same configs as HTTP endpoints on Cloudflare Workers. Instead of shelling out to `curl`, it parses rendered command templates and executes them via `fetch()`.

```sh
cd workers
npm install
npm test        # 242 tests (Workers pool + comparative)
npm run dev     # local dev server
```

CLI invocations map to HTTP requests:

| CLI                              | HTTP                          |
|----------------------------------|-------------------------------|
| `api-cli users get 1`            | `GET /users/get/1`            |
| `api-cli users list --limit 3`   | `GET /users/list?limit=3`     |
| `api-cli user-posts Bret`        | `GET /user-posts/Bret`        |

See `workers/README.md` for the full API mapping, deployment, and architecture details.

## Using the formatter as a library

The `<fields>` formatter is importable on its own, at `github.com/wow-look-at-my/api-cli/fields`. A program with decoded JSON gets the same table, list, JSON, Markdown, CSV, or timeline output, without the XML config language.

```go
f := &fields.Fields{Over: "items", List: []fields.Field{
	{Name: "id", Path: "id"},
	{Name: "name", Path: "name"},
}}
out, err := fields.Render(nil, f, body, map[string]any{"data": body}, "", 0)
```

The first argument evaluates `expr=` and `footer=` templates. It may be nil for a declaration that uses neither. See `docs/fields-package.md`.
## Development

```sh
go-toolchain        # runs go mod tidy, vet, tests, coverage, and build
```
