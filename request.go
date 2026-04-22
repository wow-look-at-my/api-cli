package main

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
)

// httpClient is the package-level HTTP client used for all outbound calls.
// Tests may replace it.
var httpClient = http.DefaultClient

// httpOut is the writer that receives the response body. Defaults to stdout;
// tests may replace it.
var httpOut io.Writer = os.Stdout

// doRequest builds and executes the outbound HTTP request. It streams the
// response body to httpOut and returns an exit code:
//
//	0  on 2xx
//	4  on 4xx
//	5  on 5xx
//	1  on transport/config error
func doRequest(method, baseURL, renderedPath string, query, headers map[string]string, body []byte) int {
	fullURL, err := joinURL(baseURL, renderedPath, query)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}

	var bodyReader io.Reader
	if len(body) > 0 {
		bodyReader = bytes.NewReader(body)
	}

	req, err := http.NewRequest(strings.ToUpper(method), fullURL, bodyReader)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: build request: %v\n", err)
		return 1
	}

	for k, v := range headers {
		if strings.ContainsAny(v, "\r\n") {
			fmt.Fprintf(os.Stderr, "error: header %q contains CR/LF\n", k)
			return 1
		}
		req.Header.Set(k, v)
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	defer resp.Body.Close()

	if _, err := io.Copy(httpOut, resp.Body); err != nil {
		fmt.Fprintf(os.Stderr, "error: read response: %v\n", err)
		return 1
	}

	switch {
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		return 0
	case resp.StatusCode >= 400 && resp.StatusCode < 500:
		return 4
	case resp.StatusCode >= 500 && resp.StatusCode < 600:
		return 5
	default:
		return 1
	}
}

func joinURL(base, path string, query map[string]string) (string, error) {
	if strings.TrimSpace(base) == "" {
		return "", fmt.Errorf("base_url is empty")
	}
	u, err := url.Parse(base)
	if err != nil {
		return "", fmt.Errorf("parse base_url %q: %w", base, err)
	}
	if u.Scheme == "" || u.Host == "" {
		return "", fmt.Errorf("base_url %q must include scheme and host", base)
	}
	u = u.JoinPath(path)

	if len(query) > 0 {
		q := u.Query()
		for k, v := range query {
			if v == "" {
				continue // drop empty-string renderings (optional flags with default "")
			}
			q.Add(k, v)
		}
		u.RawQuery = q.Encode()
	}
	return u.String(), nil
}
