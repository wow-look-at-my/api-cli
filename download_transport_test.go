package main

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fixtureTransport writes fixture's contents to stdout, standing in for the
// program a real config would delegate to (corp-http, an SSO-aware curl, ...).
func fixtureTransport(name, fixture string, isDefault bool) *Transport {
	return shellTransport(name, `cat `+fixture, isDefault)
}

// writeFixture drops body on disk and returns its path.
func writeFixture(t *testing.T, body []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "payload")
	require.NoError(t, os.WriteFile(path, body, 0o644))
	return path
}

func TestDownloadTransport_StreamsProgramStdoutToTheFile(t *testing.T) {
	body := binaryBody()
	fixture := writeFixture(t, body)
	dir := t.TempDir()

	cfg, err := loadStr(t, `<config name="dl">
		<transports><transport name="corp"><run>cat `+fixture+`</run></transport></transports>
		<command name="grab">
			<download transport="corp">
				<url>https://internal.example/blob</url>
				<to>blob.bin</to>
			</download>
		</command>
	</config>`)
	require.NoError(t, err)

	code, _, errOut := execCmdFull(t, cfg, "grab", "--download-dir", dir)
	require.Equal(t, 0, code, "stderr: %s", errOut)

	got, err := os.ReadFile(filepath.Join(dir, "blob.bin"))
	require.NoError(t, err)
	assert.Equal(t, body, got, "a transport download is streamed, not round-tripped through a string")
}

func TestDownloadTransport_ProgramSeesTheRenderedRequest(t *testing.T) {
	dir := t.TempDir()
	cfg, err := loadStr(t, `<config name="dl">
		<vars><var name="token">sekrit</var></vars>
		<transports>
			<transport name="corp">
				<run>
					<argv>/bin/sh</argv>
					<argv>-c</argv>
					<argv>printf '%s %s %s' "$0" "$1" "$2"</argv>
					<argv><value name="request.method"/></argv>
					<argv><value name="request.url"/></argv>
					<argv><value expr="{{ index .request.headers &quot;Authorization&quot; }}"/></argv>
				</run>
			</transport>
		</transports>
		<command name="grab">
			<download transport="corp">
				<url>https://internal.example/thing?v=2</url>
				<to>out.txt</to>
				<header name="Authorization">Bearer <value name="var.token"/></header>
			</download>
		</command>
	</config>`)
	require.NoError(t, err)

	code, _, errOut := execCmdFull(t, cfg, "grab", "--download-dir", dir)
	require.Equal(t, 0, code, "stderr: %s", errOut)

	got, err := os.ReadFile(filepath.Join(dir, "out.txt"))
	require.NoError(t, err)
	assert.Equal(t, "GET https://internal.example/thing?v=2 Bearer sekrit", string(got),
		"the same .request context a request-form transport gets")
}

func TestDownloadTransport_DefaultTransportCarriesDownloadsToo(t *testing.T) {
	fixture := writeFixture(t, []byte("via-default"))
	dir := t.TempDir()

	cfg := &Config{
		Name:       "dl",
		Transports: map[string]*Transport{"corp": fixtureTransport("corp", fixture, true)},
		Commands: []Command{{
			Name:      "grab",
			Downloads: []Download{{URL: "https://internal.example/x", To: "f.bin"}},
		}},
	}

	code, _, errOut := execCmdFull(t, cfg, "grab", "--download-dir", dir)
	require.Equal(t, 0, code, "stderr: %s", errOut)

	got, err := os.ReadFile(filepath.Join(dir, "f.bin"))
	require.NoError(t, err)
	assert.Equal(t, "via-default", string(got),
		"a config whose endpoints all need the program needs it for its files too")
}

func TestDownloadTransport_OptOutToTheBuiltinClient(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("from-http"))
	}))
	defer srv.Close()
	swapDownloadClient(t, srv)

	fixture := writeFixture(t, []byte("from-transport"))
	dir := t.TempDir()

	cfg := &Config{
		Name:       "dl",
		Transports: map[string]*Transport{"corp": fixtureTransport("corp", fixture, true)},
		Commands: []Command{{
			Name:      "public",
			Downloads: []Download{{URL: srv.URL + "/x", To: "f.bin", Transport: builtinTransportName}},
		}},
	}

	code, _, errOut := execCmdFull(t, cfg, "public", "--download-dir", dir)
	require.Equal(t, 0, code, "stderr: %s", errOut)

	got, err := os.ReadFile(filepath.Join(dir, "f.bin"))
	require.NoError(t, err)
	assert.Equal(t, "from-http", string(got))
}

