package main

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestAllowStatus_FallbackChain is the "try A, then B" shape: the first step
// may 404, and the second step runs on what the first one stored.
func TestAllowStatus_FallbackChain(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/a" {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"message":"Not Found"}`))
			return
		}
		_, _ = w.Write([]byte(`{"name":"from-b"}`))
	}))
	t.Cleanup(srv.Close)
	swapHTTPClient(t, srv)

	cfg := &Config{
		Name: "t",
		Commands: []Command{{
			Name: "get",
			Steps: []Step{{
				Name:    "a",
				Request: &Request{Method: "GET", URL: srv.URL + "/a", AllowStatus: []int{404}},
			}, {
				Name:    "b",
				When:    "{{ not .result.a.name }}",
				Request: &Request{Method: "GET", URL: srv.URL + "/b"},
			}},
			Command: &Cmd{Shell: true, Template: "printf %s {{ .result.b.name }}"},
		}},
	}

	code, out := execCmd(t, cfg, "get")
	require.Equal(t, 0, code)
	assert.Equal(t, "from-b", out)
}

func TestAllowStatus_UnlistedStatusStillFails(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusGone)
		_, _ = w.Write([]byte(`gone`))
	}))
	t.Cleanup(srv.Close)
	swapHTTPClient(t, srv)

	cfg := &Config{
		Name: "t",
		Commands: []Command{{
			Name:    "get",
			Request: &Request{Method: "GET", URL: srv.URL + "/a", AllowStatus: []int{404}},
		}},
	}

	code, _, errOut := execCmdFull(t, cfg, "get")
	assert.NotEqual(t, 0, code)
	assert.Contains(t, errOut, "HTTP 410")
}

func TestAllowStatus_RejectsATransport(t *testing.T) {
	cfg := &Config{
		Name: "t",
		Transports: map[string]*Transport{"prog": {
			Name:    "prog",
			Default: true,
			Command: &Cmd{Shell: true, Template: "printf %s '{}'"},
		}},
		Commands: []Command{{
			Name:    "get",
			Request: &Request{Method: "GET", URL: "https://example.invalid/a", AllowStatus: []int{404}},
		}},
	}

	code, _, errOut := execCmdFull(t, cfg, "get")
	assert.NotEqual(t, 0, code)
	assert.Contains(t, errOut, "allow-status needs the built-in client")
}

func TestParseXML_AllowStatus(t *testing.T) {
	cfg := mustParse(t, `<config name="x"><command name="c"><run>
		<request allow-status="404, 410"><url>https://e/x</url></request></run></command></config>`)
	assert.Equal(t, []int{404, 410}, cfg.Commands[0].Request.AllowStatus)

	_, err := parseConfigXML([]byte(`<config name="x"><command name="c"><run>
		<request allow-status="200"><url>https://e/x</url></request></run></command></config>`))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not an error status")

	_, err = parseConfigXML([]byte(`<config name="x"><command name="c"><run>
		<request allow-status="oops"><url>https://e/x</url></request></run></command></config>`))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "is not a status code")
}
