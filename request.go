package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/itchyny/gojq"
)

// httpClient performs request-form leaves. A package var so tests can point it
// at an httptest.Server.
var httpClient = &http.Client{Timeout: 60 * time.Second}

// preparedRequest is a request with every template rendered — what actually
// goes on the wire. Both the built-in client and a <transport> program consume
// this, so the two see an identical request.
type preparedRequest struct {
	Method  string
	URL     string // includes the query string
	Body    string
	Headers []renderedHeader
}

type renderedHeader struct{ Name, Value string }

// runRequest performs a first-class HTTP request and returns its output as a
// string plus an exit code (0 on success). On an HTTP error status or a
// transport error it writes a diagnostic to errOut and returns a non-zero
// code with empty output, mirroring `curl -f`.
//
// The request travels over the built-in net/http client, or over the
// <transport> program the config selects for it (see transport.go).
//
// Output is the response body, optionally shaped by the request's jq program
// (resolved from the data context) and re-encoded as indented JSON. Without a
// <response> the raw body is returned verbatim.
func runRequest(req *Request, data map[string]any, errOut io.Writer) (string, int) {
	prepared, err := prepareRequest(req, data)
	if err != nil {
		fmt.Fprintln(errOut, "error:", err)
		return "", 1
	}

	transport, err := resolveTransport(req)
	if err != nil {
		fmt.Fprintln(errOut, "error:", err)
		return "", 1
	}

	var raw []byte
	var code int
	if transport != nil {
		out, c := runViaTransport(transport, prepared, data, errOut)
		raw, code = []byte(out), c
	} else {
		raw, code = doHTTP(prepared, errOut)
	}
	if code != 0 {
		return "", code
	}

	if req.Response == nil {
		return string(raw), 0
	}
	out, err := applyJQ(req.Response.JQ, raw, data)
	if err != nil {
		fmt.Fprintln(errOut, "error:", err)
		return "", 1
	}
	return out, 0
}

// prepareRequest renders every part of a request against the data context.
func prepareRequest(req *Request, data map[string]any) (*preparedRequest, error) {
	p := &preparedRequest{Method: strings.TrimSpace(req.Method)}
	if p.Method == "" {
		p.Method = "GET"
	}

	rawURL, err := renderString(req.URL, data)
	if err != nil {
		return nil, fmt.Errorf("render url: %w", err)
	}
	p.URL = strings.TrimSpace(rawURL)

	qs, err := buildRequestQuery(req, data)
	if err != nil {
		return nil, err
	}
	if qs != "" {
		if strings.Contains(p.URL, "?") {
			p.URL += "&" + qs
		} else {
			p.URL += "?" + qs
		}
	}

	if req.Body != "" {
		if p.Body, err = renderString(req.Body, data); err != nil {
			return nil, fmt.Errorf("render body: %w", err)
		}
	}

	if p.Headers, err = renderHeaders(req.Headers, data); err != nil {
		return nil, err
	}
	return p, nil
}

// doHTTP performs a prepared request with the built-in client and returns the
// response body. A 4xx/5xx status is a failure, like `curl -f`.
func doHTTP(p *preparedRequest, errOut io.Writer) ([]byte, int) {
	var body io.Reader
	if p.Body != "" {
		body = strings.NewReader(p.Body)
	}
	httpReq, err := http.NewRequest(p.Method, p.URL, body)
	if err != nil {
		fmt.Fprintln(errOut, "error: build request:", err)
		return nil, 1
	}
	for _, h := range p.Headers {
		httpReq.Header.Set(h.Name, h.Value)
	}

	logVerbose("request: %s %s", p.Method, p.URL)
	resp, err := httpClient.Do(httpReq)
	if err != nil {
		fmt.Fprintln(errOut, "error: request failed:", err)
		return nil, 1
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		fmt.Fprintln(errOut, "error: read response:", err)
		return nil, 1
	}
	logVerbose("request: status %d (%d bytes)", resp.StatusCode, len(raw))

	if resp.StatusCode >= 400 {
		fmt.Fprintf(errOut, "error: HTTP %d %s\n", resp.StatusCode, strings.TrimSpace(resp.Status))
		if len(raw) > 0 {
			fmt.Fprintln(errOut, strings.TrimSpace(string(raw)))
		}
		return nil, 1
	}
	return raw, 0
}

