# api-cli

A declarative command-line alias system. You write an **XML** config describing
a tree of commands (subcommands, args, flags, user-defined variables); the tool
builds a cobra command tree from it. Each leaf either runs a command (shell or
argv) or performs a first-class HTTP **request**, then renders the result --
optionally through the **fields** auto-formatter. Help at every level, shell tab
completion, and strong templating come for free.

It is a *hybrid* tool. HTTP requests are first-class (`<run><request>`, no
`curl`/`jq` subprocess needed), but the general shell/argv execution engine is
fully retained, so non-HTTP aliases (git, tar, ...) work just as well.

Two example configs ship in this repo:

- [`api.example.xml`](./api.example.xml) -- a minimal demo against
  `jsonplaceholder.typicode.com`.
- [`samples/github/github.xml`](./samples/github/github.xml) -- a real,
  read-only wrapper for the GitHub REST API with table/detail views and
  aggressive noise-trimming (drops every `*url` field with `jq`, cutting
  response size by 50-70%). See [GitHub example](#github-example) below.

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

1. Put the `api-cli` binary on your `$PATH`.
2. Save your config somewhere stable, e.g. `~/.config/myapi/api.xml`.
3. Create an executable shell script like `~/.local/bin/myapi`:

   ```bash
   #!/bin/bash
   set -euo pipefail
   api-cli --config ~/.config/myapi/api.xml "$@"
   ```

Now `myapi users get 1` works from anywhere. Repeat per API to maintain
several wrappers from one `api-cli` install.

## Quickstart

```sh
cp api.example.xml api.xml          # or pass --config <path>
./api-cli --help
./api-cli users get 1
./api-cli users list --limit 3
./api-cli posts get 1
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

A leaf runs whatever its closest `<run>` ancestor defines, then presents the
output. `<run>` comes in three forms:

- **A request** -- `<run><request>...</request></run>` performs an HTTP call (see
  [Requests](#requests)).
- **A shell command** -- `<run>echo hi {{.arg.x}}</run>` runs via `/bin/sh -c`.
- **An argv list** -- `<run><argv>echo</argv><argv>{{.arg.x}}</argv></run>` execs
  directly with no shell (safe; no quoting concerns).

`<run>` inherits down the tree: the closest ancestor that defines one wins, and
a node overrides it for its subtree. Defining a request clears an inherited
command and vice versa.

## Placeholders

Element content can interleave plain text with three placeholder elements that
compile to Go `text/template` source. You can always drop to a raw template
with `expr=` (or just type `{{ ... }}` in the text).

| Placeholder | Compiles to | Notes |
|-------------|-------------|-------|
| `<value name="var.x"/>` | `{{ .var.x }}` | `name=` is a dotted context path. |
| `<value name="x" default="-"/>` | `{{ .x | default "-" }}` | Fallback for an empty value. |
| `<value name="x" as="urlpath"/>` | `{{ urlpath .x }}` | Wrap with any template helper. |
| `<value expr="{{ or .a .b }}"/>` | `{{ or .a .b }}` | Verbatim template escape hatch. |
| `<if test="var.token">A<else/>B</if>` | `{{ if truthy .var.token }}A{{ else }}B{{ end }}` | `test=` is a context path; truthy unless empty/`false`/`0`/`no`. |
| `<if test="arg.tag" eq="latest">...</if>` | `{{ if eq (printf "%v" .arg.tag) "latest" }}...{{ end }}` | `eq=` compares to a literal. |
| `<for each="items">...</for>` | `{{ range .items }}...{{ end }}` | Iterate; `.` rebinds to each element (`<value name="field"/>` reads the element). |

```xml
<path><if test="arg.username">/users/<value name="arg.username" as="urlpath"/><else/>/user</if></path>
```

## Requests

A `<request>` is built entirely from templates and executed with the Go HTTP
client -- no `curl`. JSON responses can be shaped with an embedded `jq` engine
([gojq](https://github.com/itchyny/gojq)); no `jq` binary is required. To send
requests through your own program instead, see [Transports](#transports).

```xml
<run>
	<request method="GET">
		<url><value name="var.base_url"/><value name="entry.path"/></url>
		<query from="entry.query"/>
		<header name="Accept">application/json</header>
		<if test="var.token"><header name="Authorization">Bearer <value name="var.token"/></header></if>
		<response jq="var.filter"/>
	</request>
</run>
```

| Element | Notes |
|---------|-------|
| `method=` | HTTP method (template). Defaults to `GET`. |
| `<url>` | The request URL (template). |
| `<query from="path">` | Pull a map of params from a context path (e.g. `entry.query`). |
| `<query><param name="k">v</param></query>` | Explicit params. Empty values are dropped. An enclosing `<if test=>` gates the params it wraps. |
| `<header name="H">v</header>` | A header (value is a template). An enclosing `<if test=>` gates the headers it wraps. |
| `<body>` | Request body (template); omit for no body. |
| `<response jq="path"/>` | Shape the JSON body with the jq program at that context path. Omit `<response>` to return the raw body verbatim (diffs, READMEs, ...). |

A non-2xx/3xx status prints the body to stderr and exits non-zero (like
`curl -f`). The root `<run>` is typically the shared request; per-leaf `<run>`
overrides it (e.g. a `POST`, or a raw-body download).

## Transports

Some APIs authenticate in a way that is painful to reproduce -- a signing
scheme, a session dance, an mTLS setup -- and you already have a program that
does it. A `<transport>` is that program, wired in as the thing that *performs*
requests. Everything else stays: the same `<request>` syntax, `<response jq=>`,
`<fields>`, steps, MCP.

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

The program receives the fully rendered request on top of the leaf's usual
context, and its **stdout is the response body**:

| Placeholder | Value |
|-------------|-------|
| `.request.method` | `GET`, `POST`, ... |
| `.request.url` | Full URL, query string included. |
| `.request.body` | Rendered body (empty when there is none). |
| `.request.headers` | Map of rendered header name -> value. |
| `.request.header_lines` | `["Accept: application/json", ...]`, for splatting. |

- **The body goes to the program's stdin** unless the transport declares its own
  `<stdin>`. Either way stdin is explicit -- a transport never inherits your
  terminal, so a program that reads stdin cannot hang waiting for one.
- **A non-zero exit fails the request**, like a 4xx from the built-in client.
  The program's stderr passes through untouched.
- **Which transport runs**: the request's `transport=` attribute, else the
  registry's `default="true"` entry, else the built-in client. There is no
  runtime override -- how a request reaches its endpoint is a property of that
  endpoint, not a user preference. The name `http` is reserved for the built-in
  client, so `transport="http"` on one request opts it out of a default
  transport (the public endpoint in an otherwise internal API).
- `<cwd>` sets the program's working directory. A transport's `<run>` must be a
  command -- it is what performs a request, so it cannot be one.

To build repeated flags, assemble a list and `spread` it:

```xml
<argv><value expr="{{ $h := list }}{{ range .request.header_lines }}{{ $h = concat $h (list &quot;-H&quot; .) }}{{ end }}{{ spread $h }}"/></argv>
```

An empty `spread` contributes no argument at all, so it is also how you make an
argument conditional -- `{{ spread (ternary (list "--data-binary" "@-") (list) (ne .request.body "")) }}`
adds two arguments only when there is a body. A plain `<if>` would leave an
empty string in the argv instead. See the `curl` transport in
[`api.example.xml`](./api.example.xml) for both together.

## Downloads

A step can work a URL out -- parse it from a listing, sign it, follow a redirect
-- and then hand it to a shared downloader instead of printing it. One queue
serves the whole run, fetching four at a time by default and carrying whatever
auth the steps established.

```xml
<downloads concurrency="8" dir="./out"/>
...
<command name="grab" description="Fetch every asset in the release.">
	<steps>
		<step name="release">
			<run><request><url><value name="var.api"/>/releases/latest</url></request></run>
		</step>
	</steps>
	<download over="result.release.assets">
		<url><value name="browser_download_url"/></url>
		<to><value name="name"/></to>
		<hash algo="sha256"><value name="digest"/></hash>
		<header name="Authorization">Bearer <value name="var.token"/></header>
		<cookie name="session"><value name="result.login.sid"/></cookie>
	</download>
</command>
```

On a terminal this draws a live display: a line per transfer with percentage,
sizes, rate and ETA, an aggregate `TOTAL` row, and a height-capped,
self-scrolling log region carrying the steps' and the downloader's output.

```
downloads: 3 active, 3 queued, 0 done
  ubuntu-25.04.iso         59%     3.6 MiB / 6.0 MiB      870.1 KiB/s  ETA 00:02
  linux-6.18.tar.xz        55%     1.7 MiB / 3.0 MiB      406.0 KiB/s  ETA 00:03
  dataset-a.parquet        69%     5.5 MiB / 8.0 MiB      1.3 MiB/s    ETA 00:01
  TOTAL                    63%    10.8 MiB / 17.0 MiB+    2.6 MiB/s
------------------------------------------------------------
 downloading dataset-a.parquet
 downloaded ubuntu-25.04.iso (6.0 MiB)
```

Piped, there is no display: progress stays line-based on stderr and the
destination paths go to stdout, one per line, for whatever reads them next.

- **`<download>` is the leaf's action.** It runs after the steps and stands in
  for the leaf's `<run>`, so an inherited request does not fire on the way.
- **`over=`** repeats the declaration per record of a list, with the record's
  keys promoted (`<value name="name"/>`) and the record itself at `.item`. An
  empty list downloads nothing; a path that resolves to nothing is an error.
- **`<url>` may render several lines** -- what a `<for>` loop produces -- and
  each line becomes its own download.
- **`<to>`** is a file path, or a directory when it ends in `/`, names an
  existing one, or serves several URLs. Empty means the download directory,
  named by the URL or the response's `Content-Disposition`.
- **A relative `<to>` resolves under the download directory** (`dir=`, or
  `--download-dir`); an absolute one is taken as-is.
- **Auth rides along**: `<header>` and `<cookie>` render against the same
  context, take `<if test=>`, and cookies fold into one `Cookie` header.
- **Failures are loud.** A 4xx is the answer; a 5xx or a network fault retries
  at a fixed one-second cadence. Anything still failing names its URL on stderr
  and exits non-zero, while the other files carry on.
- Bytes land in a `.part` sibling first, so an interrupted transfer never leaves
  a truncated file wearing the real name.
- **No resume.** Every attempt starts at byte zero, and a leftover `.part` is
  overwritten rather than continued.

### Downloading through a transport

A `<download>` reaches its URL the same way a `<request>` does: over the
built-in client, or over a [transport](#transports) program when the endpoint
needs one.

```xml
<download transport="corp">…</download>
```

- **Selection matches requests**: the `transport=` attribute, else the
  registry's `default="true"` entry, else the built-in client. So a config whose
  endpoints all need the program needs it for its files too, with nothing extra
  to say. `transport="http"` opts one download back to the built-in client.
- **The program is handed the same `.request` context** — `method`, `url`,
  `headers`, `header_lines` — so one program serves requests and downloads
  alike. `method` is `GET` and there is no body.
- **Its stdout is streamed into the file**, not buffered as a response body:
  that is the one way the two paths differ, and it is why a file larger than
  memory is fine. The `.part` sibling, the byte counting, and the digest check
  are the same code on both.
- **A non-zero exit fails the download and is retried.** A program's exit code
  is its own (curl says 22 for a 404 and 7 for a refused connection), so unlike
  the built-in client this cannot tell an answer from a hiccup, and lets the
  attempt limit end it. Its stderr goes to the log region.
- The size is unknown up front — there is no `Content-Length` — so the display
  shows `?%` for that file and marks the total as a floor.

### Checking a download against a digest

`<hash>` is optional; when present the file is verified as it streams, and one
that does not match its digest is deleted rather than renamed into place.

```xml
<hash algo="sha256"><value name="digest"/></hash>
```

- `algo=` is `sha256` (the default), `sha512`, `sha1`, or `md5`.
- The body is a template like any other, so the digest usually comes from the
  same manifest record as the URL, or from a step that fetched a `.sha256` file.
- **A mismatch is final.** The bytes arrived intact, so refetching them cannot
  change the digest; the run reports expected and actual, and exits non-zero.
- A success says which algorithm passed (`downloaded x.iso (6.0 MiB, sha256
  ok)`) — a check you cannot see happen is one you cannot trust ran.
- **The digest must look like one.** A renamed manifest field renders as the
  template engine's placeholder, and that is rejected before anything is
  fetched, rather than quietly leaving the file unverified. A `sha256sum` line
  (`<hex>  <name>`) is accepted, as is any capitalization.
- **To make it optional per record**, render it empty for the records that have
  no digest: `<hash><if test="sha256"><value name="sha256"/></if></hash>`.

`<downloads>` sets the queue up once for the config: `concurrency` (default 4),
`retries` (3; `0` means report a failure immediately), `dir` (`.`), and
`log_lines` for the log region's height
(default: `min(15, half the terminal)`). `--concurrency`, `--download-dir`,
`--log-lines`, and `--no-tui` override them per invocation.

## Output: fields

A leaf can declare the *shape* of its output records once, via `<fields>`. The
renderer then represents that one declaration automatically -- as a table, a
`Label: value` list, JSON, Markdown, CSV, plain lines, or an
[ASCII timeline](#timeline) -- choosing a default from the data's shape. You
never write "table" anywhere; a runtime flag (`--as`) or a pipe can force any
representation.

```xml
<fields over="data.items" footer="{{.data.total_count}} total">
	<field name="name">full_name</field>            <!-- rename a field -->
	<field name="stars">stargazers_count</field>
	<field name="lang" default="-">language</field> <!-- fallback for empty -->
	<field name="sha" truncate="7">sha</field>      <!-- transform -->
	<field name="branch" expr="{{.head.ref}} -> {{.base.ref}}"/>  <!-- computed -->
</fields>
```

Automatic representation, by data shape:

| Data | Default | Other sinks |
|------|---------|-------------|
| array of records | `table` | `json`, `markdown`, `csv`, `timeline` |
| single record | `list` | `json` |
| array of scalars | `lines` | `json` |
| scalar / non-JSON | `raw` | -- |

| `<field>` attribute | Meaning |
|---------------------|---------|
| body text | Record-relative source path (`login`, `user.login`). |
| `@key` / `@value` | The entry key/value when `over=` walks a map. |
| `expr=` | A virtual field: a Go template with the record as `.` and the whole context as `$` (`$.var`, `$.data`). Overrides the path. |
| `default=` | Substitute for an empty value. |
| `truncate="N"` | Cap the string to N characters. |
| `firstline="true"` | Keep only the first line. |
| `priority="N"` | Lowest priority columns are dropped first when a table is too narrow (default 0; ties keep document order). |
| `show_in=` | Gate per sink: `""`/`*` = all; an allowlist (`json,csv`) shows only there; a negated list (`!json`) shows everywhere except there. |

`<fields over="path"/>` selects where the records live (`data.items`, a map for
`@key`/`@value`, `data.names` for scalars) rather than the whole body.
`footer=` adds a trailing summary line for the human sinks.

With **no** `<fields>` at all, a request leaf prints its (jq-shaped) JSON body;
add `--as=table` to project nothing and table the raw keys.

### Forcing a representation

`--as=<sink>` forces `table | list | lines | raw | json | markdown | csv |
timeline`. `--no-format` (or `--format=raw`, `NO_FORMAT=1`) returns the raw
body. So `gh repo get x` is a list on a terminal, `gh repo get x --as=json | jq`
is JSON, and `gh repo get x --no-format` is the unshaped response.

### Timeline

`--as=timeline` renders the records as a horizontal, annotated ASCII timeline
(via [`ascii-timeline`](https://github.com/wow-look-at-my/ascii-timeline)). Each
record becomes one event, and the **field name** selects what it contributes:

| Field name    | Event role                                              |
|---------------|---------------------------------------------------------|
| `date`        | A **point** event at this instant (a `●` marker).       |
| `start`+`end` | A **duration** event spanning the range (a `█` bar).    |
| `label`       | Text shown next to the marker/bar.                      |
| `description` | Dim text appended after the date.                       |
| `color`       | Style spec (e.g. `green`, `bold cyan`); auto-assigned otherwise. |

Other field names are ignored, and `show_in=` still applies (use
`show_in="timeline"` / `!timeline` to scope a field). Dates accept the many
formats `ascii-timeline` understands (`2006-01-02`, `Jan 2, 2006`, RFC3339, ...).
A record that resolves no `date` and no `start`+`end` pair is skipped, so partial
data degrades gracefully. Color follows the terminal (off when piped or under
`NO_COLOR`), and the axis uses the terminal width.

```xml
<fields>
	<field name="label">name</field>
	<field name="date">created_at</field>          <!-- point event -->
	<field name="start">window.opened</field>      <!-- or a duration: start -->
	<field name="end">window.closed</field>        <!--                + end   -->
	<field name="description" default="-">title</field>
</fields>
```

```text
$ ghr repo releases golang/go --as=timeline
# Timeline

Feb 8, 2026  →  Sep 2, 2026   (7 months)
 Mar 2026        May 2026        Jul 2026        Sep 2026
├──┴───────────────┴───────────────┴───────────────┴──────────┤
   ● go1.24.1 (Mar 4, 2026)
            ● go1.24.2 (Apr 1, 2026)
                            ● go1.25rc1 (Jun 10, 2026)
                                              ● go1.25 (Sep 2, 2026)
```

The sample's `repo commits` command similarly maps `commit.author.date` to a
timeline. If the upstream JSON already has keys named `label`/`date`/`start`/
`end`, you can even skip the `<fields>` block: `... --as=timeline` derives them
directly.

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

The engine doesn't care that a command is HTTP. A tiny git wrapper:

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

## Passthrough mode

When a leaf sets `passthrough="true"`, the command accepts arbitrary positional
args (everything after `--` in the wrapper script) and performs its own minimal
flag extraction:

1. Only declared `<flag>`s are recognized (matched with one or two leading
   dashes, e.g. both `-o` and `--o`).
2. Everything else -- unknown flags, their values, bare positionals -- is
   collected into `.rest` (a `[]string`).
3. Extracted flags do NOT appear in `.rest`, so `{{spread .rest}}` reconstructs
   the original command line minus the captured flags.

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

**Constraints:** `passthrough` is mutually exclusive with `<arg>`; leaf-only.
Flags support `=` and next-arg syntax; `bool` flags consume no value;
`string-slice` flags accumulate. Filter `.rest` with `filterSuffix` /
`filterPrefix`.

## Result reuse across calls (steps)

A leaf can declare `<steps>` -- pre-execution stages that run before the leaf's
own run. Each step's output is captured and exposed under `.result.<name>` for
later steps and the leaf's own `entry`/run. This enables indirection (resolve a
name to an ID, then use it), joins, and fan-out pipelines.

A step runs a command or a request -- the same fork as `<run>`. A step that
declares neither inherits the leaf's effective run, so chaining two calls
against an inherited `<request>` needs nothing but a second `<entry>`:

```xml
<command name="user-posts" description="List posts for a user looked up by username.">
	<arg name="username" required="true"/>
	<steps>
		<step name="user">
			<entry>
				<path>/users</path>
				<query><param name="username"><value name="arg.username"/></param></query>
			</entry>
		</step>
	</steps>
	<entry>
		<path>/posts</path>
		<query><param name="userId"><value expr="{{ (index .result.user 0).id }}"/></param></query>
	</entry>
</command>
```

Mix freely: a `<step><run>` may be a shell command while the leaf performs a
request, or the reverse.

- Steps run in declaration order; each `entry` is rendered against the current
  context including `.result.*` from prior steps.
- Step output is parsed as JSON (with `UseNumber`); non-JSON is kept as a string.
  A request step stores what the leaf would have printed, `<response jq=>`
  shaping included.
- A non-zero step aborts the run with that exit code.
- A `when` attribute (a Go-template predicate) skips a step when falsy (empty,
  `false`, `0`, `no`); `.result.<name>` is then unset.
- When more than one command runs, `N executions` is printed to stderr; suppress
  with `--quiet`/`-q`.

## Legacy formats and views

Alongside `<fields>`, the older explicit `<format>`/`<view>` system (inspired by
PowerShell's `.format.ps1xml`) is still available for cases where you want full
control of the rendered template. A leaf uses `<fields>` *or* `<format>`, not
both.

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

`input=` is `json` (default), `lines`, or `raw`. Formatting applies iff the
author `when` predicate AND the user verdict agree; `--view=<name>` forces a
view. An inline `<format>` (with `<view>` children, no `ref=`) overrides an
inherited one. See [Global flags](#global-flags) for `--format`/`--no-format`.

## Template helpers

Every [sprig v3](https://masterminds.github.io/sprig/) helper is available
(`toJson`, `upper`, `default`, `required`, `regexReplaceAll`, ...). On top of
sprig:

| Helper        | Purpose                                                                                   |
|---------------|-------------------------------------------------------------------------------------------|
| `truthy`      | Truthiness used by `<if test=>` (nil/`false`/`0`/`no`/"" are falsy).                       |
| `querystring` | Render a map as `?k=v&k=v` (URL-encoded). Empty values dropped.                            |
| `repeatkey`   | Repeated params for one key over a slice: `repeatkey "tag" .arg.tags`.                     |
| `shellquote`  | POSIX single-quote a value for the shell form of a command.                               |
| `urlpath`     | URL-escape a single path segment.                                                         |
| `spread`      | Splat a slice into multiple argv slots (or shell-quoted words). Works with `[]string`/`[]int`/`[]any`. |
| `fileExists` / `dirExists` | Path predicates, useful in `<preconditions>`.                              |
| `tabwriter`   | Align rows of tab-separated cells (display-width aware).                                  |
| `padRight` / `padLeft` / `displayWidth` / `stripANSI` | Width-aware string helpers.                  |
| `filterSuffix` / `filterPrefix` | Filter a `[]string` (used with `.rest`).                            |

## Template semantics

- Parsed with `missingkey=zero` -- missing map keys don't error. Use
  `{{default "" .x}}`, `{{if .x}}...{{end}}`, or `{{required "msg" .x}}`.
- `<entry>` is rendered first (without `.entry` in scope); every string leaf is
  rendered independently and exposed as `.entry`.
- `<vars>` resolve to a fixpoint, so a var can reference another var
  (`var.filter` can interpolate `var.noise`).

## Working directory and stdin

`<cwd>` and `<stdin>` may appear at the top level, on any `<command>`, and on any
`<step>`. Both are templates and inherit down the tree (closest non-empty
ancestor wins; a step overrides its leaf). `<cwd>` sets the child's working
directory (default: the caller's cwd); `<stdin>` feeds a rendered string to the
child's stdin (default: inherit the parent's stdin). These apply to shell/argv
commands.

```xml
<config name="stack">
	<cwd><value name="env.STACKS_ROOT"/></cwd>
	<command name="up"><run><argv>docker</argv><argv>compose</argv><argv>up</argv></run></command>
</config>
```

## Config format

A config is **XML 1.1** (`<?xml version="1.1" encoding="UTF-8"?>`). Structural
indentation is **tabs**; the loader dedents the common leading tabs from
multi-line text content. Use CDATA (`<![CDATA[ ... ]]>`) for content with `<`,
`&`, or a foreign language like a jq program.

```xml
<?xml version="1.1" encoding="UTF-8"?>
<config name="apicli" schema="./api.schema.xsd">
	<vars>
		<var name="base_url">https://api.example.com/v1</var>
		<var name="filter"><![CDATA[walk(if type=="object" then with_entries(select(.key|endswith("url")|not)) else . end)]]></var>
	</vars>
	...
</config>
```

Attribute values are always raw (templates or context paths), so the double
quotes Go templates need (`eq .x "y"`) are written with `'single quotes'` around
the attribute, or escaped. The `schema=` attribute is an editor hint pointing at
the XSD; the loader ignores it.

## Config schema

An XSD reference for the grammar lives at [`api.schema.xsd`](./api.schema.xsd)
(also printed by `api-cli docs schema`). It documents every element and
attribute for editor tooling. It is a guide, not the enforcement point: the
loader is authoritative (configs are validated by loading them), and a strict
XSD validator cannot represent the recursive `<command>` grammar.

### Top-level elements

| Element | Notes |
|---------|-------|
| `<config name="..." [schema="..."]>` | Root. `name` is required. |
| `<description>` | Shown as the CLI header in `--help`. |
| `<vars><var name="...">...</var></vars>` | Shared variables (inherited, fixpoint-resolved). |
| `<run>` | Default executable (request / argv / shell). Inherited. |
| `<cwd>` / `<stdin>` | Default working directory / stdin templates. Inherited. |
| `<formats>` | Named, reusable legacy formats. |
| `<transports>` | Named programs that perform requests. See [Transports](#transports). |
| `<downloads concurrency= retries= dir= log_lines=/>` | Settings for the shared download queue. See [Downloads](#downloads). |
| `<command>` | Top-level subcommands. |

### `<command>`

| Attribute / child | Notes |
|-------------------|-------|
| `name=` (required) | Subcommand name. Not `help`, `completion`, `docs`. |
| `description=` | Shown in help. |
| `passthrough="true"` | Leaf-only. See [Passthrough mode](#passthrough-mode). |
| `confirm=` (or `<confirm>`) | Prompt `<msg> [y/N]` before running; bypass with `--yes`. Inherited. |
| `<arg>` / `<flag>` | Positional args / named flags. |
| `<vars>` | Merged with ancestor vars (this node wins). |
| `<run>` / `<cwd>` / `<stdin>` | Override the inherited executable / cwd / stdin. |
| `<steps>` | Leaf-only. Pre-execution stages, each a command or a request. |
| `<entry>` | Leaf-only. `<path>`, `<query>`, or user-defined keys -> `.entry`. |
| `<preconditions><precondition>` | Leaf-only. A non-empty render is a fatal error message (exit 1). |
| `<fields>` / `<format>` | Output shape (auto) / legacy format. Leaf-only; not both. |
| `<download over= when= transport=>` | Leaf-only, repeatable. Hands URLs to the download queue. See [Downloads](#downloads). |
| `<command>` | Nested subcommands. |

### `<arg>` and `<flag>`

`<arg name= type="string|int" required= variadic= description=/>`. A `variadic`
arg (last only) collects the rest into a typed slice; pair with `spread`.

`<flag name= short= type="string|bool|int|string-slice" default= required=
conflicts="a,b" description=/>`. A string `default` may itself be a template
(rendered when the flag isn't set). A `bool` flag defaulting to `true` gets a
hidden `--no-NAME` companion.

## Global flags

| Flag              | Short | Default | Notes |
|-------------------|-------|---------|-------|
| `--config <path>` |       |         | Config file (XML). Falls back to `./api.xml`. |
| `--mcp <transport>` |     |         | Run the config as an MCP server: `stdio`, `http://<addr>`, `sse://<addr>`. Each leaf becomes a tool; HTTP/SSE also expose `GET /health`. Behaves like `--format=always`. |
| `--cors <level>`  |       | `strict`| CORS for the MCP HTTP/SSE server. See [CORS levels](#cors-levels). |
| `--quiet`         | `-q`  | false   | Suppress the `N executions` line. |
| `--yes`           | `-y`  | false   | Skip `confirm` prompts. |
| `--verbose`       |       | false   | Show executed commands/requests, exit codes, conditions on stderr. |
| `--debug`         |       | false   | Full execution detail (implies `--verbose`). |
| `--no-format`     |       | false   | Disable output formatting (= `--format=raw`). |
| `--format <mode>` |       | `auto`  | `raw` / `auto` / `always`. |
| `--as <sink>`     |       |         | Force a `<fields>` representation: `table|list|lines|json|markdown|csv|timeline`. |
| `--view <name>`   |       |         | Pick a named legacy view, bypassing predicate selection. |
| `--var KEY=VALUE` |       |         | Set an env var before evaluation (so `{{.env.KEY}}` sees it). Repeatable. |
| `--concurrency <n>` |     | `4`     | Parallel downloads. See [Downloads](#downloads). |
| `--download-dir <path>` | | `.`     | Base directory for `<download>` destinations. |
| `--log-lines <n>`  |      |         | Height of the download display's log region. Default: `min(15, half the terminal)`. |
| `--no-tui`        |       | false   | Report download progress as plain lines instead of drawing the display. |

Env vars (lower precedence than flags): `NO_FORMAT` (any value disables
formatting), `API_CLI_FORMAT` (`raw`/`auto`/`always`).

## CORS levels

When the MCP server runs over HTTP/SSE, `--cors <level>` controls which browser
origins may talk to it (irrelevant for `--mcp=stdio`).

| Level         | Origins allowed | When to use |
|---------------|-----------------|-------------|
| `disabled`    | Any (`*`).      | Local prototyping only. |
| `permissive`  | localhost/loopback + same-origin. | Local browser dev tools hitting a remote server. |
| `strict`      | Same-origin only (default). | Single-tenant server with one frontend. |
| `enabled`     | None (header never sent). | Locked down; non-browser clients only. |

Requests with no `Origin` header (curl, server-to-server, AI tools) always pass
through; CORS only matters for browsers. `Access-Control-Allow-Credentials` is
never emitted.

## Built-in subcommands

The `docs` subcommand prints embedded documentation; it works without a config.

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

[`samples/github/github.xml`](./samples/github/github.xml) wraps the read-only
slice of the GitHub REST API with first-class requests:

- **Subcommands**: `user get|repos|orgs`, `repo get|issues|issue|prs|pr|pr-diff|pr-comments|releases|release|commits|commit|branches|tags|contents|readme|languages|topics`, `org get|members|repos`, `search repos|code|issues|users`, `rate-limit`.
- **Token-aware**: picks up `$GITHUB_TOKEN` / `$GH_TOKEN` automatically (5000 vs 60 req/hr).
- **Enterprise-ready**: set `$GITHUB_API_URL` for GitHub Enterprise Server.
- **Noise stripping**: every response runs through an embedded `jq` `walk` that drops `*url` links, `node_id`s, `reactions`, `permissions`, duplicate counts, and more -- often ~80% fewer bytes. (`GITHUB_RAW=1` opts out.)
- **Fields views**: list endpoints become tables, single objects become `Label: value` lists, automatically by data shape.

```sh
mkdir -p ~/.config/ghr && cp samples/github/github.xml ~/.config/ghr/github.xml
# ~/.local/bin/ghr:  exec api-cli --config ~/.config/ghr/github.xml "$@"
ghr user get octocat
ghr repo issues cli/cli --state open -n 10
ghr search repos 'language:go stars:>10000' --sort stars
ghr repo languages golang/go --as=json
```

## Development

```sh
go-toolchain        # runs go mod tidy, vet, tests, coverage, and build
```
