package main

import (
	"crypto/md5"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testQueue builds a queue wired to srv, isolated from the process-wide one.
func testQueue(t *testing.T, srv *httptest.Server, concurrency, retries int) *downloadQueue {
	t.Helper()
	prevDelay := retryDelay
	retryDelay = time.Millisecond
	t.Cleanup(func() { retryDelay = prevDelay })

	q := newDownloadQueue(concurrency, retries)
	q.client = srv.Client()
	return q
}

// collect runs specs through a fresh batch and returns the finished items.
func collect(t *testing.T, q *downloadQueue, specs ...downloadSpec) []*downloadItem {
	t.Helper()
	batch := q.batch(nil, nil)
	for _, s := range specs {
		batch.add(s)
	}
	return batch.wait()
}

func TestDownloadQueue_WritesBodyToDestination(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("payload-bytes"))
	}))
	defer srv.Close()

	dest := filepath.Join(t.TempDir(), "nested", "out.bin")
	items := collect(t, testQueue(t, srv, 2, 0), downloadSpec{URL: srv.URL + "/a.bin", Dest: dest})

	require.Len(t, items, 1)
	assert.NoError(t, items[0].failure())
	assert.Equal(t, dlDone, items[0].state.Load())
	assert.Equal(t, int64(len("payload-bytes")), items[0].done.Load())
	assert.Equal(t, int64(len("payload-bytes")), items[0].total.Load())
	body, err := os.ReadFile(dest)
	require.NoError(t, err)
	assert.Equal(t, "payload-bytes", string(body))
	assert.NoFileExists(t, dest+".part", "the .part sibling is renamed away on success")
}

func TestDownloadQueue_DirectoryDestinationNamesFromURL(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("x"))
	}))
	defer srv.Close()

	dir := t.TempDir()
	items := collect(t, testQueue(t, srv, 1, 0),
		downloadSpec{URL: srv.URL + "/pkg/thing-1.2.tgz?token=s", Dest: dir, DestIsDir: true})

	assert.Equal(t, filepath.Join(dir, "thing-1.2.tgz"), items[0].dest())
	assert.FileExists(t, filepath.Join(dir, "thing-1.2.tgz"))
}

func TestDownloadQueue_ContentDispositionNamesTheFile(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Disposition", `attachment; filename="report Q3.csv"`)
		_, _ = w.Write([]byte("a,b\n"))
	}))
	defer srv.Close()

	dir := t.TempDir()
	items := collect(t, testQueue(t, srv, 1, 0),
		downloadSpec{URL: srv.URL + "/download?id=99", Dest: dir, DestIsDir: true})

	assert.Equal(t, filepath.Join(dir, "report Q3.csv"), items[0].dest())
	assert.Equal(t, "report Q3.csv", items[0].label())
}

func TestDownloadQueue_ClientErrorFailsWithoutRetrying(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		http.Error(w, "nope", http.StatusNotFound)
	}))
	defer srv.Close()

	dest := filepath.Join(t.TempDir(), "missing.bin")
	items := collect(t, testQueue(t, srv, 1, 3), downloadSpec{URL: srv.URL + "/missing", Dest: dest})

	assert.Equal(t, int32(1), hits.Load(), "404 is the answer, not a hiccup")
	assert.Equal(t, dlFailed, items[0].state.Load())
	require.Error(t, items[0].failure())
	assert.Contains(t, items[0].failure().Error(), "404")
	assert.NoFileExists(t, dest)
	assert.NoFileExists(t, dest+".part")
}

func TestDownloadQueue_RetriesServerErrorThenSucceeds(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if hits.Add(1) < 3 {
			http.Error(w, "later", http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()

	dest := filepath.Join(t.TempDir(), "flaky.bin")
	items := collect(t, testQueue(t, srv, 1, 3), downloadSpec{URL: srv.URL + "/flaky", Dest: dest})

	assert.Equal(t, int32(3), hits.Load())
	assert.Equal(t, dlDone, items[0].state.Load())
	assert.Equal(t, int64(2), items[0].done.Load(), "a retry restarts the byte count")
	assert.FileExists(t, dest)
}

func TestDownloadQueue_ExhaustedRetriesReportTheLastError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "down", http.StatusBadGateway)
	}))
	defer srv.Close()

	items := collect(t, testQueue(t, srv, 1, 2),
		downloadSpec{URL: srv.URL + "/x", Dest: filepath.Join(t.TempDir(), "x")})

	assert.Equal(t, dlFailed, items[0].state.Load())
	assert.ErrorContains(t, items[0].failure(), "502")
}

