package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// writeJSON encodes v as the response body. A manifest carries server URLs and
// digests, so it is marshaled rather than formatted into a string.
func writeJSON(w http.ResponseWriter, v any) {
	_ = json.NewEncoder(w).Encode(v)
}

// swapDownloadClient points the download queue at a test server. The queue
// caches the client when it is built, so the shared one is dropped too.
func swapDownloadClient(t *testing.T, srv *httptest.Server) {
	t.Helper()
	serial(t)
	prev := downloadClient
	downloadClient = srv.Client()
	resetSharedQueue()
	t.Cleanup(func() {
		downloadClient = prev
		resetSharedQueue()
	})
}

// assetServer serves a small file per path and records the auth it was sent.
func assetServer(t *testing.T) (*httptest.Server, func() (string, string)) {
	t.Helper()
	var auth, cookie string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if a := r.Header.Get("Authorization"); a != "" {
			auth = a
		}
		if c := r.Header.Get("Cookie"); c != "" {
			cookie = c
		}
		switch r.URL.Path {
		case "/index":
			base := "http://" + r.Host
			writeJSON(w, map[string]any{"assets": []any{
				map[string]any{"name": "one.txt", "url": base + "/files/one"},
				map[string]any{"name": "two.txt", "url": base + "/files/two"},
			}})
		case "/files/one":
			_, _ = w.Write([]byte("first"))
		case "/files/two":
			_, _ = w.Write([]byte("second-file"))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, func() (string, string) { return auth, cookie }
}

func TestIntegration_DownloadHandsStepURLsToTheQueue(t *testing.T) {
	srv, seen := assetServer(t)
	swapHTTPClient(t, srv)
	swapDownloadClient(t, srv)
	dir := t.TempDir()

	cfg, err := loadStr(t, `<config name="dl">
		<downloads concurrency="2"/>
		<vars><var name="token">s3cret</var></vars>
		<command name="grab">
			<steps>
				<step name="index">
					<run><request><url>`+srv.URL+`/index</url></request></run>
				</step>
			</steps>
			<download over="result.index.assets">
				<url><value name="url"/></url>
				<to><value name="name"/></to>
				<header name="Authorization">Bearer <value name="var.token"/></header>
				<cookie name="sid">abc</cookie>
			</download>
		</command>
	</config>`)
	require.NoError(t, err)

	code, out, errOut := execCmdFull(t, cfg, "grab", "--download-dir", dir)
	require.Equal(t, 0, code, "stderr: %s", errOut)

	one, err := os.ReadFile(filepath.Join(dir, "one.txt"))
	require.NoError(t, err)
	assert.Equal(t, "first", string(one))
	two, err := os.ReadFile(filepath.Join(dir, "two.txt"))
	require.NoError(t, err)
	assert.Equal(t, "second-file", string(two))

	// Not a terminal here, so the destinations are stdout's payload and the
	// summary stays on stderr.
	assert.ElementsMatch(t,
		[]string{filepath.Join(dir, "one.txt"), filepath.Join(dir, "two.txt")},
		strings.Fields(strings.TrimSpace(out)))
	assert.Contains(t, errOut, "downloaded 2/2 files")
	assert.Contains(t, errOut, "downloading ")

	auth, cookie := seen()
	assert.Equal(t, "Bearer s3cret", auth, "the step's auth reaches the downloader")
	assert.Equal(t, "sid=abc", cookie)
}

func TestIntegration_DownloadFailureIsLoudAndNonZero(t *testing.T) {
	srv, _ := assetServer(t)
	swapDownloadClient(t, srv)
	dir := t.TempDir()

	cfg, err := loadStr(t, `<config name="dl"><command name="grab">
		<download><url>`+srv.URL+`/files/one</url><to>ok.txt</to></download>
		<download><url>`+srv.URL+`/nope</url><to>bad.txt</to></download>
	</command></config>`)
	require.NoError(t, err)

	code, out, errOut := execCmdFull(t, cfg, "grab", "--download-dir", dir, "--concurrency", "1")
	assert.Equal(t, 1, code)
	assert.Contains(t, errOut, "downloaded 1/2 files")
	assert.Contains(t, errOut, "/nope: HTTP 404")
	assert.FileExists(t, filepath.Join(dir, "ok.txt"), "one bad URL does not cancel the rest")
	assert.NoFileExists(t, filepath.Join(dir, "bad.txt"))
	assert.Contains(t, out, "ok.txt", "the paths that landed are still reported")
}

func TestIntegration_DownloadVerifiesDigestsFromTheStep(t *testing.T) {
	// The manifest carries a digest per asset; one of them is wrong, which is
	// the case the feature exists for.
	good := sha256.Sum256([]byte("first"))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/index":
			base := "http://" + r.Host
			writeJSON(w, map[string]any{"assets": []any{
				map[string]any{"name": "one.txt", "url": base + "/files/one", "sha256": hex.EncodeToString(good[:])},
				map[string]any{"name": "two.txt", "url": base + "/files/two", "sha256": strings.Repeat("ff", 32)},
			}})
		case "/files/one":
			_, _ = w.Write([]byte("first"))
		default:
			_, _ = w.Write([]byte("second-file"))
		}
	}))
	defer srv.Close()
	swapHTTPClient(t, srv)
	swapDownloadClient(t, srv)
	dir := t.TempDir()

	cfg, err := loadStr(t, `<config name="dl"><command name="grab">
		<steps><step name="index"><run><request><url>`+srv.URL+`/index</url></request></run></step></steps>
		<download over="result.index.assets">
			<url><value name="url"/></url>
			<to><value name="name"/></to>
			<hash algo="sha256"><value name="sha256"/></hash>
		</download>
	</command></config>`)
	require.NoError(t, err)

	code, out, errOut := execCmdFull(t, cfg, "grab", "--download-dir", dir)
	assert.Equal(t, 1, code)
	assert.Contains(t, errOut, "sha256 ok", "a verification that happened is visible")
	assert.Contains(t, errOut, "sha256 mismatch: expected "+strings.Repeat("ff", 32))
	assert.FileExists(t, filepath.Join(dir, "one.txt"))
	assert.NoFileExists(t, filepath.Join(dir, "two.txt"), "the file that failed its digest is not kept")
	assert.NotContains(t, out, "two.txt")
}

