package main

import _ "embed"

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The server runs os.Executable once per call. In a test, that is this test binary, so it runs the real CLI when the server starts it.
func TestMain(m *testing.M) {
	if os.Getenv(serveTestReentry) == "1" {
		os.Exit(run(os.Args[1:], os.Stderr))
	}
	os.Exit(m.Run())
}

const serveTestReentry = "API_CLI_SERVE_TEST_REENTRY"

//go:embed testdata/serve-test-config.xml
var serveTestConfig string

// startServer serves serveTestConfig on a socket in a new directory, and stops it when the test ends.
func startServer(t *testing.T) string {
	t.Helper()
	t.Serial()
	t.Setenv(serveTestReentry, "1")
	t.Setenv("SERVE_TEST_SECRET", "server-side-only")
	dir := t.TempDir()
	cfg := filepath.Join(dir, "gh.xml")
	require.NoError(t, os.WriteFile(cfg, []byte(serveTestConfig), 0o644))
	sock := filepath.Join(dir, "s.sock")
	ln, err := listenUnix(sock)
	require.NoError(t, err)
	exe, err := os.Executable()
	require.NoError(t, err)
	go func() {
		for {
			conn, err := ln.AcceptUnix()
			if err != nil {
				return
			}
			go serveConn(conn, exe, cfg)
		}
	}()
	t.Cleanup(func() { _ = ln.Close() })
	return sock
}

// callServer runs argv as a client and returns the exit code, stdout and stderr.
func callServer(t *testing.T, sock, stdin string, argv ...string) (int, string, string) {
	t.Helper()
	inR, inW, err := os.Pipe()
	require.NoError(t, err)
	outR, outW, err := os.Pipe()
	require.NoError(t, err)
	errR, errW, err := os.Pipe()
	require.NoError(t, err)
	go func() {
		_, _ = io.WriteString(inW, stdin)
		_ = inW.Close()
	}()
	var out, errBuf bytes.Buffer
	done := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(&out, outR); done <- struct{}{} }()
	go func() { _, _ = io.Copy(&errBuf, errR); done <- struct{}{} }()
	var clientErr bytes.Buffer
	code := runClient(sock, argv, inR, outW, errW, &clientErr)
	_ = inR.Close()
	_ = outW.Close()
	_ = errW.Close()
	for range 2 {
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Fatal("the run's output never closed")
		}
	}
	return code, out.String(), errBuf.String() + clientErr.String()
}

func TestServe_RunsTheContractOnTheServerSide(t *testing.T) {
	sock := startServer(t)
	code, out, errOut := callServer(t, sock, "", "pr", "view", "12", "--json", "title", "--web")
	assert.Equal(t, 0, code, errOut)
	assert.Equal(t, "gh pr view 12 --json=title --web\n", out)
}

func TestServe_RefusesACommandTheContractLacks(t *testing.T) {
	sock := startServer(t)
	code, _, errOut := callServer(t, sock, "", "pr", "merge", "12")
	assert.NotEqual(t, 0, code)
	assert.Contains(t, errOut, "unknown command")
}

func TestServe_RefusesAFlagTheContractLacks(t *testing.T) {
	sock := startServer(t)
	code, _, errOut := callServer(t, sock, "", "pr", "view", "12", "--admin")
	assert.NotEqual(t, 0, code)
	assert.Contains(t, errOut, "unknown flag: --admin")
}

func TestServe_RefusesServerOnlyGlobalFlags(t *testing.T) {
	sock := startServer(t)
	for _, argv := range [][]string{
		{"--config", "/tmp/evil.xml", "where"},
		{"--config=/tmp/evil.xml", "where"},
		{"where", "--var", "X=1"},
		{"where", "--debug"},
		{"--download-dir", "/", "where"},
	} {
		code, out, errOut := callServer(t, sock, "", argv...)
		assert.Equal(t, 2, code, "%v", argv)
		assert.Empty(t, out, "%v must not run", argv)
		assert.Contains(t, errOut, "is not allowed from a client", "%v", argv)
	}
}

func TestServe_ArgumentsAfterDoubleDashAreNotFlags(t *testing.T) {
	assert.NoError(t, refuseServerOnlyFlags([]string{"x", "--", "--config"}))
	assert.Error(t, refuseServerOnlyFlags([]string{"x", "--verbose"}))
	assert.NoError(t, refuseServerOnlyFlags([]string{"x", "--json", "-config"}))
}

