package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

type captured struct {
	method   string
	path     string
	rawQuery string
	headers  http.Header
	body     []byte
}

func startCapture(t *testing.T, status int, respBody string) (string, *captured) {
	t.Helper()
	cap := &captured{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cap.method = r.Method
		cap.path = r.URL.Path
		cap.rawQuery = r.URL.RawQuery
		cap.headers = r.Header.Clone()
		cap.body, _ = io.ReadAll(r.Body)
		w.WriteHeader(status)
		_, _ = io.WriteString(w, respBody)
	}))
	t.Cleanup(srv.Close)
	return srv.URL, cap
}

// execCmd builds the root from cfg, sets argv, executes, and returns the
// process exit code (as main would compute it), plus anything written to
// stdout.
func execCmd(t *testing.T, cfg *Config, argv ...string) (int, string) {
	t.Helper()
	if err := validate(cfg); err != nil {
		t.Fatalf("invalid test config: %v", err)
	}
	var out bytes.Buffer
	prevOut := httpOut
	httpOut = &out
	t.Cleanup(func() { httpOut = prevOut })
	prevCode := exitCode
	exitCode = 0
	t.Cleanup(func() { exitCode = prevCode })

	root := newRoot(cfg)
	root.SetOut(io.Discard) // suppress cobra's own output in tests
	root.SetErr(io.Discard)
	root.SetArgs(argv)
	if err := root.Execute(); err != nil {
		return 1, out.String()
	}
	return exitCode, out.String()
}

func TestGET_PathArg(t *testing.T) {
	url, cap := startCapture(t, 200, `{"ok":true}`)
	cfg := &Config{
		Name: "t",
		Defaults: Defaults{
			BaseURL: url,
			Headers: map[string]string{"X-Test": "yes"},
		},
		Commands: []Command{{
			Name: "users",
			Commands: []Command{{
				Name:    "get",
				Args:    []Arg{{Name: "id", Type: "int", Required: true}},
				Request: &Request{Method: "GET", Path: "/users/{{.id}}"},
			}},
		}},
	}
	code, body := execCmd(t, cfg, "users", "get", "42")
	if code != 0 {
		t.Errorf("exit code = %d, want 0", code)
	}
	if cap.method != "GET" || cap.path != "/users/42" {
		t.Errorf("got %s %s", cap.method, cap.path)
	}
	if cap.headers.Get("X-Test") != "yes" {
		t.Errorf("X-Test header missing: %v", cap.headers)
	}
	if body != `{"ok":true}` {
		t.Errorf("stdout = %q", body)
	}
}

func TestGET_QueryRendersAndDropsEmpties(t *testing.T) {
	url, cap := startCapture(t, 200, "")
	cfg := &Config{
		Name:     "t",
		Defaults: Defaults{BaseURL: url},
		Commands: []Command{{
			Name: "posts",
			Commands: []Command{{
				Name: "list",
				Flags: []Flag{
					{Name: "limit", Type: "int", Default: float64(5)},
					{Name: "cursor", Type: "string", Default: ""},
				},
				Request: &Request{
					Method: "GET",
					Path:   "/posts",
					Query: map[string]string{
						"_limit":  "{{.limit}}",
						"_cursor": "{{.cursor}}",
					},
				},
			}},
		}},
	}
	code, _ := execCmd(t, cfg, "posts", "list")
	if code != 0 {
		t.Fatalf("exit = %d", code)
	}
	if cap.path != "/posts" {
		t.Errorf("path = %s", cap.path)
	}
	// _cursor should be dropped because its rendered value is "".
	if cap.rawQuery != "_limit=5" {
		t.Errorf("rawQuery = %q, want _limit=5", cap.rawQuery)
	}
}

func TestPOST_BodyRenders(t *testing.T) {
	url, cap := startCapture(t, 201, `{}`)
	cfg := &Config{
		Name:     "t",
		Defaults: Defaults{BaseURL: url},
		Commands: []Command{{
			Name: "posts",
			Commands: []Command{{
				Name: "create",
				Flags: []Flag{
					{Name: "title", Type: "string", Required: true},
					{Name: "body", Short: "b", Type: "string", Required: true},
				},
				Request: &Request{
					Method: "POST",
					Path:   "/posts",
					Body:   json.RawMessage(`{"title":"{{.title}}","body":"{{.body}}","userId":1}`),
				},
			}},
		}},
	}
	code, _ := execCmd(t, cfg, "posts", "create", "--title", "hi there", "-b", `q"uoted`)
	if code != 0 {
		t.Fatalf("exit = %d", code)
	}
	if cap.method != "POST" {
		t.Errorf("method = %s", cap.method)
	}
	if cap.headers.Get("Content-Type") != "application/json" {
		t.Errorf("Content-Type = %q", cap.headers.Get("Content-Type"))
	}
	var got map[string]any
	if err := json.Unmarshal(cap.body, &got); err != nil {
		t.Fatalf("body not JSON: %v (%s)", err, cap.body)
	}
	if got["title"] != "hi there" {
		t.Errorf("title = %v", got["title"])
	}
	if got["body"] != `q"uoted` {
		t.Errorf("body = %v", got["body"])
	}
	if got["userId"] != float64(1) {
		t.Errorf("userId = %v (%T)", got["userId"], got["userId"])
	}
}

func TestHTTPErrorExitCodes(t *testing.T) {
	url, _ := startCapture(t, 404, "not found")
	cfg := &Config{
		Name:     "t",
		Defaults: Defaults{BaseURL: url},
		Commands: []Command{{
			Name:    "x",
			Request: &Request{Method: "GET", Path: "/x"},
		}},
	}
	code, _ := execCmd(t, cfg, "x")
	if code != 4 {
		t.Errorf("404 → exit %d, want 4", code)
	}
}

func TestServerErrorExitCode(t *testing.T) {
	url, _ := startCapture(t, 503, "")
	cfg := &Config{
		Name:     "t",
		Defaults: Defaults{BaseURL: url},
		Commands: []Command{{
			Name:    "x",
			Request: &Request{Method: "GET", Path: "/x"},
		}},
	}
	code, _ := execCmd(t, cfg, "x")
	if code != 5 {
		t.Errorf("503 → exit %d, want 5", code)
	}
}

func TestExampleConfigLoads(t *testing.T) {
	// Sanity check: the shipped example validates cleanly.
	cfg, err := Load("api.example.json")
	if err != nil {
		t.Fatalf("api.example.json failed to load: %v", err)
	}
	if cfg.Name == "" {
		t.Error("example config missing name")
	}
}
