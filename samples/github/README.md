# GitHub MCP server

`github.xml` turns the GitHub REST API into an MCP server. Each leaf becomes a tool. `api-cli` makes the HTTP requests and runs `jq` itself, so you install nothing else -- no `curl`, no `jq`, no GitHub CLI.

The value of `--mcp` selects how a client reaches the server. Use `stdio` for a client that starts a subprocess.

```sh
# stdio (for MCP clients that spawn subprocesses)
GH_TOKEN=ghp_xxx api-cli --config samples/github/github.xml --mcp stdio

# Streamable HTTP on port 8080
GH_TOKEN=ghp_xxx api-cli --config samples/github/github.xml --mcp http://:8080

# SSE on port 8080
GH_TOKEN=ghp_xxx api-cli --config samples/github/github.xml --mcp sse://:8080
```

Give `--cors <level>` to control CORS on the HTTP and SSE servers. `$GITHUB_TOKEN` also works in place of `$GH_TOKEN`.

The same config works as a plain CLI: `api-cli --config samples/github/github.xml repo get golang/go`.
