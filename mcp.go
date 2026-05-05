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

// findMcpFlag walks argv looking for --mcp=<value> or --mcp <value>.
func findMcpFlag(args []string) string {
	for i := 0; i < len(args); i++ {
		a := args[i]
		if strings.HasPrefix(a, "--mcp=") {
			return strings.TrimPrefix(a, "--mcp=")
		}
		if a == "--mcp" && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}

// runMCP starts an MCP server using the given transport spec.
// transport is one of:
//   - "stdio"              MCP over stdin/stdout
//   - "http://host:port"   MCP over Streamable HTTP (POST /)
//   - "sse://host:port"    MCP over HTTP+SSE (GET /sse + POST /message)
func runMCP(transport string, cfg *Config) int {
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
		fmt.Fprintf(execStderr, "MCP HTTP server listening on http://%s\n", addr)
		cop := &http.CrossOriginProtection{}
		cop.AddInsecureBypassPattern("/")
		h := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return srv }, &mcp.StreamableHTTPOptions{
			DisableLocalhostProtection: true,
			CrossOriginProtection:      cop,
		})
		srv2 := &http.Server{Addr: addr, Handler: h, ReadHeaderTimeout: 30 * time.Second, IdleTimeout: 120 * time.Second}
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
		fmt.Fprintf(execStderr, "MCP SSE server listening on http://%s\n", addr)
		h := mcp.NewSSEHandler(func(*http.Request) *mcp.Server { return srv }, nil)
		srv2 := &http.Server{Addr: addr, Handler: h, ReadHeaderTimeout: 30 * time.Second, IdleTimeout: 120 * time.Second}
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
}

// buildMCPServer creates an MCP server with one tool per leaf command.
func buildMCPServer(cfg *Config) *mcp.Server {
	srv := mcp.NewServer(&mcp.Implementation{Name: cfg.Name, Version: "1.0.0"}, nil)
	for _, leaf := range collectMCPLeaves(cfg.Commands, "", cfg.Vars, cfg.Command, cfg.Cwd, cfg.Stdin) {
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

func collectMCPLeaves(cmds []Command, prefix string, vars map[string]any, cmd *Cmd, cwd, stdin string) []mcpLeaf {
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
		if len(c.Commands) == 0 {
			out = append(out, mcpLeaf{
				name:      name,
				node:      c,
				vars:      effVars,
				cmdTmpl:   effCmd,
				cwdTmpl:   effCwd,
				stdinTmpl: effStdin,
			})
		} else {
			out = append(out, collectMCPLeaves(c.Commands, name, effVars, effCmd, effCwd, effStdin)...)
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
