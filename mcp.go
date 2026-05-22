package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// runMCP starts an MCP server using the given transport spec.
// transport is one of:
//   - "stdio"              MCP over stdin/stdout
//   - "http://host:port"   MCP over Streamable HTTP (POST /)
//   - "sse://host:port"    MCP over HTTP+SSE (GET /sse + POST /message)
//
// corsLevel controls cross-origin handling for the HTTP and SSE
// transports; it is ignored for stdio.
func runMCP(transport string, cfg *Config, corsLevel CorsLevel) int {
	srv := buildMCPServer(cfg)
	ctx := context.Background()
	switch {
	case transport == "stdio":
		if err := srv.Run(ctx, &mcp.StdioTransport{}); err != nil {
			fmt.Fprintln(execStderr, "error:", err)
			return 1
		}
		return 0
	case strings.HasPrefix(transport, "http://"):
		addr := strings.TrimPrefix(transport, "http://")
		if addr == "" {
			fmt.Fprintln(execStderr, "error: --mcp http:// requires an address, e.g. http://127.0.0.1:8080")
			return 2
		}
		fmt.Fprintf(execStderr, "MCP HTTP server listening on http://%s (cors=%s)\n", addr, corsLevel)
		mcpH := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return srv }, nil)
		handler := withCORS(withHealthEndpoint(mcpH), corsLevel, addr)
		srv2 := &http.Server{Addr: addr, Handler: handler, ReadHeaderTimeout: 30 * time.Second, IdleTimeout: 120 * time.Second}
		if err := srv2.ListenAndServe(); err != nil {
			fmt.Fprintln(execStderr, "error:", err)
			return 1
		}
		return 0
	case strings.HasPrefix(transport, "sse://"):
		addr := strings.TrimPrefix(transport, "sse://")
		if addr == "" {
			fmt.Fprintln(execStderr, "error: --mcp sse:// requires an address, e.g. sse://127.0.0.1:8080")
			return 2
		}
		fmt.Fprintf(execStderr, "MCP SSE server listening on http://%s (cors=%s)\n", addr, corsLevel)
		mcpH := mcp.NewSSEHandler(func(*http.Request) *mcp.Server { return srv }, nil)
		handler := withCORS(withHealthEndpoint(mcpH), corsLevel, addr)
		srv2 := &http.Server{Addr: addr, Handler: handler, ReadHeaderTimeout: 30 * time.Second, IdleTimeout: 120 * time.Second}
		if err := srv2.ListenAndServe(); err != nil {
			fmt.Fprintln(execStderr, "error:", err)
			return 1
		}
		return 0
	default:
		fmt.Fprintf(execStderr, "error: unknown MCP transport %q; must be \"stdio\", \"http://<addr>\", or \"sse://<addr>\"\n", transport)
		return 2
	}
}

// mcpLeaf is a leaf command with all inherited context fully resolved.
type mcpLeaf struct {
	name      string
	node      Command
	vars      map[string]any
	cmdTmpl   *Cmd
	cwdTmpl   string
	stdinTmpl string
	formatRef *FormatRef
	formats   map[string]*Format
}

// buildMCPServer creates an MCP server with one tool per leaf command.
func buildMCPServer(cfg *Config) *mcp.Server {
	srv := mcp.NewServer(&mcp.Implementation{Name: cfg.Name, Version: "1.0.0"}, nil)
	for _, leaf := range collectMCPLeaves(cfg.Commands, "", cfg.Vars, cfg.Command, cfg.Cwd, cfg.Stdin, nil, cfg.Formats) {
		l := leaf // capture for closure
		srv.AddTool(
			&mcp.Tool{
				Name:        l.name,
				Description: l.node.Description,
				InputSchema: buildToolInputSchema(l.node),
			},
			func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				var args map[string]any
				if len(req.Params.Arguments) > 0 {
					if err := json.Unmarshal(req.Params.Arguments, &args); err != nil {
						return &mcp.CallToolResult{
							Content: []mcp.Content{&mcp.TextContent{Text: "error: " + err.Error()}},
							IsError: true,
						}, nil
					}
				}
				if args == nil {
					args = map[string]any{}
				}
				output, isErr := mcpExecLeaf(&l, args)
				return &mcp.CallToolResult{
					Content: []mcp.Content{&mcp.TextContent{Text: output}},
					IsError: isErr,
				}, nil
			},
		)
	}
	return srv
}

// withHealthEndpoint wraps an HTTP handler to also serve GET /health.
// The health response is always 200 OK with {"status":"ok"}.
func withHealthEndpoint(h http.Handler) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"status":"ok"}`)
	})
	mux.Handle("/", h)
	return mux
}

func collectMCPLeaves(cmds []Command, prefix string, vars map[string]any, cmd *Cmd, cwd, stdin string, inheritedFormat *FormatRef, formats map[string]*Format) []mcpLeaf {
	var out []mcpLeaf
	for _, c := range cmds {
		name := c.Name
		if prefix != "" {
			name = prefix + "_" + c.Name
		}
		effVars := mergeVars(vars, c.Vars)
		effCmd := cmd
		if c.Command.Defined() {
			effCmd = c.Command
		}
		effCwd := cwd
		if c.Cwd != "" {
			effCwd = c.Cwd
		}
		effStdin := stdin
		if c.Stdin != "" {
			effStdin = c.Stdin
		}
		effFormat := inheritedFormat
		if c.Format.Defined() {
			effFormat = c.Format
		}
		if len(c.Commands) == 0 {
			out = append(out, mcpLeaf{
				name:      name,
				node:      c,
				vars:      effVars,
				cmdTmpl:   effCmd,
				cwdTmpl:   effCwd,
				stdinTmpl: effStdin,
				formatRef: effFormat,
				formats:   formats,
			})
		} else {
			out = append(out, collectMCPLeaves(c.Commands, name, effVars, effCmd, effCwd, effStdin, effFormat, formats)...)
		}
	}
	return out
}

// buildToolInputSchema returns a JSON Schema object for the tool's arguments.
func buildToolInputSchema(node Command) map[string]any {
	props := make(map[string]any, len(node.Args)+len(node.Flags))
	var required []string

	for _, a := range node.Args {
		var prop map[string]any
		if a.Variadic {
			itemType := "string"
			if a.Type == "int" {
				itemType = "integer"
			}
			prop = map[string]any{
				"type":  "array",
				"items": map[string]any{"type": itemType},
			}
		} else if a.Type == "int" {
			prop = map[string]any{"type": "integer"}
		} else {
			prop = map[string]any{"type": "string"}
		}
		if a.Description != "" {
			prop["description"] = a.Description
		}
		props[a.Name] = prop
		if a.Required {
			required = append(required, a.Name)
		}
	}

	for _, f := range node.Flags {
		var prop map[string]any
		switch f.Type {
		case "bool":
			prop = map[string]any{"type": "boolean"}
		case "int":
			prop = map[string]any{"type": "integer"}
		case "string-slice":
			prop = map[string]any{
				"type":  "array",
				"items": map[string]any{"type": "string"},
			}
		default: // "" or "string"
			prop = map[string]any{"type": "string"}
		}
		if f.Description != "" {
			prop["description"] = f.Description
		}
		props[f.Name] = prop
		if f.Required {
			required = append(required, f.Name)
		}
	}

	schema := map[string]any{
		"type":                 "object",
		"properties":           props,
		"additionalProperties": false,
	}
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema
}
