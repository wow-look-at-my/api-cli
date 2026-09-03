# api-cli

A declarative command-line alias system. You write an **XML** config that describes a tree of commands: subcommands, args, flags and your own variables. The tool builds a cobra command tree from it. Each leaf runs a command (shell or argv) or makes a first-class HTTP **request**. It then renders the result, optionally through the **fields** auto-formatter. Help at every level, shell tab completion and strong templating come for free.

It is a *hybrid* tool. HTTP requests are first-class (`<run><request>`, with no `curl` or `jq` subprocess). The general shell and argv execution engine stays, so non-HTTP aliases (git, tar, ...) work as well.

Two example configs ship in this repo:

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

A leaf runs what its closest `<run>` ancestor gives it, then presents the output. `<run>` comes in three forms.

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

Templates build the whole `<request>`, and the Go HTTP client performs it. There is no `curl`. An embedded `jq` engine ([gojq](https://github.com/itchyny/gojq)) shapes a JSON response, so no `jq` binary is necessary. To send requests through your own program instead, see [Transports](#transports).

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
| `<response jq="program"/>` | Shape the JSON body with a jq program. See below. A `<response/>` with no `jq=` pretty-prints the JSON instead. Leave `<response>` out to return the raw body verbatim (diffs, READMEs, ...). |

`jq=` is a template, like `<url>` and `<body>`. What you write selects one of three forms.

| You write | It means |
|-----------|----------|
| `jq=".[] \| .name"` | The jq program itself. |
| `jq=".[0:{{ .flag.limit }}]"` | A template. It renders against this invocation's context (`.arg`, `.flag`, `.var`, `.entry`, `.env`), so the program depends on the flags this run was given. |
| `jq="var.filter"` | A bare dotted name is a context path. It must name a string, and that string is the program. A path that names nothing, or names anything but a string, fails the run. |

The path form keeps a long program out of the attribute. The var it names renders against this run's flags too, so both of these answer to `--limit`.

```xml
<vars>
	<var name="filter">.[0:<value name="flag.limit"/>]</var>
</vars>
...
<response jq="var.filter"/>
<response jq=".[0:{{ .flag.limit }}]"/>
```

One property keeps the forms apart. A jq program almost always opens with `.`, `$`, `[`, `{`, a digit or an operator, and a context path starts with none of those. A bare builtin is the exception: `jq="length"` reads as a path and fails, so write `jq=". | length"`.

The shaped body is what the leaf prints, `--format=raw` included. jq runs before every presentation layer, and never as part of one.

A status of 400 or more prints the body to stderr and exits non-zero, like `curl -f`. The root `<run>` usually holds the shared request. A leaf's own `<run>` overrides it, for example with a `POST` or a raw-body download.

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
- **A non-zero exit fails the request**, as a 4xx from the built-in client does. The program's stderr passes through untouched.
- **Which transport runs**: the request's `transport=` attribute, then the registry's `default="true"` entry, then the built-in client. There is no override at run time. How a request reaches its endpoint is a property of that endpoint, not a user preference. The name `http` belongs to the built-in client, so `transport="http"` on one request opts that request out of a default transport. That is the public endpoint in an otherwise internal API.
- `<cwd>` sets the program's working directory. A transport's `<run>` must be a command. It is the thing that performs a request. It cannot be one.

To build repeated flags, assemble a list and `spread` it.

```xml
<argv><value expr="{{ $h := list }}{{ range .request.header_lines }}{{ $h = concat $h (list &quot;-H&quot; .) }}{{ end }}{{ spread $h }}"/></argv>
```

An empty `spread` contributes no argument at all. That makes it the way to write a conditional argument. `{{ spread (ternary (list "--data-binary" "@-") (list) (ne .request.body "")) }}` adds two arguments only when a body exists. A plain `<if>` leaves an empty string in the argv instead. See the `curl` transport in [`api.example.xml`](./api.example.xml) for both together.

## Downloads

A step can work a URL out: parse it from a listing, sign it, or follow a redirect. It then hands the URL to a shared downloader in place of printing it. One queue serves the whole run. It fetches four files at a time by default. It also carries the auth the steps established.

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

On a terminal this draws a live display. Each transfer gets a line with its percentage, sizes, rate and ETA. An aggregate `TOTAL` row follows. Below that sits a height-capped log region that scrolls itself. It carries the output of the steps and of the downloader.

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

In a pipe there is no display. Progress stays line-based on stderr, and the destination paths go to stdout, one per line, for whatever reads them next.

- **`<download>` is the leaf's action.** It runs after the steps, and it stands in for the leaf's `<run>`. An inherited request therefore does not fire on the way.
- **`when=`** is a Go-template predicate. A falsy render (empty, `false`, `0` or `no`) skips that declaration, so one leaf can carry a conditional set.
- **`over=`** repeats the declaration per record of a list. It promotes the record's keys (`<value name="name"/>`) and puts the record itself at `.item`. An empty list downloads nothing. A path that resolves to nothing is an error.
- **`<url>` can render several lines**, which is what a `<for>` loop produces. Each line becomes its own download.
- **`<to>`** is a file path. It is a directory when it ends in `/`, when it names an existing directory, or when it serves several URLs. Leave it empty for the download directory, named by the URL or by the response's `Content-Disposition`.
- **A relative `<to>` resolves under the download directory** (`dir=`, or `--download-dir`). An absolute one stands as it is.
- **Auth rides along.** `<header>` and `<cookie>` render against the same context and take `<if test=>`. The cookies fold into one `Cookie` header.
- **Mistakes fail before the first byte.** The plan step reports a `<url>` that renders to anything but an `http(s)` URL. It also reports two records whose `<to>` renders one file path. A mistyped path is therefore not a timeout minutes later, and a `<to>` that forgot to vary does not overwrite itself N times.
- **Failures are loud.** A 4xx is the answer. A 5xx or a network fault retries at a fixed one-second cadence. A transfer that still fails names its URL on stderr and exits non-zero, while the other files carry on.
- Bytes land in a `.part` sibling first, so an interrupted transfer never leaves a truncated file under the real name.
- **No resume.** Every attempt starts at byte zero. A leftover `.part` is overwritten rather than continued.

### Downloading through a transport

A `<download>` reaches its URL as a `<request>` does. It goes over the built-in client, or over a [transport](#transports) program when the endpoint needs one.

```xml
<download transport="corp">…</download>
```

- **Selection matches requests**: the `transport=` attribute, then the registry's `default="true"` entry, then the built-in client. A config whose endpoints all need the program therefore needs it for its files too, and says nothing extra. `transport="http"` opts one download back to the built-in client.
- **The program gets the same `.request` context** -- `method`, `url`, `headers` and `header_lines` -- so one program serves requests and downloads alike. `method` is `GET`, and there is no body.
- **Its stdout streams into the file** rather than into a buffered response body. That is the one difference between the two paths. It is also why a file larger than memory is fine. The `.part` sibling, the byte count and the digest check are the same code on both.
- **A non-zero exit fails the download, and the queue retries it.** A program owns its own exit codes. curl says 22 for a 404 and 7 for a refused connection. This path therefore cannot tell an answer from a hiccup, unlike the built-in client, and it lets the attempt limit end the transfer. Its stderr goes to the log region.
- The size is unknown at the start, because there is no `Content-Length`. The display shows `?%` for that file, and it marks the total as a floor.

### Checking a download against a digest

`<hash>` is optional. With one present, the queue verifies the file as it streams. A file that does not match its digest is deleted rather than renamed into place.

```xml
<hash algo="sha256"><value name="digest"/></hash>
```

- `algo=` is `sha256` (the default), `sha512`, `sha1`, or `md5`.
- The body is a template like any other. The digest therefore usually comes from the same manifest record as the URL, or from a step that fetched a `.sha256` file.
- **A mismatch is final.** The bytes arrived intact, so a second fetch cannot change the digest. The run reports the expected value and the actual value, then exits non-zero.
- A success names the algorithm that passed: `downloaded x.iso (6.0 MiB, sha256 ok)`. A check you cannot see happen is one you cannot trust.
- **The digest must look like a digest.** A renamed manifest field renders as the template engine's placeholder. The plan step rejects that before it fetches anything, rather than leave the file unverified in silence. A `sha256sum` line (`<hex>  <name>`) is acceptable, in any capitalization.
- **To make the check optional per record**, render the body empty for a record that carries no digest: `<hash><if test="sha256"><value name="sha256"/></if></hash>`.

`<downloads>` sets the queue up one time for the config. It takes `concurrency` (default 4), `retries` (default 3, where `0` reports a failure immediately), `dir` (default `.`), and `log_lines` for the height of the log region (default `min(15, half the terminal)`). `--concurrency`, `--download-dir`, `--log-lines` and `--no-tui` override those values per invocation.

## Output: fields

A leaf declares the *shape* of its output records one time, in `<fields>`. The renderer then represents that one declaration by itself: a table, a `Label: value` list, JSON, Markdown, CSV, plain lines, or an [ASCII timeline](#timeline). It picks the default from the shape of the data. You never write "table" anywhere. A flag (`--as`) or a pipe forces any representation at run time.

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
| map walked by `@key`/`@value` | `table` | `json`, `markdown`, `csv` |
| array of scalars | `lines` | `json` |
| scalar / non-JSON | `raw` | -- |

A map is one record unless some field reads `@key` or `@value`. That is the signal to walk it entry by entry. An array of scalars is `lines` only when the leaf declares no `<field>`. One declared field keeps the table shape.

| `<field>` attribute | Meaning |
|---------------------|---------|
| body text | Record-relative source path (`login`, `user.login`). A numeric step indexes a list (`assets.0.name`). |
| `@key` / `@value` | The entry key/value when `over=` walks a map. |
| `expr=` | A virtual field: a Go template with the record as `.` and the whole context as `$` (`$.var`, `$.data`). Overrides the path. |
| `default=` | Substitute for an empty value. |
| `truncate="N"` | Cap the string to N characters. |
| `firstline="true"` | Keep only the first line. |
| `priority="N"` | The lowest priority column drops first when a table is too narrow. The default is 0. A tie drops the rightmost column first, and one column always survives. |
| `show_in=` | Gate the field per sink. `""` and `*` mean every sink. An allowlist (`json,csv`) shows it only there. A negated list (`!json`) shows it everywhere else. |

`<fields over="path"/>` selects where the records live, in place of the whole body: `data.items`, a map for `@key` and `@value`, or `data.names` for scalars. The path reads against the body first (`items`), then against the whole context (`data.items`), so either spelling reaches the same records. A numeric step indexes a list (`pages.0.rows`). `footer=` adds a trailing summary line to the human sinks.

**A path that names nothing is an error**, and so is one that names a scalar. The run exits non-zero and names the path. An empty table over exit 0 reads as an API that returned nothing, which is how a renamed field costs an afternoon. An empty **list** is a real answer. It stays quiet.

A request leaf with **no** `<fields>` at all prints its JSON body, as jq shaped it. Add `--as=table` to project nothing and to table the raw keys.

### Forcing a representation

`--as=<sink>` forces `table | list | lines | raw | json | markdown | csv | timeline`. `--no-format` returns the raw body, and `--format=raw` and `NO_FORMAT=1` do the same. So `gh repo get x` is a list on a terminal, `gh repo get x --as=json | jq` is JSON, and `gh repo get x --no-format` is the unshaped response.

### Timeline

`--as=timeline` renders the records as a horizontal ASCII timeline with annotations, through [`ascii-timeline`](https://github.com/wow-look-at-my/ascii-timeline). Each record becomes one event, and the **field name** selects what that field contributes.

| Field name    | Event role                                              |
|---------------|---------------------------------------------------------|
| `date`        | A **point** event at this instant (a `●` marker).       |
| `start`+`end` | A **duration** event spanning the range (a `█` bar).    |
| `label`       | Text shown next to the marker/bar.                      |
| `description` | Dim text appended after the date.                       |
| `color`       | Style spec, for example `green` or `bold cyan`. The renderer assigns one when the field is absent. |

The renderer ignores every other field name, and `show_in=` still applies. Use `show_in="timeline"` or `!timeline` to scope a field. A date can take any of the many formats `ascii-timeline` reads (`2006-01-02`, `Jan 2, 2006`, RFC3339, ...). The renderer skips a record that resolves no `date` and no `start` and `end` pair, so partial data still draws. Color follows the terminal, and goes off in a pipe or under `NO_COLOR`. The axis uses the terminal width.

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

The sample's `repo commits` command maps `commit.author.date` to a timeline in the same way. When the upstream JSON already carries keys named `label`, `date`, `start` and `end`, you can leave the `<fields>` block out. `... --as=timeline` then derives them directly.

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

## Passthrough mode

A leaf that sets `passthrough="true"` accepts arbitrary positional args, which is everything after `--` in the wrapper script. It then does its own minimal flag extraction.

1. The parser recognizes a declared `<flag>` only, by name or by `short=`, with one or two leading dashes. Both `-o` and `--o` match.
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

## Result reuse across calls (steps)

A leaf can declare `<steps>`, which are stages that run before the leaf's own run. Each step's output is captured and exposed at `.result.<name>`, for a later step and for the leaf's own `entry` and run. That gives you indirection, such as a name resolved to an ID and then used. It also gives you joins and fan-out pipelines.

A step runs a command or a request, the same fork as `<run>`. A step that declares neither one inherits the leaf's effective run. Two chained calls against an inherited `<request>` therefore need nothing but a second `<entry>`.

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

Mix the two freely. A `<step><run>` can be a shell command while the leaf makes a request, or the reverse.

- Steps run in declaration order. Each `entry` renders against the current context, and that context includes `.result.*` from the prior steps.
- Step output parses as JSON, with `UseNumber`. Output that is not JSON stays a string. A request step stores what the leaf prints, and that includes the `<response jq=>` shaping.
- A non-zero step aborts the run with that exit code.
- A `when` attribute is a Go-template predicate. It skips the step on a falsy render (empty, `false`, `0` or `no`), and `.result.<name>` then stays unset.
- More than one command in a run prints `N executions` to stderr. Suppress that line with `--quiet` or `-q`.

## Legacy formats and views

The older explicit `<format>` and `<view>` system stays next to `<fields>`, for a case that needs full control of the rendered template. PowerShell's `.format.ps1xml` is its ancestor. A leaf uses `<fields>` *or* `<format>`, never both.

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
| `tabwriter`   | Align rows of tab-separated cells (display-width aware).                                  |
| `padRight` / `padLeft` / `displayWidth` / `stripANSI` | Width-aware string helpers.                  |
| `filterSuffix` / `filterPrefix` | Filter a `[]string` (used with `.rest`).                            |

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
<config name="apicli" schema="./api.schema.xsd">
	<vars>
		<var name="base_url">https://api.example.com/v1</var>
		<var name="filter"><![CDATA[walk(if type=="object" then with_entries(select(.key|endswith("url")|not)) else . end)]]></var>
	</vars>
	...
</config>
```

An attribute value is always raw, a template or a context path. A Go template needs double quotes for `eq .x "y"`, so put `'single quotes'` around such an attribute, or escape the inner quotes. The `schema=` attribute is an editor hint that points at the XSD. The loader ignores it.

## Config schema

An XSD reference for the grammar lives at [`api.schema.xsd`](./api.schema.xsd), and `api-cli docs schema` prints it. It documents each element and attribute, for editor tooling. It is a guide, and not the enforcement point. The loader is authoritative, because api-cli validates a config by loading it. A strict XSD validator also cannot express the recursive `<command>` grammar.

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
| `name=` (required) | Subcommand name. No whitespace or slashes, and not `help`, `completion`, `__complete`, or `docs`. |
| `description=` | Shown in help. |
| `passthrough="true"` | Leaf-only. See [Passthrough mode](#passthrough-mode). |
| `confirm=` (or `<confirm>`) | Prompt `<msg> [y/N]` before the run. `--yes` bypasses it. Off a terminal the run refuses rather than assume a yes. Inherited. |
| `<arg>` / `<flag>` | Positional args / named flags. |
| `<vars>` | Merged with ancestor vars (this node wins). |
| `<run>` / `<cwd>` / `<stdin>` | Override the inherited executable / cwd / stdin. |
| `<steps>` | Leaf-only. Pre-execution stages, each a command or a request. |
| `<entry>` | Leaf-only. `<path>`, `<query>`, or user-defined keys -> `.entry`. |
| `<preconditions><precondition>` | Leaf-only. A non-empty render is a fatal error message (exit 1). |
| `<fields>` / `<format>` | The automatic output shape, or a legacy format. Leaf-only, and never both. |
| `<download over= when= transport=>` | Leaf-only, repeatable. Hands URLs to the download queue. See [Downloads](#downloads). |
| `<command>` | Nested subcommands. |

### `<arg>` and `<flag>`

`<arg name= type="string|int" required= variadic= description=/>`. A `variadic` arg comes last, and it collects the rest into a typed slice. Pair it with `spread`. A required arg cannot follow an optional one, because cobra counts positions and nothing can fill the gap.

`<flag name= short= type="string|bool|int|string-slice" default= required= conflicts="a,b" description=/>`. A string `default` can be a template itself. It renders when the user does not set the flag. A `bool` flag with a `true` default gets a hidden `--no-NAME` companion. A flag name therefore cannot start with `no-`. `short=` is one character.

## Global flags

| Flag              | Short | Default | Notes |
|-------------------|-------|---------|-------|
| `--config <path>` |       |         | Config file (XML). Falls back to `./api.xml`. |
| `--version`       |       |         | Print the binary's version. Needs no config. |
| `--mcp <transport>` |     |         | Run the config as an MCP server: `stdio`, `http://<addr>`, `sse://<addr>`. Each leaf becomes a tool named for its command path, with underscores (`users_get`). The HTTP and SSE servers also answer `GET /health`. The server behaves as `--format=always` does, with `.tty` true and width 80. |
| `--cors <level>`  |       | `strict`| CORS for the MCP HTTP/SSE server. See [CORS levels](#cors-levels). |
| `--quiet`         | `-q`  | false   | Suppress the `N executions` line. |
| `--yes`           | `-y`  | false   | Skip `confirm` prompts. |
| `--verbose`       |       | false   | Show executed commands/requests, exit codes, conditions on stderr. |
| `--debug`         |       | false   | Full execution detail (implies `--verbose`). |
| `--no-format`     |       | false   | Disable output formatting (= `--format=raw`). |
| `--format <mode>` |       | `auto`  | `raw` / `auto` / `always`. |
| `--as <sink>`     |       |         | Force a `<fields>` representation: `table|list|lines|raw|json|markdown|csv|timeline`. |
| `--view <name>`   |       |         | Pick a named legacy view, bypassing predicate selection. |
| `--var KEY=VALUE` |       |         | Set an env var before evaluation (so `{{.env.KEY}}` sees it). Repeatable. |
| `--concurrency <n>` |     | `4`     | Parallel downloads. See [Downloads](#downloads). |
| `--download-dir <path>` | | `.`     | Base directory for `<download>` destinations. |
| `--log-lines <n>`  |      |         | Height of the download display's log region. Default: `min(15, half the terminal)`. |
| `--no-tui`        |       | false   | Report download progress as plain lines instead of drawing the display. |

Two env vars apply, at a lower precedence than the flags. Any value of `NO_FORMAT` turns formatting off. `API_CLI_FORMAT` takes `raw`, `auto` or `always`.

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
- **Noise stripping**: each response goes through an embedded `jq` `walk`. It drops `*url` links, `node_id`s, `reactions`, `permissions`, duplicate counts and more. That often cuts about 80% of the bytes. `GITHUB_RAW=1` opts out.
- **Fields views**: list endpoints become tables, single objects become `Label: value` lists, automatically by data shape.

```sh
mkdir -p ~/.config/ghr && cp samples/github/github.xml ~/.config/ghr/github.xml
# ~/.local/bin/ghr:  exec api-cli --config ~/.config/ghr/github.xml "$@"
ghr user get octocat
ghr repo issues cli/cli --state open -n 10
ghr search repos 'language:go stars:>10000' --sort stars
ghr repo languages golang/go --as=json
```

## Using the formatter as a library

The `<fields>` formatter is importable on its own, at
`github.com/wow-look-at-my/api-cli/fields`. A program with decoded JSON gets the
same table, list, JSON, Markdown, CSV, or timeline output without adopting the
XML config language.

```go
f := &fields.Fields{Over: "items", List: []fields.Field{
	{Name: "id", Path: "id"},
	{Name: "name", Path: "name"},
}}
out, err := fields.Render(nil, f, body, map[string]any{"data": body}, "", 0)
```

The first argument evaluates `expr=` and `footer=` templates, and may be nil for
a declaration that uses neither. See `docs/fields-package.md`.

## Development

```sh
go-toolchain        # runs go mod tidy, vet, tests, coverage, and build
```
