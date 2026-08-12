package main

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// shellTransport is a transport that runs an inline /bin/sh script.
func shellTransport(name, script string, isDefault bool) *Transport {
	return &Transport{
		Name:    name,
		Default: isDefault,
		Command: &Cmd{Shell: true, Template: script},
	}
}

func TestTransport_ReceivesRenderedRequest(t *testing.T) {
	cfg := &Config{
		Name: "t",
		Transports: map[string]*Transport{
			"fake": shellTransport("fake", `printf '{"method":"%s","url":"%s","auth":"%s"}' "{{.request.method}}" "{{.request.url}}" "{{index .request.headers "Authorization"}}"`, true),
		},
		Vars: map[string]any{"token": "sekrit"},
		Request: &Request{
			Method:  "POST",
			URL:     "https://internal.example/v1/thing",
			Query:   []Param{{Name: "id", Value: "{{.arg.id}}"}},
			Headers: []Header{{Name: "Authorization", Value: "Bearer {{.var.token}}"}},
		},
		Commands: []Command{{
			Name: "get",
			Args: []Arg{{Name: "id", Required: true}},
		}},
	}
	code, out := execCmd(t, cfg, "get", "42")
	require.Equal(t, 0, code)
	assert.JSONEq(t, `{"method":"POST","url":"https://internal.example/v1/thing?id=42","auth":"Bearer sekrit"}`, out)
}

func TestTransport_BodyOnStdinByDefault(t *testing.T) {
	cfg := &Config{
		Name:       "t",
		Transports: map[string]*Transport{"fake": shellTransport("fake", `cat`, true)},
		Request: &Request{
			Method: "POST",
			URL:    "https://internal.example/v1/thing",
			Body:   `{"name":"{{.arg.name}}"}`,
		},
		Commands: []Command{{Name: "put", Args: []Arg{{Name: "name", Required: true}}}},
	}
	code, out := execCmd(t, cfg, "put", "ada")
	require.Equal(t, 0, code)
	assert.JSONEq(t, `{"name":"ada"}`, out)
}

// An explicit <stdin> wins over the body default, including an empty one.
func TestTransport_ExplicitStdinOverridesBody(t *testing.T) {
	tr := shellTransport("fake", `cat; printf 'end'`, true)
	tr.Stdin, tr.StdinSet = "override", true

	cfg := &Config{
		Name:       "t",
		Transports: map[string]*Transport{"fake": tr},
		Request:    &Request{Method: "POST", URL: "https://internal.example/x", Body: "the-body"},
		Commands:   []Command{{Name: "go"}},
	}
	code, out := execCmd(t, cfg, "go")
	require.Equal(t, 0, code)
	assert.Equal(t, "overrideend\n", out) // streamRequest terminates output
}

// A transport never inherits the process's stdin: a program that reads stdin
// with no body configured sees EOF rather than the user's terminal.
func TestTransport_EmptyStdinIsClosedNotInherited(t *testing.T) {
	cfg := &Config{
		Name:       "t",
		Transports: map[string]*Transport{"fake": shellTransport("fake", `printf 'body=[%s]' "$(cat)"`, true)},
		Request:    &Request{Method: "GET", URL: "https://internal.example/x"},
		Commands:   []Command{{Name: "go"}},
	}
	prev := execStdin
	execStdin = failingReader{t}
	t.Cleanup(func() { execStdin = prev })

	code, out := execCmd(t, cfg, "go")
	require.Equal(t, 0, code)
	assert.Equal(t, "body=[]\n", out)
}

// failingReader fails the test if anything reads it.
type failingReader struct{ t *testing.T }

func (r failingReader) Read([]byte) (int, error) {
	r.t.Error("child read the process stdin")
	return 0, nil
}

func TestTransport_NonZeroExitFailsRequest(t *testing.T) {
	cfg := &Config{
		Name:       "t",
		Transports: map[string]*Transport{"fake": shellTransport("fake", `echo "boom" >&2; exit 3`, true)},
		Request:    &Request{Method: "GET", URL: "https://internal.example/x"},
		Commands:   []Command{{Name: "go"}},
	}
	code, out, errOut := execCmdFull(t, cfg, "go")
	assert.Equal(t, 3, code)
	assert.Empty(t, out)
	assert.Contains(t, errOut, "boom")
	assert.Contains(t, errOut, `transport "fake" exited 3`)
}