func TestServe_RunsInTheClientsDirectory(t *testing.T) {
	sock := startServer(t)
	dir := t.TempDir()
	chdir(t, dir)
	code, out, errOut := callServer(t, sock, "", "where")
	assert.Equal(t, 0, code, errOut)
	want, err := filepath.EvalSymlinks(dir)
	require.NoError(t, err)
	got, err := filepath.EvalSymlinks(strings.TrimSpace(out))
	require.NoError(t, err)
	assert.Equal(t, want, got)
}

func TestServe_TheEnvironmentIsTheServers(t *testing.T) {
	sock := startServer(t)
	code, out, errOut := callServer(t, sock, "", "secret")
	assert.Equal(t, 0, code, errOut)
	assert.Equal(t, "server-side-only", out)
}

func TestServe_RefusesADownloadLeaf(t *testing.T) {
	sock := startServer(t)
	dir := t.TempDir()
	chdir(t, dir)
	code, _, errOut := callServer(t, sock, "", "fetch")
	assert.Equal(t, 2, code, errOut)
	assert.Contains(t, errOut, "cannot run for a --client")
	_, err := os.Stat(filepath.Join(dir, "written-by-the-server"))
	assert.True(t, os.IsNotExist(err), "the server must write nothing for a client")
}

func TestServe_PassesTheExitCodeAndStdin(t *testing.T) {
	sock := startServer(t)
	code, _, _ := callServer(t, sock, "", "fail")
	assert.Equal(t, 7, code)
	code, out, errOut := callServer(t, sock, "piped in", "cat")
	assert.Equal(t, 0, code, errOut)
	assert.Equal(t, "piped in", out)
}

func TestServe_ConcurrentCallsKeepTheirOwnOutput(t *testing.T) {
	sock := startServer(t)
	type answer struct {
		code int
		out  string
	}
	results := make(chan answer, 8)
	for i := range 8 {
		go func() {
			code, out, _ := callServer(t, sock, strings.Repeat("x", i+1), "cat")
			results <- answer{code, out}
		}()
	}
	seen := map[int]bool{}
	for range 8 {
		got := <-results
		assert.Equal(t, 0, got.code)
		seen[len(got.out)] = true
	}
	assert.Len(t, seen, 8, "each call must get its own stdin back")
}

func TestClient_NoServerFailsLoud(t *testing.T) {
	var errOut bytes.Buffer
	code := runClient(filepath.Join(t.TempDir(), "none.sock"), []string{"x"}, os.Stdin, os.Stdout, os.Stderr, &errOut)
	assert.Equal(t, 2, code)
	assert.Contains(t, errOut.String(), "no api-cli server answers")
}

func TestClientTarget_OnlyAsTheFirstFlag(t *testing.T) {
	sock, rest, ok, err := clientTarget([]string{"--client", "/s", "pr", "--client", "x"})
	require.NoError(t, err)
	assert.True(t, ok)
	assert.Equal(t, "/s", sock)
	assert.Equal(t, []string{"pr", "--client", "x"}, rest)
	sock, _, ok, _ = clientTarget([]string{"--client=/t"})
	assert.True(t, ok)
	assert.Equal(t, "/t", sock)
	_, _, ok, _ = clientTarget([]string{"pr", "--client", "/s"})
	assert.False(t, ok)
	_, _, ok, err = clientTarget([]string{"--client"})
	assert.True(t, ok)
	assert.Error(t, err)
}

func TestRun_ServerNeedsAConfig(t *testing.T) {
	chdir(t, t.TempDir())
	var errOut bytes.Buffer
	assert.Equal(t, 2, run([]string{"--server", "/tmp/x.sock"}, &errOut))
	assert.Contains(t, errOut.String(), "--server needs --config")
}

func TestListenUnix_RefusesAFileThatIsNotASocket(t *testing.T) {
	path := filepath.Join(t.TempDir(), "f")
	require.NoError(t, os.WriteFile(path, nil, 0o644))
	_, err := listenUnix(path)
	assert.ErrorContains(t, err, "is not a socket")
}

func TestFlagArgs_RebuildsTheSetFlags(t *testing.T) {
	got := flagArgs(map[string]any{
		"json": "a,b", "web": true, "off": false, "limit": 0, "empty": "", "R": "o/r",
		"label": []string{"x", "y"}, "n": int64(5),
	})
	assert.Equal(t, []string{"-R=o/r", "--json=a,b", "--label=x", "--label=y", "-n=5", "--web"}, got)
}
