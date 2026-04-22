package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"github.com/wow-look-at-my/testify/assert"
	"github.com/wow-look-at-my/testify/require"
)

type captured struct {
	method		string
	path		string
	rawQuery	string
	headers		http.Header
	body		[]byte
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
	require.NoError(t, validate(cfg))

	var out bytes.Buffer
	prevOut := httpOut
	httpOut = &out
	t.Cleanup(func() { httpOut = prevOut })
	prevCode := exitCode
	exitCode = 0
	t.Cleanup(func() { exitCode = prevCode })

	root := newRoot(cfg)
	root.SetOut(io.Discard)	// suppress cobra's own output in tests
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
		Name:	"t",
		Defaults: Defaults{
			BaseURL:	url,
			Headers:	map[string]string{"X-Test": "yes"},
		},
		Commands: []Command{{
			Name:	"users",
			Commands: []Command{{
				Name:		"get",
				Args:		[]Arg{{Name: "id", Type: "int", Required: true}},
				Request:	&Request{Method: "GET", Path: "/users/{{.id}}"},
			}},
		}},
	}
	code, body := execCmd(t, cfg, "users", "get", "42")
	assert.Equal(t, 0, code)

	assert.False(t, cap.method != "GET" || cap.path != "/users/42")

	assert.Equal(t, "yes", cap.headers.Get("X-Test"))

	assert.Equal(t, `{"ok":true}`, body)

}

func TestGET_QueryRendersAndDropsEmpties(t *testing.T) {
	url, cap := startCapture(t, 200, "")
	cfg := &Config{
		Name:		"t",
		Defaults:	Defaults{BaseURL: url},
		Commands: []Command{{
			Name:	"posts",
			Commands: []Command{{
				Name:	"list",
				Flags: []Flag{
					{Name: "limit", Type: "int", Default: float64(5)},
					{Name: "cursor", Type: "string", Default: ""},
				},
				Request: &Request{
					Method:	"GET",
					Path:	"/posts",
					Query: map[string]string{
						"_limit":	"{{.limit}}",
						"_cursor":	"{{.cursor}}",
					},
				},
			}},
		}},
	}
	code, _ := execCmd(t, cfg, "posts", "list")
	require.Equal(t, 0, code)

	assert.Equal(t, "/posts", cap.path)

	// _cursor should be dropped because its rendered value is "".
	assert.Equal(t, "_limit=5", cap.rawQuery)

}

func TestPOST_BodyRenders(t *testing.T) {
	url, cap := startCapture(t, 201, `{}`)
	cfg := &Config{
		Name:		"t",
		Defaults:	Defaults{BaseURL: url},
		Commands: []Command{{
			Name:	"posts",
			Commands: []Command{{
				Name:	"create",
				Flags: []Flag{
					{Name: "title", Type: "string", Required: true},
					{Name: "body", Short: "b", Type: "string", Required: true},
				},
				Request: &Request{
					Method:	"POST",
					Path:	"/posts",
					Body:	json.RawMessage(`{"title":"{{.title}}","body":"{{.body}}","userId":1}`),
				},
			}},
		}},
	}
	code, _ := execCmd(t, cfg, "posts", "create", "--title", "hi there", "-b", `q"uoted`)
	require.Equal(t, 0, code)

	assert.Equal(t, "POST", cap.method)

	assert.Equal(t, "application/json", cap.headers.Get("Content-Type"))

	var got map[string]any
	require.NoError(t, json.Unmarshal(cap.body, &got))

	assert.Equal(t, "hi there", got["title"])

	assert.Equal(t, `q"uoted`, got["body"])

	assert.Equal(t, float64(1), got["userId"])

}

func TestHTTPErrorExitCodes(t *testing.T) {
	url, _ := startCapture(t, 404, "not found")
	cfg := &Config{
		Name:		"t",
		Defaults:	Defaults{BaseURL: url},
		Commands: []Command{{
			Name:		"x",
			Request:	&Request{Method: "GET", Path: "/x"},
		}},
	}
	code, _ := execCmd(t, cfg, "x")
	assert.Equal(t, 4, code)

}

func TestServerErrorExitCode(t *testing.T) {
	url, _ := startCapture(t, 503, "")
	cfg := &Config{
		Name:		"t",
		Defaults:	Defaults{BaseURL: url},
		Commands: []Command{{
			Name:		"x",
			Request:	&Request{Method: "GET", Path: "/x"},
		}},
	}
	code, _ := execCmd(t, cfg, "x")
	assert.Equal(t, 5, code)

}

func TestExampleConfigLoads(t *testing.T) {
	// Sanity check: the shipped example validates cleanly.
	cfg, err := Load("api.example.json")
	require.Nil(t, err)

	assert.NotEqual(t, "", cfg.Name)

}
