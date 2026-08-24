# GitHub MCP server

`github.xml` turns the GitHub REST API into an MCP server: every leaf becomes a
tool. HTTP requests and `jq` are built into `api-cli`, so there is nothing else
to install -- no `curl`, no `jq`, no GitHub CLI.

The transport is the value of `--mcp` (`stdio` suits an MCP client that spawns a
subprocess):

```sh
# stdio (for MCP clients that spawn subprocesses)
GH_TOKEN=ghp_xxx api-cli --config samples/github/github.xml --mcp stdio

# Streamable HTTP on port 8080
GH_TOKEN=ghp_xxx api-cli --config samples/github/github.xml --mcp http://:8080

# SSE on port 8080
GH_TOKEN=ghp_xxx api-cli --config samples/github/github.xml --mcp sse://:8080
```

Pass `--cors <level>` to control CORS on the HTTP and SSE transports.
`$GITHUB_TOKEN` is also accepted in place of `$GH_TOKEN`.

The same config works as a plain CLI: `api-cli --config samples/github/github.xml
repo get golang/go`.
