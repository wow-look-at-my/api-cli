package main

import (
	"compress/gzip"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The built-in client never sets Accept-Encoding, so net/http offers gzip on
// its own and unwraps the answer. This pins that: a compressed transfer lands
// decompressed, and it arrives without a length, because the header the server
// sent described the compressed bytes.
func TestDownload_AcceptsGzipAndLosesTheLength(t *testing.T) {
	payload := make([]byte, 64*1024)
	for i := range payload {
		payload[i] = byte('a' + i%26)
	}

	var offered string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		offered = r.Header.Get("Accept-Encoding")
		w.Header().Set("Content-Encoding", "gzip")
		zw := gzip.NewWriter(w)
		_, _ = zw.Write(payload)
		require.NoError(t, zw.Close())
	}))
	t.Cleanup(srv.Close)

	dest := filepath.Join(t.TempDir(), "asset.bin")
	q := testQueue(t, srv, 1, 1)
	items := collect(t, q, downloadSpec{URL: srv.URL + "/asset.bin", Dest: dest})

	require.Len(t, items, 1)
	require.NoError(t, items[0].failure())
	assert.Contains(t, offered, "gzip", "the transfer asks for compression")

	got, err := os.ReadFile(dest)
	require.NoError(t, err)
	assert.Equal(t, payload, got, "the file holds the decompressed bytes")

	assert.Negative(t, items[0].total.Load(),
		"a compressed body reports no length for the file, which is why its row shows no percentage")
	assert.EqualValues(t, len(payload), items[0].done.Load(),
		"progress counts the bytes the file gets, not the bytes the wire carried")
}