// <response jq=> shapes a transport's stdout exactly as it shapes a built-in
// response body.
func TestTransport_ResponseJQApplies(t *testing.T) {
	cfg := &Config{
		Name:       "t",
		Transports: map[string]*Transport{"fake": shellTransport("fake", `printf '{"items":[{"n":1},{"n":2}]}'`, true)},
		Vars:       map[string]any{"filter": ".items | map(.n)"},
		Request: &Request{
			Method:   "GET",
			URL:      "https://internal.example/x",
			Response: &Response{JQ: "var.filter"},
		},
		Commands: []Command{{Name: "go"}},
	}
	code, out := execCmd(t, cfg, "go")
	require.Equal(t, 0, code)
	assert.JSONEq(t, `[1,2]`, out)
}

// A request naming a transport uses it even when another is the default.
func TestTransport_PerRequestSelection(t *testing.T) {
	cfg := &Config{
		Name: "t",
		Transports: map[string]*Transport{
			"a": shellTransport("a", `printf 'from-a'`, true),
			"b": shellTransport("b", `printf 'from-b'`, false),
		},
		Request:  &Request{Method: "GET", URL: "https://internal.example/x"},
		Commands: []Command{{Name: "go"}, {Name: "viab", Request: &Request{Method: "GET", URL: "https://internal.example/x", Transport: "b"}}},
	}
	code, out := execCmd(t, cfg, "go")
	require.Equal(t, 0, code)
	assert.Equal(t, "from-a\n", out)

	code, out = execCmd(t, cfg, "viab")
	require.Equal(t, 0, code)
	assert.Equal(t, "from-b\n", out)
}

// --transport=http forces the built-in client past a default transport.
func TestTransport_FlagForcesBuiltinClient(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`from-http`))
	}))
	defer srv.Close()
	swapHTTPClient(t, srv)

	cfg := &Config{
		Name:       "t",
		Transports: map[string]*Transport{"fake": shellTransport("fake", `printf 'from-transport'`, true)},
		Request:    &Request{Method: "GET", URL: srv.URL},
		Commands:   []Command{{Name: "go"}},
	}
	code, out := execCmd(t, cfg, "go")
	require.Equal(t, 0, code)
	assert.Equal(t, "from-transport\n", out)

	code, out = execCmd(t, cfg, "--transport", "http", "go")
	require.Equal(t, 0, code)
	assert.Equal(t, "from-http\n", out)
}

func TestTransport_UnknownOverrideFailsLoud(t *testing.T) {
	cfg := &Config{
		Name:       "t",
		Transports: map[string]*Transport{"fake": shellTransport("fake", `printf 'x'`, true)},
		Request:    &Request{Method: "GET", URL: "https://internal.example/x"},
		Commands:   []Command{{Name: "go"}},
	}
	code, out, errOut := execCmdFull(t, cfg, "--transport", "nope", "go")
	assert.Equal(t, 1, code)
	assert.Empty(t, out)
	assert.Contains(t, errOut, `unknown transport "nope"`)
	assert.Contains(t, errOut, "known: fake, http")
}

func TestTransport_HeaderLinesSpreadIntoArgv(t *testing.T) {
	cfg := &Config{
		Name: "t",
		Transports: map[string]*Transport{
			"fake": {
				Name:    "fake",
				Default: true,
				Command: &Cmd{Argv: []string{"echo", "{{spread .request.header_lines}}"}},
			},
		},
		Request: &Request{
			Method: "GET",
			URL:    "https://internal.example/x",
			Headers: []Header{
				{Name: "Accept", Value: "application/json"},
				{Name: "X-Trace", Value: "abc"},
			},
		},
		Commands: []Command{{Name: "go"}},
	}
	code, out := execCmd(t, cfg, "go")
	require.Equal(t, 0, code)
	assert.Equal(t, "Accept: application/json X-Trace: abc\n", out)
}

func TestParseXML_Transports(t *testing.T) {
	cfg := mustParse(t, `<config name="x">
	<transports>
		<transport name="corp" default="true">
			<run>
				<argv>corp-http</argv>
				<argv><value name="request.method"/></argv>
				<argv><value name="request.url"/></argv>
			</run>
			<cwd>/tmp</cwd>
			<stdin><value name="request.body"/></stdin>
		</transport>
	</transports>
	<command name="c"><run><request transport="corp"><url>https://x/y</url></request></run></command>
</config>`)

	require.Len(t, cfg.Transports, 1)
	tr := cfg.Transports["corp"]
	require.NotNil(t, tr)
	assert.True(t, tr.Default)
	assert.Equal(t, []string{"corp-http", "{{ .request.method }}", "{{ .request.url }}"}, tr.Command.Argv)
	assert.Equal(t, "/tmp", tr.Cwd)
	assert.True(t, tr.StdinSet)
	assert.Equal(t, "{{ .request.body }}", tr.Stdin)
	assert.Equal(t, "corp", cfg.Commands[0].Request.Transport)
}