// binaryBody is 64 KiB covering every byte value, including sequences that are
// not valid UTF-8. Anything that treats a payload as text mangles it.
func binaryBody() []byte {
	body := make([]byte, 65536)
	for i := range body {
		body[i] = byte(i % 256)
	}
	return body
}

func TestIntegration_DownloadIsByteExact(t *testing.T) {
	body := binaryBody()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(body)
	}))
	defer srv.Close()
	swapDownloadClient(t, srv)
	dir := t.TempDir()

	cfg, err := loadStr(t, `<config name="dl"><command name="grab">
		<download><url>`+srv.URL+`/blob</url><to>blob.bin</to></download>
	</command></config>`)
	require.NoError(t, err)

	code, _, errOut := execCmdFull(t, cfg, "grab", "--download-dir", dir)
	require.Equal(t, 0, code, "stderr: %s", errOut)

	got, err := os.ReadFile(filepath.Join(dir, "blob.bin"))
	require.NoError(t, err)
	assert.Equal(t, body, got, "a download is bytes, not text")
}

// The same guarantee for a request streamed to a redirect: the body is the
// caller's file, so not one byte is added to it.
func TestIntegration_RedirectedRequestIsByteExact(t *testing.T) {
	body := binaryBody()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(body)
	}))
	defer srv.Close()
	swapHTTPClient(t, srv)

	cfg, err := loadStr(t, `<config name="dl"><command name="cat">
		<run><request><url>`+srv.URL+`/blob</url></request></run>
	</command></config>`)
	require.NoError(t, err)

	code, out, _ := execCmdFull(t, cfg, "cat")
	require.Equal(t, 0, code)
	assert.Equal(t, body, []byte(out), "no trailing newline into a redirect")
}