func TestDownloadTransport_VerifiesTheDigestToo(t *testing.T) {
	body := []byte("transported bytes")
	sum := sha256.Sum256(body)
	fixture := writeFixture(t, body)
	dir := t.TempDir()

	mk := func(digest string) *Config {
		return &Config{
			Name:       "dl",
			Transports: map[string]*Transport{"corp": fixtureTransport("corp", fixture, true)},
			Commands: []Command{{
				Name:      "grab",
				Downloads: []Download{{URL: "https://internal.example/x", To: "f.bin", Hash: digest, HashAlgo: "sha256"}},
			}},
		}
	}

	code, _, errOut := execCmdFull(t, mk(hex.EncodeToString(sum[:])), "grab", "--download-dir", dir)
	require.Equal(t, 0, code, "stderr: %s", errOut)
	assert.Contains(t, errOut, "sha256 ok")

	bad := t.TempDir()
	code, _, errOut = execCmdFull(t, mk(strings.Repeat("ab", 32)), "grab", "--download-dir", bad)
	assert.Equal(t, 1, code)
	assert.Contains(t, errOut, "sha256 mismatch")
	assert.NoFileExists(t, filepath.Join(bad, "f.bin"), "the same guarantee on both paths")
}

func TestDownloadTransport_FailingProgramFailsTheDownload(t *testing.T) {
	dir := t.TempDir()
	cfg := &Config{
		Name: "dl",
		Transports: map[string]*Transport{
			"corp": shellTransport("corp", `echo "corp-http: no such object" >&2; exit 22`, true),
		},
		// retries="0" means report the failure now, and is distinct from unset.
		Downloads: &Downloads{Retries: 0, RetriesSet: true},
		Commands: []Command{{
			Name:      "grab",
			Downloads: []Download{{URL: "https://internal.example/gone", To: "f.bin"}},
		}},
	}

	code, _, errOut := execCmdFull(t, cfg, "grab", "--download-dir", dir)
	assert.Equal(t, 1, code)
	assert.Contains(t, errOut, `transport "corp"`)
	assert.Contains(t, errOut, "corp-http: no such object", "the program's own stderr reaches the user")
	assert.NoFileExists(t, filepath.Join(dir, "f.bin"))
	assert.NoFileExists(t, filepath.Join(dir, "f.bin.part"))
}

func TestDownloadTransport_DirectoryDestinationNamesFromTheURL(t *testing.T) {
	fixture := writeFixture(t, []byte("x"))
	dir := t.TempDir()

	cfg := &Config{
		Name:       "dl",
		Transports: map[string]*Transport{"corp": fixtureTransport("corp", fixture, true)},
		Commands: []Command{{
			Name:      "grab",
			Downloads: []Download{{URL: "https://internal.example/pkg/tool-2.1.tgz?sig=x"}},
		}},
	}

	code, _, errOut := execCmdFull(t, cfg, "grab", "--download-dir", dir)
	require.Equal(t, 0, code, "stderr: %s", errOut)
	assert.FileExists(t, filepath.Join(dir, "tool-2.1.tgz"),
		"no Content-Disposition from a program, so the URL names the file")
}

func TestValidate_DownloadTransportMustExist(t *testing.T) {
	_, err := loadStr(t, `<config name="dl"><command name="g">
		<download transport="ghost"><url>https://h/f</url></download>
	</command></config>`)
	assert.ErrorContains(t, err, `references unknown transport "ghost"`)

	_, err = loadStr(t, `<config name="dl"><command name="g">
		<download transport="http"><url>https://h/f</url></download>
	</command></config>`)
	assert.NoError(t, err, `"http" is the built-in client, always available`)
}

func TestParseXML_DownloadTransportAttribute(t *testing.T) {
	cfg := mustParse(t, `<config name="x"><command name="g">
		<download transport="corp"><url>https://h/f</url></download>
	</command></config>`)
	assert.Equal(t, "corp", cfg.Commands[0].Downloads[0].Transport)
}

func TestResolveArgv(t *testing.T) {
	shell, err := resolveArgv(&Cmd{Shell: true, Template: `echo {{.var.x}}`}, map[string]any{"var": map[string]any{"x": "hi"}})
	require.NoError(t, err)
	assert.Equal(t, []string{"/bin/sh", "-c", "echo hi"}, shell)

	argv, err := resolveArgv(&Cmd{Argv: []string{"tool", "{{.var.x}}"}}, map[string]any{"var": map[string]any{"x": "hi"}})
	require.NoError(t, err)
	assert.Equal(t, []string{"tool", "hi"}, argv)

	_, err = resolveArgv(&Cmd{}, nil)
	assert.ErrorContains(t, err, "argv command is empty")
}