func TestDownloadQueue_VerifiesTheDeclaredDigest(t *testing.T) {
	body := "payload-bytes"
	sum := sha256.Sum256([]byte(body))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	dest := filepath.Join(t.TempDir(), "ok.bin")
	items := collect(t, testQueue(t, srv, 1, 0), downloadSpec{
		URL: srv.URL + "/f", Dest: dest,
		Hash: hex.EncodeToString(sum[:]), HashAlgo: "sha256",
	})

	assert.NoError(t, items[0].failure())
	assert.FileExists(t, dest)
}

func TestDownloadQueue_MismatchedDigestFailsWithoutKeepingTheFile(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		_, _ = w.Write([]byte("not what the manifest promised"))
	}))
	defer srv.Close()

	dest := filepath.Join(t.TempDir(), "tampered.bin")
	wrong := strings.Repeat("ab", 32)
	items := collect(t, testQueue(t, srv, 1, 3), downloadSpec{
		URL: srv.URL + "/f", Dest: dest,
		Hash: wrong, HashAlgo: "sha256",
	})

	assert.Equal(t, dlFailed, items[0].state.Load())
	assert.ErrorContains(t, items[0].failure(), "sha256 mismatch: expected "+wrong)
	assert.NoFileExists(t, dest, "a file that failed its digest never gets the real name")
	assert.NoFileExists(t, dest+".part")
	assert.Equal(t, int32(1), hits.Load(), "the bytes arrived intact; refetching them cannot change the digest")
}

func TestDownloadQueue_VerifiesEveryAlgorithm(t *testing.T) {
	body := []byte("checksum me")
	sums := map[string]string{
		"md5":    fmt.Sprintf("%x", md5.Sum(body)),
		"sha1":   fmt.Sprintf("%x", sha1.Sum(body)),
		"sha256": fmt.Sprintf("%x", sha256.Sum256(body)),
		"sha512": fmt.Sprintf("%x", sha512.Sum512(body)),
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	dir := t.TempDir()
	q := testQueue(t, srv, 2, 0)
	for algo, sum := range sums {
		t.Run(algo, func(t *testing.T) {
			items := collect(t, q, downloadSpec{
				URL: srv.URL + "/f", Dest: filepath.Join(dir, algo+".bin"),
				Hash: sum, HashAlgo: algo,
			})
			assert.NoError(t, items[0].failure())
		})
	}
}

func TestNewHasher_NoDigestMeansNoHashing(t *testing.T) {
	assert.Nil(t, newHasher("sha256", ""), "an absent digest must not cost a hash pass")
	assert.NotNil(t, newHasher("", "abc"), "an unnamed algorithm falls back to the default")
}

func TestDownloadQueue_SendsDeclaredHeaders(t *testing.T) {
	var auth, cookie string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth, cookie = r.Header.Get("Authorization"), r.Header.Get("Cookie")
		_, _ = w.Write([]byte("."))
	}))
	defer srv.Close()

	collect(t, testQueue(t, srv, 1, 0), downloadSpec{
		URL:  srv.URL + "/f",
		Dest: filepath.Join(t.TempDir(), "f"),
		Headers: []renderedHeader{
			{Name: "Authorization", Value: "Bearer tok"},
			{Name: "Cookie", Value: "sid=abc; region=eu"},
		},
	})

	assert.Equal(t, "Bearer tok", auth)
	assert.Equal(t, "sid=abc; region=eu", cookie)
}

func TestDownloadQueue_RespectsConcurrencyLimit(t *testing.T) {
	var mu sync.Mutex
	inFlight, peak := 0, 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		inFlight++
		if inFlight > peak {
			peak = inFlight
		}
		mu.Unlock()
		time.Sleep(20 * time.Millisecond)
		mu.Lock()
		inFlight--
		mu.Unlock()
		_, _ = w.Write([]byte("."))
	}))
	defer srv.Close()

	dir := t.TempDir()
	specs := make([]downloadSpec, 0, 8)
	for i := 0; i < 8; i++ {
		specs = append(specs, downloadSpec{
			URL:  fmt.Sprintf("%s/f%d", srv.URL, i),
			Dest: filepath.Join(dir, fmt.Sprintf("f%d", i)),
		})
	}
	items := collect(t, testQueue(t, srv, 2, 0), specs...)

	require.Len(t, items, 8)
	for _, item := range items {
		assert.NoError(t, item.failure())
	}
	mu.Lock()
	defer mu.Unlock()
	assert.LessOrEqual(t, peak, 2, "concurrency 2 must not run a third transfer")
	assert.Greater(t, peak, 1, "concurrency 2 must actually overlap transfers")
}