func TestIntegration_DownloadPlanErrorStopsBeforeFetching(t *testing.T) {
	cfg, err := loadStr(t, `<config name="dl"><command name="grab">
		<download over="result.nothing"><url><value name="item"/></url></download>
	</command></config>`)
	require.NoError(t, err)

	code, _, errOut := execCmdFull(t, cfg, "grab")
	assert.Equal(t, 1, code)
	assert.Contains(t, errOut, `over="result.nothing" resolved to nothing`)
}

func TestIntegration_DownloadSkippedEntirelySaysSo(t *testing.T) {
	cfg, err := loadStr(t, `<config name="dl"><command name="grab">
		<flag name="save" type="bool"/>
		<download when="{{.flag.save}}"><url>https://example.invalid/f</url></download>
	</command></config>`)
	require.NoError(t, err)

	code, _, errOut := execCmdFull(t, cfg, "grab")
	assert.Equal(t, 0, code)
	assert.Contains(t, errOut, "no downloads", "a run that fetched nothing says why")
}

func TestIntegration_DownloadLeafIgnoresAnInheritedRun(t *testing.T) {
	srv, _ := assetServer(t)
	swapDownloadClient(t, srv)
	dir := t.TempDir()

	// The ancestor's <run> would write a marker file if a <download> leaf ran
	// it on the way to the queue.
	marker := filepath.Join(dir, "ran-the-command")
	cfg, err := loadStr(t, `<config name="dl">
		<run>touch `+marker+`</run>
		<command name="grab">
			<download><url>`+srv.URL+`/files/one</url><to>one.txt</to></download>
		</command>
	</config>`)
	require.NoError(t, err)

	code, _, errOut := execCmdFull(t, cfg, "grab", "--download-dir", dir)
	require.Equal(t, 0, code, "stderr: %s", errOut)
	assert.FileExists(t, filepath.Join(dir, "one.txt"))
	assert.NoFileExists(t, marker, "the hand-off is the leaf's action")
}

func TestIntegration_DownloadStepFailureSkipsTheQueue(t *testing.T) {
	dir := t.TempDir()
	cfg, err := loadStr(t, `<config name="dl"><command name="grab">
		<steps><step name="probe"><run>exit 3</run></step></steps>
		<download><url>https://example.invalid/f</url><to>never.bin</to></download>
	</command></config>`)
	require.NoError(t, err)

	code, _, _ := execCmdFull(t, cfg, "grab", "--download-dir", dir)
	assert.Equal(t, 3, code)
	assert.NoFileExists(t, filepath.Join(dir, "never.bin"))
}

func TestMCP_DownloadLeafReportsWhatLanded(t *testing.T) {
	srv, _ := assetServer(t)
	swapDownloadClient(t, srv)
	dir := t.TempDir()

	prev := downloadDefaults
	t.Cleanup(func() { downloadDefaults = prev })
	installDownloads(&Config{Downloads: &Downloads{Concurrency: 2, Dir: dir}})

	out, isErr := mcpRunDownloads([]Download{
		{URL: srv.URL + "/files/one", To: "one.txt"},
		{URL: srv.URL + "/nope", To: "bad.txt"},
	}, map[string]any{})

	assert.True(t, isErr, "a failed transfer is an error to the caller")
	assert.Contains(t, out, "one.txt (5 B)")
	assert.Contains(t, out, "failed "+srv.URL+"/nope")
	assert.FileExists(t, filepath.Join(dir, "one.txt"))
}

func TestMCP_DownloadLeafWithNothingToDo(t *testing.T) {
	prev := downloadDefaults
	t.Cleanup(func() { downloadDefaults = prev })
	installDownloads(nil)

	out, isErr := mcpRunDownloads([]Download{{When: "false", URL: "https://h/f"}}, map[string]any{})
	assert.False(t, isErr)
	assert.Contains(t, out, "no downloads")

	out, isErr = mcpRunDownloads([]Download{{URL: "{{"}}, map[string]any{})
	assert.True(t, isErr)
	assert.Contains(t, out, "render url")
}