// buildRequestQuery assembles the URL-encoded query string (no leading "?")
// from the request's `from` map and explicit <param> children. Params whose
// `When` path is falsy are skipped; empty values are dropped.
func buildRequestQuery(req *Request, data map[string]any) (string, error) {
	values := url.Values{}
	if req.QueryFrom != "" {
		m := lookupPath(data, req.QueryFrom)
		if mm, ok := m.(map[string]any); ok {
			keys := sortedKeys(mm)
			for _, k := range keys {
				if err := addQueryValue(values, k, mm[k]); err != nil {
					return "", err
				}
			}
		}
	}
	for _, p := range req.Query {
		if p.When != "" && !templateTruthy(lookupPath(data, p.When)) {
			continue
		}
		name, err := renderString(p.Name, data)
		if err != nil {
			return "", fmt.Errorf("render query name: %w", err)
		}
		val, err := renderString(p.Value, data)
		if err != nil {
			return "", fmt.Errorf("render query %q: %w", name, err)
		}
		if val != "" {
			values.Add(name, val)
		}
	}
	return values.Encode(), nil
}

// renderHeaders renders the declared headers in order, dropping those whose
// `When` path is falsy and those whose name renders empty.
func renderHeaders(headers []Header, data map[string]any) ([]renderedHeader, error) {
	var out []renderedHeader
	for _, h := range headers {
		if h.When != "" && !templateTruthy(lookupPath(data, h.When)) {
			continue
		}
		name, err := renderString(h.Name, data)
		if err != nil {
			return nil, fmt.Errorf("render header name: %w", err)
		}
		val, err := renderString(h.Value, data)
		if err != nil {
			return nil, fmt.Errorf("render header %q: %w", name, err)
		}
		if name == "" {
			continue
		}
		out = append(out, renderedHeader{Name: name, Value: val})
	}
	return out, nil
}

// applyJQ runs the jq program (resolved from the data context via jqPath) over
// the JSON body and returns the result(s) as indented JSON. An empty program
// pretty-prints the body unchanged.
func applyJQ(jqPath string, raw []byte, data map[string]any) (string, error) {
	var input any
	if err := json.Unmarshal(raw, &input); err != nil {
		// Not JSON: return the raw body untouched.
		return string(raw), nil
	}

	program := ""
	if jqPath != "" {
		if s, ok := lookupPath(data, jqPath).(string); ok {
			program = strings.TrimSpace(s)
		}
	}
	if program == "" {
		return marshalJSON(input)
	}

	query, err := gojq.Parse(program)
	if err != nil {
		return "", fmt.Errorf("parse jq %q: %w", program, err)
	}
	code, err := gojq.Compile(query)
	if err != nil {
		return "", fmt.Errorf("compile jq: %w", err)
	}

	var results []any
	iter := code.Run(input)
	for {
		v, ok := iter.Next()
		if !ok {
			break
		}
		if e, ok := v.(error); ok {
			if _, halt := e.(*gojq.HaltError); halt {
				break
			}
			return "", fmt.Errorf("jq: %w", e)
		}
		results = append(results, v)
	}

	switch len(results) {
	case 0:
		return "", nil
	case 1:
		return marshalJSON(results[0])
	default:
		parts := make([]string, len(results))
		for i, r := range results {
			s, err := marshalJSON(r)
			if err != nil {
				return "", err
			}
			parts[i] = s
		}
		return strings.Join(parts, "\n"), nil
	}
}

func marshalJSON(v any) (string, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return "", fmt.Errorf("encode json: %w", err)
	}
	return strings.TrimRight(buf.String(), "\n"), nil
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
