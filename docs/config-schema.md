# Config schema

The grammar is an XSD that [api-cli-spec](https://github.com/wow-look-at-my/api-cli-spec) owns, and `api-cli docs schema` prints it. Write it out and check a config against it with [xml-validator](https://github.com/wow-look-at-my/xml-validator): `api-cli docs schema > api.xsd && xml-validator --schema api.xsd ./api.xml`. The test suite checks every config this repo ships that way. This repo keeps no copy of the schema. The loader stays authoritative at run time. It enforces the rules a schema cannot state.

One example is the rule that a leaf needs a run, its own or an ancestor's. api-cli-spec lists all of those rules.

## Top-level elements

| Element | Notes |
|---------|-------|
| `<config name="..." [schema="..."]>` | Root. `name` is required. |
| `<description>` | Shown as the CLI header in `--help`. |
| `<vars><var name="...">...</var></vars>` | Shared variables (inherited, fixpoint-resolved). |
| `<run>` | Default executable (request / argv / shell). Inherited. |
| `<cwd>` / `<stdin>` | Default working directory / stdin templates. Inherited. |
| `<preconditions><precondition>` | Guards every run in the tree. They accumulate rather than override. See [One guard for a whole subtree](#one-guard-for-a-whole-subtree). |
| `<formats>` | Named, reusable legacy formats. |
| `<transports>` | Named programs that perform requests. See [Transports](#transports). |
| `<downloads concurrency= retries= dir=/>` | Settings for the shared download queue. See [Downloads](#downloads). |
| `<command>` | Top-level subcommands. |

## `<command>`

| Attribute / child | Notes |
|-------------------|-------|
| `name=` (required) | Subcommand name. No whitespace or slashes, and not `help`, `completion`, `__complete`, or `docs`. |
| `description=` | Shown in help. |
| `passthrough="true"` | Leaf-only. See [Passthrough mode](#passthrough-mode). |
| `runnable="true"` | A node with subcommands runs in its own right. Every `<arg>` then needs a `pattern=`. See [A parent that also runs](#a-parent-that-also-runs). |
| `confirm=` (or `<confirm>`) | Prompt `<msg> [y/N]` before the run. `--yes` bypasses it. Off a terminal the run refuses rather than assume a yes. Inherited. |
| `watch=` | Repeat on this interval by default (`5s`, `500ms`, or seconds). `--watch` overrides it, and `--watch off` runs once. Inherited. Not on a `<download>` leaf. See [Watch](#watch). |
| `<arg>` / `<flag>` | Positional args / named flags. |
| `<vars>` | Merged with ancestor vars (this node wins). |
| `<run>` / `<cwd>` / `<stdin>` | Override the inherited executable / cwd / stdin. |
| `<steps>` | Leaf-only. Pre-execution stages, each a command or a request. |
| `<entry>` | Leaf-only. `<path>`, `<query>`, or user-defined keys -> `.entry`. |
| `<preconditions><precondition>` | A non-empty render is a fatal error message (exit 1). It runs before `<steps>`, so `.result` holds nothing. A config that reads `.result` there fails to load. Declared on any node, and the whole subtree runs it. |
| `<fields when=>` / `<format>` | The automatic output shape, or a legacy format. Leaf-only, and never both. `<fields>` repeats: every block whose `when=` holds renders. |
| `<download over= when= transport= group= order=>` | Leaf-only, repeatable. Hands URLs to the download queue. A `<join>` child concatenates a group. See [Downloads](#downloads). |
| `<stream mode= chunk=>` | Leaf-only. Cuts the leaf's own run into chunks and emits each as it is whole. An optional `<run>` child transforms one chunk at a time. See [Streaming](#streaming-stream). |
| `<mock exit=>` | Leaf-only. Stands in for a program. `<input>`, `<output>`, `<record>`, `<stdout>`, `<stderr>`. See [Mock executables](#mock-executables). |
| `<command>` | Nested subcommands. A node with children prints help, unless it declares `runnable="true"`. |

## `<arg>` and `<flag>`

`<arg name= type="string|int" required= variadic= pattern= description=/>`. A `variadic` arg comes last, and it collects the rest into a typed slice. Pair it with `spread`. A required arg cannot follow an optional one, because cobra counts positions and nothing can fill the gap. `pattern=` is a Go regular expression every supplied value must match, and a [runnable parent](#a-parent-that-also-runs) requires one on each arg. It is also a template. It can name a `<var>` or the built-in `{{ segmentPattern }}`. An MCP tool call is checked against it too, and the tool's `inputSchema` states it.

**Every declared arg is present.** An omitted optional arg holds the zero value of its type. That is `""` for a string, `0` for an int, and an empty slice for a variadic. A string reaches `urlpath .arg.id`, and every other helper that takes a string, with no guard around it. The same holds on the MCP side for a tool argument the caller leaves out.

One predicate covers both cases. `{{ .arg.id }}` is truthy when the arg is present, and `{{ not .arg.id }}` is truthy when it is omitted. That is the same truthiness `<if test=>` uses, so `<if test="arg.id">` and `when="{{ .arg.id }}"` always agree.

```xml
<command name="thing" description="List things, or show one.">
	<arg name="id"/>
	<steps>
		<!-- The when runs first, so the omitted id never reaches urlpath. -->
		<step name="detail" when="{{ .arg.id }}">
			<run><request><url><value name="var.api"/>/things/<value name="arg.id" as="urlpath"/></url></request></run>
		</step>
	</steps>
	<run><request><url><value name="var.api"/>/things<if test="arg.id">/<value name="arg.id" as="urlpath"/></if></url></request></run>
</command>
```

`<flag name= short= type="string|bool|int|string-slice" default= required= conflicts="a,b" description=/>`. A string `default` can be a template itself. It renders when the user does not set the flag. A `bool` flag with a `true` default gets a hidden `--no-NAME` companion. A flag name therefore cannot start with `no-`. `short=` is one character.
