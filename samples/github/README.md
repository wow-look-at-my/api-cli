# GitHub MCP server

Pre-built Alpine image containing `api-cli`, `curl`, `jq`, and `github.yaml`.
The transport is specified as the container command (default `stdio`):

```sh
# stdio (default — for MCP clients that spawn subprocesses)
docker run --rm -e GH_TOKEN=ghp_xxx ghcr.io/wow-look-at-my/github

# Streamable HTTP on port 8080
docker run --rm -p 127.0.0.1:8080:8080 -e GH_TOKEN=ghp_xxx ghcr.io/wow-look-at-my/github http://:8080

# SSE on port 8080
docker run --rm -p 127.0.0.1:8080:8080 -e GH_TOKEN=ghp_xxx ghcr.io/wow-look-at-my/github sse://:8080
```

Pass `--cors <level>` after the transport to control CORS.
`$GITHUB_TOKEN` is also accepted in place of `$GH_TOKEN`.
