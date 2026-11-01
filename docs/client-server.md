# Client and server

A config can run on one side of a boundary for a caller on the other side. The usual boundary is a sandbox. The credentials stay outside it, and the commands that need them run there.

```sh
# Outside the sandbox, where the credentials are:
api-cli --config gh.xml --server /run/user/1000/gh.sock

# Inside the sandbox, as the program named gh:
exec api-cli --client /run/user/1000/gh.sock "$@"
```

The client loads no config. The server holds the only copy of the contract. The sides cannot disagree. A command, a subcommand or a flag that the config does not declare does not exist for the client. Cobra refuses it on the server side, and nothing runs.

## What crosses the socket

The client sends its argv, its working directory, and its stdin, stdout and stderr. The descriptors travel as `SCM_RIGHTS`. A run writes straight to the client's terminal or pipes, and a large output never passes through a buffer. The server answers with one JSON line, `{"exit":N}`, and the client exits with that code. A signal that reaches the client goes to the run on the server. A client that goes away kills its run.

Each call is its own connection, so calls from many processes at once never share a stream.

## What the server decides

- The server runs `api-cli --config <its config>` with the client's argv, in the client's working directory. A directory that does not exist on the server fails the call.
- The run gets the server's environment, not the client's. That is where `GH_TOKEN` or a keychain lives.
- The client may not send a global flag that changes what the server loads, writes or prints. `--config`, `--var`, `--mcp`, `--cors`, `--install-mocks`, `--download-dir`, `--debug`, `--verbose`, `--server` or `--client`. The server refuses the call with exit 2. An argument after a bare `--` is an argument, not a flag.
- `--client` must be the first flag. Every later argument goes to the server as it is.

## Lifetime

The server listens until SIGINT, SIGTERM or SIGHUP. When its stdin is a pipe, it also stops when the pipe closes. A parent process keeps the write end open for as long as the service must live. The socket is owner-only (`0600`). A stale socket from a dead server is replaced. Any other file at the path is an error.

## A contract that passes flags on

Declare each flag the wrapped program may take, and pass the set ones on with `flagArgs`:

```xml
<command name="view">
	<arg name="args" variadic="true"/>
	<flag name="json" type="string"/>
	<flag name="web" type="bool"/>
	<run><argv>gh</argv><argv>pr</argv><argv>view</argv><argv>{{spread .arg.args}}</argv><argv>{{spread (flagArgs .flag)}}</argv></run>
</command>
```

`flagArgs` sorts the flags by name and leaves out a flag at its zero value. A bool is the bare flag, and a list repeats the flag once per value.
