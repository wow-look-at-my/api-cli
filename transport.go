package main

import (
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
)

// builtinTransportName selects the built-in net/http client. Reserved as a
// <transport> name so a single request can opt out of a default transport with
// transport="http" — for the public endpoint in an otherwise internal API.
const builtinTransportName = "http"

// The active transport registry. Config data rather than state, but a process
// loads exactly one config, so this mirrors how httpClient and execStdout are
// held — and spares every call site between runLeaf and runRequest a parameter
// it would only pass along.
var (
	transports       = map[string]*Transport{}
	defaultTransport string
)

// buildTransports parses the <transports> registry. Kept here rather than in
// xmlsource.go so the whole transport feature reads in one place.
func buildTransports(n *xnode) (map[string]*Transport, error) {
	if err := checkAttrs(n); err != nil {
		return nil, err
	}
	out := map[string]*Transport{}
	for _, child := range n.Children() {
		if child.Name() != "transport" {
			return nil, fmt.Errorf("<transports>: unexpected child element <%s>", child.Name())
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
	name := strings.TrimSpace(n.Attr("name"))
	if name == "" {
		return nil, fmt.Errorf("<transport>: name= is required")
	}
	t := &Transport{Name: name, Default: n.Attr("default") == "true"}
	for _, child := range n.Children() {
		switch child.Name() {
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
			return nil, fmt.Errorf("<transport %q>: unexpected child element <%s>", name, child.Name())
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

// resolveTransport picks the transport for a request: its own transport=, else
// the registry default, else the built-in client (a nil return).
//
// There is deliberately no runtime override. How a request reaches its endpoint
// is a property of that endpoint, not a user preference — sending an internal
// API's request over the built-in client would just fetch an SSO page.
func resolveTransport(req *Request) (*Transport, error) {
	return resolveTransportNamed(req.Transport)
}

// resolveTransportNamed is the selection itself, shared by requests and
// downloads: an explicit name, else the registry default, else the built-in
// client (a nil return).
func resolveTransportNamed(name string) (*Transport, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		name = defaultTransport
	}
	if name == "" || name == builtinTransportName {
		return nil, nil
	}
	t := transports[name]
	if t == nil {
		// validate() rejects an unknown transport= at load time, so reaching
		// this means the registry was never published for this config. Fail
		// rather than quietly sending the request over the built-in client.
		return nil, fmt.Errorf("transport %q is not registered (known: %s)", name, knownTransports())
	}
	return t, nil
}

// downloadTransport is a transport resolved for one download: the program's
// argv with every template already rendered, plus where to run it. The queue
// executes this on a worker long after the leaf's data context is gone, so
// nothing here may still need rendering.
type downloadTransport struct {
	Name  string
	Argv  []string
	Cwd   string
	Stdin string
}

// prepareDownloadTransport picks the transport for a download and renders its
// command now. A nil return means the built-in client carries this one.
//
// The program sees the same `.request` context a request-form transport sees —
// method, url, headers, header_lines — so one program serves both. The
// difference is on the way back: a download's stdout is streamed to the file
// rather than buffered as a response body, because the whole point of the file
// is that it need not fit in memory.
func prepareDownloadTransport(name, rawURL string, headers []renderedHeader, ctx map[string]any) (*downloadTransport, error) {
	t, err := resolveTransportNamed(name)
	if err != nil || t == nil {
		return nil, err
	}

	p := &preparedRequest{Method: http.MethodGet, URL: rawURL, Headers: headers}
	tctx := p.context(ctx)

	cwd, err := renderCwd(t.Cwd, tctx)
	if err != nil {
		return nil, fmt.Errorf("transport %q: render cwd: %w", t.Name, err)
	}
	// A download has no body, so a transport that declares no <stdin> gets
	// nothing — never the user's terminal.
	stdin := ""
	if t.StdinSet {
		if stdin, err = renderString(t.Stdin, tctx); err != nil {
			return nil, fmt.Errorf("transport %q: render stdin: %w", t.Name, err)
		}
	}
	argv, err := resolveArgv(t.Command, tctx)
	if err != nil {
		return nil, fmt.Errorf("transport %q: %w", t.Name, err)
	}
	return &downloadTransport{Name: t.Name, Argv: argv, Cwd: cwd, Stdin: stdin}, nil
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