func TestSharedQueue_IsReusedAcrossBatches(t *testing.T) {
	serial(t)
	resetSharedQueue()
	t.Cleanup(resetSharedQueue)

	first := sharedQueue(3, 1)
	assert.Same(t, first, sharedQueue(9, 9), "one process, one pool")
	assert.Equal(t, 3, first.concurrency)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("hi"))
	}))
	defer srv.Close()
	first.client = srv.Client()

	dir := t.TempDir()
	for i := 0; i < 2; i++ {
		batch := first.batch(nil, nil)
		batch.add(downloadSpec{URL: srv.URL + "/a", Dest: filepath.Join(dir, fmt.Sprintf("a%d", i))})
		items := batch.wait()
		require.Len(t, items, 1, "a batch only ever sees its own items")
		assert.NoError(t, items[0].failure())
	}
}

func TestNewDownloadQueue_ClampsSettings(t *testing.T) {
	q := newDownloadQueue(0, -5)
	assert.Equal(t, 1, q.concurrency)
	assert.Equal(t, 0, q.retries)
}

func TestSanitizeFilename(t *testing.T) {
	cases := map[string]string{
		"report.csv":            "report.csv",
		"../../etc/passwd":      "passwd",
		`..\..\windows\sys.dll`: "sys.dll",
		"/absolute/path.bin":    "path.bin",
		"..":                    "",
		"":                      "",
	}
	for in, want := range cases {
		assert.Equal(t, want, sanitizeFilename(in), "sanitizeFilename(%q)", in)
	}
}

func TestURLFilename(t *testing.T) {
	assert.Equal(t, "f.tar.gz", urlFilename("https://h/a/b/f.tar.gz?x=1"))
	assert.Equal(t, "download", urlFilename("https://h/"))
	assert.Equal(t, "download", urlFilename("https://h"))
}

func TestRetryableStatus(t *testing.T) {
	for _, code := range []int{408, 429, 500, 503} {
		assert.True(t, retryableStatus(code), "%d", code)
	}
	for _, code := range []int{400, 401, 403, 404, 410} {
		assert.False(t, retryableStatus(code), "%d", code)
	}
}

func TestTallyDownloads(t *testing.T) {
	now := time.Now()
	mk := func(state int32, done, total int64, started time.Time) *downloadItem {
		item := &downloadItem{}
		item.state.Store(state)
		item.done.Store(done)
		item.total.Store(total)
		if !started.IsZero() {
			item.start.Store(started.UnixNano())
		}
		return item
	}
	items := []*downloadItem{
		mk(dlDone, 100, 100, now.Add(-4*time.Second)),
		mk(dlActive, 50, 200, now.Add(-2*time.Second)),
		mk(dlQueued, 0, -1, time.Time{}),
		mk(dlFailed, 0, -1, now.Add(-time.Second)),
	}

	got := tallyDownloads(items, now)
	assert.Equal(t, 1, got.Active)
	assert.Equal(t, 1, got.Queued)
	assert.Equal(t, 1, got.Done)
	assert.Equal(t, 1, got.Failed)
	assert.Equal(t, int64(150), got.Bytes)
	assert.False(t, got.TotalKnown, "a queued file of unreported length makes the total a floor")
	assert.Equal(t, int64(300), got.Total)
	assert.InDelta(t, 4*time.Second, got.Elapsed, float64(time.Second))
}

func TestTallyDownloads_FinishedRunHasASettledTotal(t *testing.T) {
	now := time.Now()
	done := &downloadItem{}
	done.state.Store(dlDone)
	done.done.Store(100)
	done.total.Store(100)
	done.start.Store(now.Add(-time.Second).UnixNano())
	failed := &downloadItem{}
	failed.state.Store(dlFailed)
	failed.total.Store(-1)

	got := tallyDownloads([]*downloadItem{done, failed}, now)
	assert.True(t, got.TotalKnown, "a failed download never gets a size, and nothing is still coming")
	assert.Equal(t, int64(100), got.Total)
}

func TestProgressOf(t *testing.T) {
	now := time.Now()
	p := progressOf(500, 1000, now.Add(-5*time.Second), now)
	assert.InDelta(t, 0.5, p.Fraction, 0.001)
	assert.InDelta(t, 100.0, p.Speed, 1.0)
	assert.True(t, p.HasETA)
	assert.InDelta(t, float64(5*time.Second), float64(p.ETA), float64(200*time.Millisecond))

	unknown := progressOf(500, -1, now.Add(-time.Second), now)
	assert.Equal(t, float64(-1), unknown.Fraction)
	assert.False(t, unknown.HasETA)

	idle := progressOf(0, 100, time.Time{}, now)
	assert.Zero(t, idle.Speed)
	assert.False(t, idle.HasETA)

	over := progressOf(120, 100, now.Add(-time.Second), now)
	assert.Equal(t, float64(1), over.Fraction, "a server that undercounts must not overfill the bar")
}