func TestParseXML_TransportErrors(t *testing.T) {
	cases := map[string]string{
		"missing name":     `<config name="x"><transports><transport><run>x</run></transport></transports></config>`,
		"duplicate name":   `<config name="x"><transports><transport name="a"><run>x</run></transport><transport name="a"><run>y</run></transport></transports></config>`,
		"request as run":   `<config name="x"><transports><transport name="a"><run><request><url>u</url></request></run></transport></transports></config>`,
		"unknown child":    `<config name="x"><transports><transport name="a"><run>x</run><nope/></transport></transports></config>`,
		"unknown attr":     `<config name="x"><transports><transport name="a" fallback="true"><run>x</run></transport></transports></config>`,
		"unknown element":  `<config name="x"><transports><nope name="a"/></transports></config>`,
		"attrs on wrapper": `<config name="x"><transports default="a"><transport name="a"><run>x</run></transport></transports></config>`,
	}
	for name, src := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := parseConfigXML([]byte(src))
			assert.Error(t, err)
		})
	}
}

func TestValidate_TransportErrors(t *testing.T) {
	cases := map[string]struct{ src, want string }{
		"reserved name": {
			`<config name="x"><transports><transport name="http"><run>x</run></transport></transports><command name="c"><run>y</run></command></config>`,
			"reserved",
		},
		"no run": {
			`<config name="x"><transports><transport name="a"/></transports><command name="c"><run>y</run></command></config>`,
			"requires a <run> command",
		},
		"two defaults": {
			`<config name="x"><transports><transport name="a" default="true"><run>x</run></transport><transport name="b" default="true"><run>x</run></transport></transports><command name="c"><run>y</run></command></config>`,
			"already the default",
		},
		"unknown reference": {
			`<config name="x"><command name="c"><run><request transport="ghost"><url>u</url></request></run></command></config>`,
			`unknown transport "ghost"`,
		},
		"unknown reference in step": {
			`<config name="x"><command name="c"><steps><step name="s"><run><request transport="ghost"><url>u</url></request></run></step></steps><run>y</run></command></config>`,
			`unknown transport "ghost"`,
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := loadStr(t, tc.src)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
		})
	}
}

// The curl transport shipped in api.example.xml is documentation users will
// copy, so pin the argv it actually builds — in particular that a body reaches
// curl rather than being dropped, and that a GET grows no stray empty
// arguments from the conditional spread.
func TestExampleCurlTransportArgv(t *testing.T) {
	cfg, err := Load("api.example.xml")
	require.NoError(t, err)
	tr := cfg.Transports["curl"]
	require.NotNil(t, tr)

	build := func(p *preparedRequest) []string {
		cmd, err := buildExecCmd(tr.Command, p.context(map[string]any{}))
		require.NoError(t, err)
		return cmd.Args
	}

	get := build(&preparedRequest{
		Method:  "GET",
		URL:     "https://api.example.com/users",
		Headers: []renderedHeader{{Name: "Accept", Value: "application/json"}},
	})
	assert.Equal(t, []string{"curl", "-fsSL", "-X", "GET", "-H", "Accept: application/json", "https://api.example.com/users"}, get)

	post := build(&preparedRequest{
		Method: "POST",
		URL:    "https://api.example.com/users",
		Body:   `{"name":"ada"}`,
	})
	assert.Equal(t, []string{"curl", "-fsSL", "-X", "POST", "--data-binary", "@-", "https://api.example.com/users"}, post)
	assert.False(t, tr.StdinSet, "the body must reach curl on stdin")
}

func TestValidate_TransportAcceptsBuiltinNameOnRequest(t *testing.T) {
	_, err := loadStr(t, `<config name="x">
	<transports><transport name="a" default="true"><run>x</run></transport></transports>
	<command name="c"><run><request transport="http"><url>u</url></request></run></command>
</config>`)
	assert.NoError(t, err)
}
