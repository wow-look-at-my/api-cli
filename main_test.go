package main

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wow-look-at-my/testify/assert"
	"github.com/wow-look-at-my/testify/require"
)

// chdir switches the working directory for the duration of the test and
// restores it on cleanup. Used for config-discovery tests that need to
// control ./api.json's existence.
func chdir(t *testing.T, dir string) {
	t.Helper()
	prev, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(dir))
	t.Cleanup(func() { _ = os.Chdir(prev) })
}

func TestRun_MissingConfigReturns2(t *testing.T) {
	chdir(t, t.TempDir())
	var errOut bytes.Buffer
	code := run([]string{"whatever"}, &errOut)
	assert.Equal(t, 2, code)
	assert.Contains(t, errOut.String(), "no config found")
}

func TestRun_InvalidConfigReturns2(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "bad.json"), []byte("{not json"), 0o600))
	var errOut bytes.Buffer
	code := run([]string{"--config", filepath.Join(dir, "bad.json")}, &errOut)
	assert.Equal(t, 2, code)
}

func TestRun_HappyPath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"ok":true}`)
	}))
	t.Cleanup(srv.Close)
	dir := t.TempDir()
	cfg := `{
      "name": "t",
      "defaults": {"base_url": "` + srv.URL + `"},
      "commands": [{"name":"ping","request":{"method":"GET","path":"/"}}]
    }`
	p := filepath.Join(dir, "api.json")
	require.NoError(t, os.WriteFile(p, []byte(cfg), 0o600))

	prevOut := httpOut
	var buf bytes.Buffer
	httpOut = &buf
	t.Cleanup(func() { httpOut = prevOut })
	prevCode := exitCode
	exitCode = 0
	t.Cleanup(func() { exitCode = prevCode })

	var errOut bytes.Buffer
	code := run([]string{"--config", p, "ping"}, &errOut)
	assert.Equal(t, 0, code)
	assert.Equal(t, `{"ok":true}`, buf.String())
}

func TestRun_PicksUpCwdAPIJson(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "ok")
	}))
	t.Cleanup(srv.Close)
	dir := t.TempDir()
	cfg := `{
      "name": "t",
      "defaults": {"base_url": "` + srv.URL + `"},
      "commands": [{"name":"ping","request":{"method":"GET","path":"/"}}]
    }`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "api.json"), []byte(cfg), 0o600))
	chdir(t, dir)

	prevOut := httpOut
	httpOut = io.Discard
	t.Cleanup(func() { httpOut = prevOut })

	var errOut bytes.Buffer
	code := run([]string{"ping"}, &errOut)
	assert.Equal(t, 0, code)
}

func TestRegisterFlag_AllTypes(t *testing.T) {
	cfg := &Config{
		Name:     "t",
		Defaults: Defaults{BaseURL: "https://x.example"},
		Commands: []Command{{
			Name: "x",
			Flags: []Flag{
				{Name: "s", Type: "string", Default: "hi"},
				{Name: "b", Type: "bool", Default: true},
				{Name: "n", Type: "int", Default: float64(7)},
				{Name: "tags", Type: "string-slice", Default: []any{"a", "b"}},
				{Name: "untyped"}, // default "string" fallback
			},
			Request: &Request{Method: "GET", Path: "/"},
		}},
	}
	require.NoError(t, validate(cfg))
	root := newRoot(cfg)
	cmd, _, err := root.Find([]string{"x"})
	require.NoError(t, err)

	assert.Equal(t, "hi", cmd.Flags().Lookup("s").DefValue)
	assert.Equal(t, "true", cmd.Flags().Lookup("b").DefValue)
	assert.Equal(t, "7", cmd.Flags().Lookup("n").DefValue)
	assert.Equal(t, "[a,b]", cmd.Flags().Lookup("tags").DefValue)
	require.NotNil(t, cmd.Flags().Lookup("untyped"))
}

func TestStringSlice_PreservesCommas(t *testing.T) {
	// StringArrayVar (vs StringSliceVar) keeps commas inside values instead
	// of splitting them.
	var captured []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured = r.URL.Query()["tag"]
		w.WriteHeader(200)
	}))
	t.Cleanup(srv.Close)
	cfg := &Config{
		Name:     "t",
		Defaults: Defaults{BaseURL: srv.URL},
		Commands: []Command{{
			Name:  "x",
			Flags: []Flag{{Name: "tag", Type: "string-slice"}},
			Request: &Request{
				Method: "GET",
				Path:   "/",
				// Slice interpolates via Go's template default of "[a b c]" —
				// not useful for query; this test just verifies pflag-level
				// comma preservation via GetStringArray upstream of rendering.
			},
		}},
	}
	require.NoError(t, validate(cfg))
	root := newRoot(cfg)
	// We test the flag parsing, not templating of slices: use pflag.
	cmd, _, err := root.Find([]string{"x"})
	require.NoError(t, err)
	require.NoError(t, cmd.Flags().Parse([]string{"--tag", "a,b", "--tag", "c"}))
	got, err := cmd.Flags().GetStringArray("tag")
	require.NoError(t, err)
	assert.Equal(t, []string{"a,b", "c"}, got)
	_ = captured // unused — no HTTP call made in this test
}

func TestJoinURL_Errors(t *testing.T) {
	if _, err := joinURL("", "/x", nil); err == nil {
		t.Error("expected error for empty base_url")
	}
	if _, err := joinURL("::bad::", "/x", nil); err == nil {
		t.Error("expected error for unparseable base_url")
	}
	if _, err := joinURL("not-a-url", "/x", nil); err == nil {
		t.Error("expected error for missing scheme/host")
	}
}

func TestHeaderInjectionRejected(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))
	t.Cleanup(srv.Close)
	code := doRequest("GET", srv.URL, "/", nil, map[string]string{"X-Bad": "a\r\nInjected: yes"}, nil)
	assert.Equal(t, 1, code)
}

func TestDoRequest_TransportError(t *testing.T) {
	// Point at an address nothing listens on.
	code := doRequest("GET", "http://127.0.0.1:1", "/", nil, nil, nil)
	assert.Equal(t, 1, code)
}

func TestDoRequest_StripsEmptyQueryValues(t *testing.T) {
	var gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		w.WriteHeader(200)
	}))
	t.Cleanup(srv.Close)
	prev := httpOut
	httpOut = io.Discard
	t.Cleanup(func() { httpOut = prev })

	code := doRequest("GET", srv.URL, "/", map[string]string{"a": "", "b": "x"}, nil, nil)
	assert.Equal(t, 0, code)
	assert.Equal(t, "b=x", gotQuery)
}

func TestEnvMap_HasProcessEnv(t *testing.T) {
	key := "API_CLI_TEST_ENV_" + strings.ToUpper(t.Name())
	require.NoError(t, os.Setenv(key, "42"))
	t.Cleanup(func() { _ = os.Unsetenv(key) })
	m := envMap()
	assert.Equal(t, "42", m[key])
}
