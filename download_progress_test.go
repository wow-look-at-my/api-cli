package main

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// waitForBytes polls an in-flight item until it reports bytes, and fails when
// the transfer holds bytes that its counter does not show.
func waitForBytes(t *testing.T, item *downloadItem) int64 {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if n := item.done.Load(); n > 0 {
			require.Equal(t, dlActive, item.state.Load(), "the bytes must show while the transfer is still open")
			return n
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("an open transfer with no Content-Length still reads 0 B")
	return 0
}

func TestDownload_UnknownLengthCountsBytesMidTransfer(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(make([]byte, 64*1024))
		w.(http.Flusher).Flush()
		<-release
		_, _ = w.Write(make([]byte, 1024))
	}))
	t.Cleanup(srv.Close)

	q := testQueue(t, srv, 1, 0)
	batch := q.batch(nil, nil)
	item := batch.add(downloadSpec{URL: srv.URL + "/big.bin", Dest: filepath.Join(t.TempDir(), "big.bin")})

	n := waitForBytes(t, item)
	assert.Negative(t, item.total.Load(), "a chunked body reports no length")
	close(release)
	batch.wait()
	require.NoError(t, item.failure())
	assert.EqualValues(t, 65*1024, item.done.Load())
	assert.Positive(t, n)
}

func TestDownload_TransportCountsBytesMidTransfer(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	t.Cleanup(srv.Close)

	q := testQueue(t, srv, 1, 0)
	batch := q.batch(nil, nil)
	item := batch.add(downloadSpec{
		URL:       "http://example.invalid/big.bin",
		Dest:      filepath.Join(t.TempDir(), "big.bin"),
		Transport: &downloadTransport{Name: "sh", Argv: []string{"sh", "-c", "head -c 65536 /dev/zero; sleep 1; head -c 1024 /dev/zero"}},
	})

	waitForBytes(t, item)
	batch.wait()
	require.NoError(t, item.failure())
	assert.EqualValues(t, 64*1024+1024, item.done.Load())
}
