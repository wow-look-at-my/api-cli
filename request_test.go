package main

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func swapHTTPClient(t *testing.T, srv *httptest.Server) {
	t.Helper()
	prev := httpClient
	httpClient = srv.Client()
	t.Cleanup(func() { httpClient = prev })
}

func TestRunRequest_GETQueryHeadersAndJQ(t *testing.T) {
	var gotPath, gotQuery, gotAuth, gotUA string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotQuery = r.URL.RawQuery
		gotAuth = r.Header.Get("Authorization")
		gotUA = r.Header.Get("User-Agent")
		_, _ = w.Write([]byte(`{"login":"octo","followers_url":"u","followers":10}`))
	}))
	defer srv.Close()
	swapHTTPClient(t, srv)

	req := &Request{
		Method: "GET",
		URL:    srv.URL + "/users/{{.arg.u}}",
		Query: []Param{
			{Name: "per_page", Value: "{{.flag.n}}"},
			{Name: "skip", Value: "x", When: "var.never"},
			{Name: "empty", Value: ""},
		},
		Headers: []Header{
			{Name: "User-Agent", Value: "test-agent"},
			{Name: "Authorization", Value: "Bearer {{.var.token}}", When: "var.token"},
		},
		Response: &Response{JQ: "var.filter"},
	}
	data := map[string]any{
		"arg":  map[string]any{"u": "octo"},
		"flag": map[string]any{"n": "5"},
		"var": map[string]any{
			"token":  "tok",
			"filter": `with_entries(select(.key | endswith("url") | not))`,
		},
	}
	out, code := runRequest(req, data, io.Discard)
	require.Equal(t, 0, code)
	assert.Equal(t, "/users/octo", gotPath)
	assert.Contains(t, gotQuery, "per_page=5")
	assert.NotContains(t, gotQuery, "skip")  // When falsy
	assert.NotContains(t, gotQuery, "empty") // empty value dropped
	assert.Equal(t, "Bearer tok", gotAuth)
	assert.Equal(t, "test-agent", gotUA)
	assert.Contains(t, out, `"login": "octo"`)
	assert.NotContains(t, out, "followers_url")
	assert.Contains(t, out, `"followers": 10`)
}

func TestRunRequest_HeaderWhenFalsySkips(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()
	swapHTTPClient(t, srv)

	req := &Request{
		Method:  "GET",
		URL:     srv.URL + "/x",
		Headers: []Header{{Name: "Authorization", Value: "Bearer x", When: "var.token"}},
	}
	_, code := runRequest(req, map[string]any{"var": map[string]any{"token": ""}}, io.Discard)
	require.Equal(t, 0, code)
	assert.Empty(t, gotAuth)
}

func TestRunRequest_RawBodyNoResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("plain text body"))
	}))
	defer srv.Close()
	swapHTTPClient(t, srv)

	out, code := runRequest(&Request{Method: "GET", URL: srv.URL + "/raw"}, map[string]any{}, io.Discard)
	require.Equal(t, 0, code)
	assert.Equal(t, "plain text body", out)
}

func TestRunRequest_QueryFromMap(t *testing.T) {
	var gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()
	swapHTTPClient(t, srv)

	req := &Request{Method: "GET", URL: srv.URL + "/x", QueryFrom: "entry.query"}
	data := map[string]any{"entry": map[string]any{"query": map[string]any{"a": "1", "b": "", "c": "two words"}}}
	_, code := runRequest(req, data, io.Discard)
	require.Equal(t, 0, code)
	assert.Contains(t, gotQuery, "a=1")
	assert.NotContains(t, gotQuery, "b=")
	assert.Contains(t, gotQuery, "c=two+words")
}

func TestRunRequest_POSTBody(t *testing.T) {
	var gotBody, gotMethod, gotCT string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		gotMethod = r.Method
		gotCT = r.Header.Get("Content-Type")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()
	swapHTTPClient(t, srv)

	req := &Request{
		Method:  "POST",
		URL:     srv.URL + "/things",
		Headers: []Header{{Name: "Content-Type", Value: "application/json"}},
		Body:    `{"name":{{ .flag.name | toJson }}}`,
	}
	data := map[string]any{"flag": map[string]any{"name": "ada"}}
	out, code := runRequest(req, data, io.Discard)
	require.Equal(t, 0, code)
	assert.Equal(t, "POST", gotMethod)
	assert.Equal(t, "application/json", gotCT)
	assert.JSONEq(t, `{"name":"ada"}`, gotBody)
	assert.Contains(t, out, `"ok": true`)
}

func TestRunRequest_HTTPErrorStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"message":"not found"}`))
	}))
	defer srv.Close()
	swapHTTPClient(t, srv)

	var errBuf bytes.Buffer
	out, code := runRequest(&Request{Method: "GET", URL: srv.URL + "/missing"}, map[string]any{}, &errBuf)
	assert.NotEqual(t, 0, code)
	assert.Empty(t, out)
	assert.Contains(t, errBuf.String(), "HTTP 404")
	assert.Contains(t, errBuf.String(), "not found")
}

func TestRunRequest_BadURLTemplate(t *testing.T) {
	var errBuf bytes.Buffer
	out, code := runRequest(&Request{Method: "GET", URL: "{{ .broken"}, map[string]any{}, &errBuf)
	assert.Equal(t, 1, code)
	assert.Empty(t, out)
	assert.Contains(t, errBuf.String(), "url")
}

func TestRunRequest_TransportError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	swapHTTPClient(t, srv)
	srv.Close() // close so the connection is refused

	var errBuf bytes.Buffer
	_, code := runRequest(&Request{Method: "GET", URL: srv.URL + "/x"}, map[string]any{}, &errBuf)
	assert.Equal(t, 1, code)
	assert.Contains(t, errBuf.String(), "request failed")
}

func TestApplyJQ_Identity(t *testing.T) {
	out, err := applyJQ("", []byte(`{"b":2,"a":1}`), map[string]any{})
	require.NoError(t, err)
	assert.Contains(t, out, `"a": 1`)
	assert.Contains(t, out, `"b": 2`)
}

func TestApplyJQ_NonJSONPassesThrough(t *testing.T) {
	out, err := applyJQ("var.f", []byte("not json at all"), map[string]any{"var": map[string]any{"f": "."}})
	require.NoError(t, err)
	assert.Equal(t, "not json at all", out)
}

func TestApplyJQ_MultipleOutputs(t *testing.T) {
	data := map[string]any{"var": map[string]any{"f": ".[]"}}
	out, err := applyJQ("var.f", []byte(`[1,2,3]`), data)
	require.NoError(t, err)
	assert.Equal(t, "1\n2\n3", out)
}

func TestApplyJQ_EmptyResult(t *testing.T) {
	data := map[string]any{"var": map[string]any{"f": ".[] | select(. > 10)"}}
	out, err := applyJQ("var.f", []byte(`[1,2,3]`), data)
	require.NoError(t, err)
	assert.Empty(t, out)
}

func TestApplyJQ_ParseError(t *testing.T) {
	data := map[string]any{"var": map[string]any{"f": "this is ( not jq"}}
	_, err := applyJQ("var.f", []byte(`{}`), data)
	require.Error(t, err)
}

func TestApplyJQ_RuntimeError(t *testing.T) {
	// .a on an array is a runtime error in jq.
	data := map[string]any{"var": map[string]any{"f": ".a"}}
	_, err := applyJQ("var.f", []byte(`[1,2]`), data)
	require.Error(t, err)
}
