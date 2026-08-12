package main

import (
	"fmt"
	"io"
	"sort"
	"strings"
)

// builtinTransportName selects the built-in net/http client. Reserved: a
// <transport> may not take this name, and --transport=http forces the built-in
// client even when the config declares a default transport.
const builtinTransportName = "http"

// The active transport registry. Config data rather than state, but a process
// loads exactly one config, so this mirrors how httpClient and execStdout are
// held — and spares every call site between runLeaf and runRequest a parameter
// it would only pass along.
var (
	transports        = map[string]*Transport{}
	defaultTransport  string
	transportOverride string // --transport, captured per invocation in runLeaf
)

// buildTransports parses the <transports> registry. Kept here rather than in
// xmlsource.go so the whole transport feature reads in one place.
func buildTransports(n *xnode) (map[string]*Transport, error) {
	if err := checkAttrs(n); err != nil {
		return nil, err
	}
	out := map[string]*Transport{}
	for _, child := range n.children() {
		if child.name != "transport" {
			return nil, fmt.Errorf("<transports>: unexpected child element <%s>", child.name)
		}
		t, err := buildTransport(child)
		if err != nil {
			return nil, err
		}
		if _, dup := out[t.Name]; dup {
			return nil, fmt.Errorf("<transport>: duplicate name %q", t.Name)
		}
		out[t.Name] = t
	}
	return out, nil
}

func buildTransport(n *xnode) (*Transport, error) {
	if err := checkAttrs(n, "name", "default"); err != nil {
		return nil, err
	}
	name := strings.TrimSpace(n.attr("name"))
	if name == "" {
		return nil, fmt.Errorf("<transport>: name= is required")
	}
	t := &Transport{Name: name, Default: n.attr("default") == "true"}
	for _, child := range n.children() {
		switch child.name {
		case "run":
			cmd, req, err := buildRun(child)
			if err != nil {
				return nil, err
			}
			if req != nil {
				return nil, fmt.Errorf("<transport %q>: <run> must be a command; a transport is what performs a request, so it cannot be one", name)
			}
			t.Command = cmd
		case "cwd":
			s, err := compileTextElem(child)
			if err != nil {
				return nil, err
			}
			t.Cwd = s
		case "stdin":
			s, err := compileTextElem(child)
			if err != nil {
				return nil, err
			}
			t.Stdin, t.StdinSet = s, true
		default:
			return nil, fmt.Errorf("<transport %q>: unexpected child element <%s>", name, child.name)
		}
	}
	return t, nil
}

// installTransports publishes a config's transport registry. Called by every
// path that turns a config into runnable commands (newRoot, buildMCPServer),
// before any leaf runs.
func installTransports(cfg *Config) {
	transports = map[string]*Transport{}
	defaultTransport = ""
	transportOverride = "" // runLeaf re-reads --transport per invocation
	if cfg == nil {
		return
	}
	for name, t := range cfg.Transports {
		t.Name = name
		transports[name] = t
		if t.Default {
			defaultTransport = name
		}
	}
}

// resolveTransport picks the transport for a request. Precedence: --transport,
// then the request's own transport=, then the registry default, then the
// built-in client (a nil return).
func resolveTransport(req *Request) (*Transport, error) {
	name := strings.TrimSpace(req.Transport)
	if transportOverride != "" {
		name = transportOverride
	}
	if name == "" {
		name = defaultTransport
	}
	if name == "" || name == builtinTransportName {
		return nil, nil
	}
	t := transports[name]
	if t == nil {
		return nil, fmt.Errorf("unknown transport %q (known: %s)", name, knownTransports())
	}
	return t, nil
}

func knownTransports() string {
	names := make([]string, 0, len(transports)+1)
	names = append(names, builtinTransportName)
	for name := range transports {
		names = append(names, name)
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}

// runViaTransport hands a prepared request to the transport program and
// returns its stdout as the response body. A non-zero exit is a failed
// request: the program's own stderr has already reached the user.
func runViaTransport(t *Transport, p *preparedRequest, data map[string]any, errOut io.Writer) (string, int) {
	ctx := p.context(data)

	cwd, err := renderCwd(t.Cwd, ctx)
	if err != nil {
		fmt.Fprintf(errOut, "error: transport %q: render cwd: %v\n", t.Name, err)
		return "", 1
	}

	stdin := p.Body
	if t.StdinSet {
		if stdin, err = renderString(t.Stdin, ctx); err != nil {
			fmt.Fprintf(errOut, "error: transport %q: render stdin: %v\n", t.Name, err)
			return "", 1
		}
	}

	logVerbose("transport %q: %s %s", t.Name, p.Method, p.URL)
	out, code := captureExecTo(t.Command, cwd, stdin, ctx, errOut)
	if code != 0 {
		fmt.Fprintf(errOut, "error: transport %q exited %d\n", t.Name, code)
		return "", code
	}
	logVerbose("transport %q: %d bytes", t.Name, len(out))
	return out, 0
}

// context layers the rendered request onto the leaf's data context, so a
// transport's argv reads .request.url the same way any command reads .arg.x.
func (p *preparedRequest) context(data map[string]any) map[string]any {
	headers := make(map[string]string, len(p.Headers))
	lines := make([]string, 0, len(p.Headers))
	for _, h := range p.Headers {
		headers[h.Name] = h.Value
		lines = append(lines, h.Name+": "+h.Value)
	}
	ctx := make(map[string]any, len(data)+1)
	for k, v := range data {
		ctx[k] = v
	}
	ctx["request"] = map[string]any{
		"method":       p.Method,
		"url":          p.URL,
		"body":         p.Body,
		"headers":      headers,
		"header_lines": lines,
	}
	return ctx
}
