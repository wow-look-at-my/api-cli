# Requests

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

**A `jq=` program needs a JSON body.** A body that is not JSON fails the request, and the error shows the start of that body. The excerpt usually reveals an SSO login page or a proxy error. Without `jq=`, a body that is not JSON passes through unchanged.

A status of 400 or more prints the body to stderr and exits non-zero, like `curl -f`. The root `<run>` usually holds the shared request. A leaf's own `<run>` overrides it, for example with a `POST` or a raw-body download.

`allow-status=` names the error statuses that are an answer instead of a failure. The body then reaches the caller with exit code 0, and a step stores it at `.result.<name>` as any other step output.

```xml
<steps>
	<step name="primary">
		<run><request allow-status="404"><url><value name="var.api"/>/a/<value name="arg.id" as="urlpath"/></url></request></run>
	</step>
	<step name="fallback" when="{{ not .result.primary.id }}">
		<run><request><url><value name="var.api"/>/b/<value name="arg.id" as="urlpath"/></url></request></run>
	</step>
</steps>
```

Take one lookup that may miss, followed by a second lookup on the same id. The list accepts `404` or `404,410`, and every entry must be in the 400 to 599 range. It needs the built-in client, because a `<transport>` program reports an exit code rather than a status. A request that asks for both fails and says so.
