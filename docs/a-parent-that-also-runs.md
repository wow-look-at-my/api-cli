# A parent that also runs

A node with `<command>` children prints help and nothing else. `runnable="true"` makes it execute as well, so one name is the group **and** the command. `tool thing` and `tool thing 42` run the parent, and `tool thing create` runs the child.

```xml
<command name="thing" runnable="true" description="List things, or show one.">
	<arg name="id" pattern="^[0-9]+$" description="Numeric thing ID. Omit it for the list."/>
	<run>
		<request><url><value name="var.api"/>/things<if test="arg.id">/<value name="arg.id" as="urlpath"/></if></url></request>
	</run>
	<fields when="{{ not .arg.id }}" over="items"><field name="id">id</field></fields>
	<fields when="{{ .arg.id }}"><field name="name">name</field></fields>
	<command name="create" description="Create a thing."><run><request method="POST">...</request></run></command>
</command>
```

Cobra reads the first positional as a subcommand name, so the two stay apart only when no argument value can spell one. `pattern=` is how a config states that, and the loader enforces it.

- **Every `<arg>` on a runnable node needs a `pattern=`.** It is a Go regular expression, and anchoring it with `^` and `$` is what makes it narrow.
- **A pattern that matches one of the node's own subcommand names is a load error.** It may not match a name cobra owns either: `help`, `completion`, `__complete` or `docs`.
- **A value that matches nothing is neither.** The error names both halves: the pattern the value missed, and the subcommands it is not.
- **`--` ends the subcommand lookup.** That is how a value that starts with a dash reaches the parent: `tool thing -- -5`.
- **`runnable="true"` and `passthrough="true"` cannot both hold.** Passthrough takes every argument, which leaves nothing to name a subcommand.

A runnable parent is a full node. `<arg>`, `<flag>`, `<steps>`, `<entry>`, `<fields>`, `<download>` and `<run>` all work as they do on a leaf. Its `<run>` still inherits to the children, as an ancestor's always did. It also becomes an MCP tool of its own, named for its path, next to the tools its children become.

`pattern=` works on a leaf too, where it is plain validation with no dispatch to disambiguate. `<arg name="sha" pattern="^[0-9a-f]{7,40}$"/>` rejects a bad value before the request goes out.

## `pattern=` is validation, on every path

A pattern is a rule about the value, not help text for the CLI. It applies wherever a value arrives.

- **An MCP tool call is checked against it too.** A value the CLI rejects never reaches a run over MCP either. The tool's `inputSchema` states the pattern as well. A caller therefore reads the shape of a legal value.
- **Each element of a variadic arg carries the pattern.** One bad element fails the invocation.
- **An omitted optional arg is not checked.** It holds the unset value of its type, and no pattern applies to an absent value.

## `pattern=` can name a `<var>`

A pattern is a template. It renders once at load time, against the vars in scope at that node plus `.env`. So one rule lives in one place, and every arg names it.

```xml
<vars><var name="segment">^[A-Za-z0-9_.-]+$</var></vars>
...
<arg name="owner" pattern="{{ .var.segment }}"/>
<arg name="repo" pattern="{{ .var.segment }}"/>
```

A pattern has to be known before any value arrives, so `.arg` and `.flag` are empty here. A pattern that renders empty, or that names a var the node does not have, is a load error.

## The built-in path-segment rule

`{{ segmentPattern }}` is the rule a traversal argument needs: one path component, made of unreserved characters, and never `.` or `..`. It rejects a slash, a backslash, a space and a percent escape. A value that matches it can neither split a path nor climb out of one.

```xml
<arg name="owner" pattern="{{ segmentPattern }}"/>
```

`safeSegments` is the same rule as a predicate, for a `<precondition>` or an `<if>`. It reads several values. A value that is a list contributes each element. An empty value is an absent one. A guard written over a whole tree therefore stays quiet on a leaf that declares no such arg.

```xml
<precondition>{{ if not (safeSegments .arg.owner .arg.repo) }}owner and repo must each be one path segment{{ end }}</precondition>
```
