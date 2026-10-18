# Mock executables

A `<mock>` leaf stands in for a program. The leaf takes the program's argv through passthrough mode. `<input>` names the parts of that argv. `<output>` writes the files the caller expects to find afterwards. `<record>` appends one line per call.

This exists for a build. A tool such as `make` decides what to do next from the files on disk and their timestamps. A mock compiler that writes its declared outputs therefore satisfies the build without compiling anything. A whole toolchain stands up this way before one line of the real work exists.

```xml
<config name="mock-toolchain">
	<command name="cc" passthrough="true">
		<flag name="o" short="o" type="string"/>
		<flag name="c" type="bool"/>
		<mock>
			<input name="src" match="\.c$" required="true"/>
			<output path="{{ .flag.o }}" when="{{ .flag.o }}"/>
			<output path="{{ stem .mock.src }}.o" when="{{ and .flag.c (not .flag.o) }}"/>
			<record path="build/compile_commands.jsonl"/>
		</mock>
	</command>
</config>
```

```sh
# Wrapper script, ahead of the real tool on PATH:
exec api-cli --config mock-toolchain.xml cc -- "$@"
```

`mock.example.xml` is a full stand-in toolchain: a compiler, an archiver and a linker.

## Installing the wrappers

`--install-mocks <dir>` writes one executable script per `<mock>` leaf and exits. Each script is named after its leaf, so a leaf at `tools cc` installs as `cc`. Put that directory first on `PATH`, and the build reaches the stand-in instead of the real tool. Nothing in the build system itself changes.

```sh
api-cli --config mock-toolchain.xml --install-mocks ./mockbin
export PATH="$PWD/mockbin:$PATH"
make
```

A name that more than one mock leaf carries is an error. One script cannot answer for both leaves. The build then calls a stand-in nobody can trace back.

## What an input matches

`match=` is a regular expression tested against each element of `.rest`, in order. The first match lands at `.mock.<name>`. With `variadic="true"` every match lands there as a list instead.

- `required="true"` fails the run when nothing matches. An invocation missing its source file is broken. An empty string renders an output path named `.o`.
- `default=` is a template used when nothing matches. It is the alternative to `required=`. The two cannot both hold.

The engine publishes these keys next to the named ones.

| Key            | Value                                                        |
|----------------|--------------------------------------------------------------|
| `.mock.argv`   | The whole command line, the leaf's name first. A `<record>` replays these, so it holds every declared flag, not the leftovers alone. |
| `.mock.prog`   | The leaf's name, which is the name the build called.         |
| `.mock.cwd`    | The working directory of this call.                          |
| `.mock.file`   | The first input that resolved to something.                  |
| `.mock.outputs` | Every path this call writes. `.mock.output` is the first.   |

An `<input>` matches `.rest`, not `.mock.argv`. A declared `<flag>` is already pulled out of the leftovers, and reading it back off the command line defeats the declaration.

## What an output writes

`path=` is a template, and its parent directory is created. The element's text is the file body. That body is empty by default, because a build tool reads a mock artifact's timestamp rather than its bytes.

- `when=` is a predicate, so one leaf covers the several shapes one real tool answers to.
- `over=` repeats the declaration per element of a list, exactly as a `<download over=>` does. One `<output>` therefore covers a compiler called with many sources.
- `from=` copies an existing file, for a stand-in that has to parse downstream. A declaration never carries both `from=` and a body.
- `mode=` is octal and defaults to `0644`. A linker stand-in wants `0755`.
- A path that records both render is an error. It names an `over=` whose path template forgot to vary.

## What a record writes

`<record path=>` appends one line per invocation. The default body is a `compile_commands.json` entry for this call, which is the reason the element exists. Point every tool of a build at one path. The build then writes its own compilation database on the way past.

```json
{"directory":"/src","arguments":["-c","src/foo.c"],"file":"src/foo.c","output":"foo.o"}
```

The element's text overrides that body. The append is what makes it safe under `make -j`: each call adds its own line without reading what is already there.

## Output text and the trailing newline

An element's text is trimmed, which is how the whole config language treats text. A literal newline at the end of a `<stdout>`, a `<stderr>` or an `<output>` body therefore does not survive. Write it as `<value expr="{{ &quot;\n&quot; }}"/>` wherever a real program emits one.

## Thin wrappers over the real tool

A leaf that declares a `<mock>` **and** its own `<run>` is a thin wrapper. The records and the outputs land first. The real program then runs. The process takes that program's exit code. A failed compile is exactly the one somebody wants the command line of. The record therefore happens either way.

An **inherited** `<run>` is not the leaf's own. It stays where it is, exactly as it does for a `<download>`. A mock leaf under a parent that declares a run does not fire that run on the way past.

**Constraints:** `<mock>` sits on a leaf only. It cannot share a leaf with a `<request>` or a `<download>`, because each one is the leaf's whole action. A mock that declares no `<output>`, `<record>`, `<stdout>` or `<stderr>` stands in for nothing. That load fails. `exit=` is a template for the exit code, which is how a mock stands in for a tool that fails.
