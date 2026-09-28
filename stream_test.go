package main

import (
	"bytes"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// collectChunks drives the shipped chunker the way the run path does and returns
// every chunk in order. It is the a single helper the chunking tests share, so
// each test asserts on real chunker output rather than on a re-implementation.
func collectChunks(t *testing.T, src io.Reader, mode string, size int) [][]byte {
	t.Helper()
	c := newChunker(src, mode, size)
	var out [][]byte
	for {
		chunk, err := c.next()
		if err == io.EOF {
			return out
		}
		require.NoError(t, err)
		out = append(out, chunk)
	}
}

// joined concatenates chunks, which is the byte the criteria care about: the
// chunks in order must be the source exactly.
func joined(chunks [][]byte) []byte {
	var b bytes.Buffer
	for _, c := range chunks {
		b.Write(c)
	}
	return b.Bytes()
}

func TestParseChunkSize_Units(t *testing.T) {
	for raw, want := range map[string]int{
		"1":      1,
		"4096":   4096,
		"4kb":    4 << 10,
		"4KB":    4 << 10,
		"64k":    64 << 10,
		"4m":     4 << 20,
		"4mb":    4 << 20,
		"1gb":    1 << 30,
		" 4 kb ": 4 << 10,
		"512b":   512,
	} {
		got, err := parseChunkSize(raw)
		require.NoErrorf(t, err, "chunk=%q", raw)
		assert.Equalf(t, want, got, "chunk=%q", raw)
	}
}

func TestParseChunkSize_Rejects(t *testing.T) {
	for _, raw := range []string{"", "  ", "0", "0kb", "kb", "4xb", "4 kbb", "-4kb", "4.5kb", "0x10"} {
		_, err := parseChunkSize(raw)
		assert.Errorf(t, err, "chunk=%q must be rejected", raw)
	}
}

func TestChunker_FixedSizeExactMultiple(t *testing.T) {
	src := []byte("abcdefgh")
	chunks := collectChunks(t, bytes.NewReader(src), streamModeBytes, 4)
	require.Len(t, chunks, 2)
	assert.Equal(t, []byte("abcd"), chunks[0])
	assert.Equal(t, []byte("efgh"), chunks[1])
	assert.Equal(t, src, joined(chunks))
}

func TestChunker_FixedSizeRemainderIsLastChunk(t *testing.T) {
	src := []byte("abcdefghij")
	chunks := collectChunks(t, bytes.NewReader(src), streamModeBytes, 4)
	require.Len(t, chunks, 3)
	assert.Equal(t, []byte("abcd"), chunks[0])
	assert.Equal(t, []byte("efgh"), chunks[1])
	assert.Equal(t, []byte("ij"), chunks[2], "the last chunk is the remainder")
	assert.Equal(t, src, joined(chunks))
}

func TestChunker_FixedSizeSmallerThanOneChunk(t *testing.T) {
	src := []byte("abc")
	chunks := collectChunks(t, bytes.NewReader(src), streamModeBytes, 4096)
	require.Len(t, chunks, 1)
	assert.Equal(t, src, chunks[0])
}

func TestChunker_EmptySourceYieldsNoChunk(t *testing.T) {
	chunks := collectChunks(t, bytes.NewReader(nil), streamModeBytes, 4)
	assert.Empty(t, chunks, "an empty source has nothing to emit, not one empty chunk")
}

// chunkedReader hands its data over a single byte at a time, so a chunker
// that trusted a single read to fill a chunk would produce the wrong boundaries.
type chunkedReader struct {
	data []byte
	step int
}

func (r *chunkedReader) Read(p []byte) (int, error) {
	if len(r.data) == 0 {
		return 0, io.EOF
	}
	n := r.step
	if n > len(p) {
		n = len(p)
	}
	if n > len(r.data) {
		n = len(r.data)
	}
	copy(p, r.data[:n])
	r.data = r.data[n:]
	return n, nil
}

func TestChunker_FixedSizeIgnoresHowTheSourceSplits(t *testing.T) {
	src := []byte("abcdefghij")
	chunks := collectChunks(t, &chunkedReader{data: src, step: 1}, streamModeBytes, 4)
	assert.Equal(t, src, joined(chunks))
	require.Len(t, chunks, 3)
	assert.Len(t, chunks[0], 4)
	assert.Len(t, chunks[1], 4)
	assert.Len(t, chunks[2], 2)
}

func TestChunker_BinaryRoundTrip(t *testing.T) {
	// Every byte value, including NUL and sequences that are not valid UTF-8.
	src := make([]byte, 4096)
	for i := range src {
		src[i] = byte(i)
	}
	chunks := collectChunks(t, bytes.NewReader(src), streamModeBytes, 64)
	assert.Equal(t, src, joined(chunks))
}

func TestChunker_LineModeOneChunkPerLine(t *testing.T) {
	src := []byte("alpha\nbeta\ngamma\n")
	chunks := collectChunks(t, bytes.NewReader(src), streamModeLines, 0)
	require.Len(t, chunks, 3)
	assert.Equal(t, []byte("alpha\n"), chunks[0])
	assert.Equal(t, []byte("beta\n"), chunks[1])
	assert.Equal(t, []byte("gamma\n"), chunks[2], "the newline belongs to the chunk it ends")
	assert.Equal(t, src, joined(chunks))
}

func TestChunker_LineModeUnterminatedTail(t *testing.T) {
	src := []byte("alpha\nbeta")
	chunks := collectChunks(t, bytes.NewReader(src), streamModeLines, 0)
	require.Len(t, chunks, 2)
	assert.Equal(t, []byte("alpha\n"), chunks[0])
	assert.Equal(t, []byte("beta"), chunks[1], "a final line with no newline is still a chunk")
	assert.Equal(t, src, joined(chunks))
}

func TestChunker_LineModeBlankLinesAreChunks(t *testing.T) {
	src := []byte("a\n\nb\n")
	chunks := collectChunks(t, bytes.NewReader(src), streamModeLines, 0)
	require.Len(t, chunks, 3)
	assert.Equal(t, []byte("a\n"), chunks[0])
	assert.Equal(t, []byte("\n"), chunks[1])
	assert.Equal(t, []byte("b\n"), chunks[2])
	assert.Equal(t, src, joined(chunks))
}

func TestChunker_LineModeLongerThanTheReadBlock(t *testing.T) {
	// A line several read blocks long must still arrive as a single chunk.
	long := strings.Repeat("x", streamReadBlock*2+17)
	src := []byte(long + "\nsecond\n")
	chunks := collectChunks(t, bytes.NewReader(src), streamModeLines, 0)
	require.Len(t, chunks, 2)
	assert.Equal(t, []byte(long+"\n"), chunks[0], "a line longer than the buffer stays intact")
	assert.Equal(t, []byte("second\n"), chunks[1])
	assert.Equal(t, src, joined(chunks))
}

func TestChunker_LineModeEmptySourceYieldsNoChunk(t *testing.T) {
	chunks := collectChunks(t, bytes.NewReader(nil), streamModeLines, 0)
	assert.Empty(t, chunks)
}

func TestChunker_LineModeBinaryPayloadWithNulBytes(t *testing.T) {
	src := []byte("a\x00b\n\xff\xfe tail")
	chunks := collectChunks(t, bytes.NewReader(src), streamModeLines, 0)
	require.Len(t, chunks, 2)
	assert.Equal(t, []byte("a\x00b\n"), chunks[0])
	assert.Equal(t, []byte("\xff\xfe tail"), chunks[1])
	assert.Equal(t, src, joined(chunks))
}

// growingReader emits a prefix and then blocks until released, so a test can
// observe how much a chunker hands over before the source is finished.
type growingReader struct {
	first   []byte
	second  []byte
	release chan struct{}
	served  bool
}

func (r *growingReader) Read(p []byte) (int, error) {
	if !r.served {
		r.served = true
		n := copy(p, r.first)
		return n, nil
	}
	if r.second != nil {
		<-r.release
		n := copy(p, r.second)
		r.second = nil
		return n, nil
	}
	return 0, io.EOF
}

func TestChunker_IncrementalBeforeSourceFinishes(t *testing.T) {
	// The source hands over a single chunk's worth and then holds. the
	// earliest chunk must already be readable, which is what makes this
	// incremental rather than a buffer-everything implementation.
	src := &growingReader{
		first:   []byte("abcd"),
		second:  []byte("efgh"),
		release: make(chan struct{}),
	}
	c := newChunker(src, streamModeBytes, 4)

	first, err := c.next()
	require.NoError(t, err)
	assert.Equal(t, []byte("abcd"), first, "the first chunk arrives before the source finishes")

	close(src.release)
	second, err := c.next()
	require.NoError(t, err)
	assert.Equal(t, []byte("efgh"), second)

	_, err = c.next()
	assert.Equal(t, io.EOF, err)
}
