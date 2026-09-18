package main

import (
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The tests drive the shipped entry point with it, so what they assert is what
// a user's config gets.
func streamConfig(streamXML, source string) *Config {
	return &Config{
		Name: "t",
		Commands: []Command{{
			Name:   "log",
			Stream: mustBuildStream(streamXML),
			Command: &Cmd{
				Shell:    true,
				Template: source,
			},
		}},
	}
}

// mustBuildStream parses a <stream> declaration from inline XML, exactly as the
// config loader does.
func mustBuildStream(inner string) *Stream {
	node, err := parseDOM([]byte("<stream " + inner + "/>"))
	if err != nil {
		panic(err)
	}
	st, err := buildStream(node)
	if err != nil {
		panic(err)
	}
	return st
}

// mustBuildStreamWith parses a <stream> declaration that carries children, so a
// per-chunk step is exercised through the real parser too.
func mustBuildStreamWith(attrs, children string) *Stream {
	doc := "<stream " + attrs + ">" + children + "</stream>"
	node, err := parseDOM([]byte(doc))
	if err != nil {
		panic(err)
	}
	st, err := buildStream(node)
	if err != nil {
		panic(err)
	}
	return st
}

func TestIntegration_StreamFixedSizeChunkBoundaries(t *testing.T) {
	cfg := streamConfig(`mode="bytes" chunk="4"`, `printf 'abcdefghij'`)
	code, out := execCmd(t, cfg, "log")
	require.Equal(t, 0, code)
	assert.Equal(t, "abcdefghij", out, "the chunks concatenate to the source")
	assert.Len(t, out, 10)
}

func TestIntegration_StreamFixedSizeHumanUnits(t *testing.T) {
	cfg := streamConfig(`chunk="1kb"`, `head -c 2048 /dev/zero | tr '\0' 'x'`)
	code, out := execCmd(t, cfg, "log")
	require.Equal(t, 0, code)
	assert.Equal(t, strings.Repeat("x", 2048), out)
}

func TestIntegration_StreamFixedSizeBinaryRoundTrip(t *testing.T) {
	// A binary source: every byte value, NUL and invalid UTF-8 included. The
	// streamed output must be the input byte for byte.
	src := make([]byte, 4096)
	for i := range src {
		src[i] = byte(i % 256)
	}
	path := filepath.Join(t.TempDir(), "blob.bin")
	require.NoError(t, os.WriteFile(path, src, 0o600))

	cfg := streamConfig(`mode="bytes" chunk="700"`, `cat `+shellQuote(path))
	code, out := execCmd(t, cfg, "log")
	require.Equal(t, 0, code)
	assert.Equal(t, src, []byte(out), "a binary source survives chunking unchanged")
	assert.Equal(t,
		fmt.Sprintf("%x", sha256.Sum256(src)),
		fmt.Sprintf("%x", sha256.Sum256([]byte(out))))
}

func TestIntegration_StreamLineModeChunkBoundaries(t *testing.T) {
	cfg := streamConfig(`mode="lines"`, `printf 'alpha\nbeta\ngamma\n'`)
	code, out := execCmd(t, cfg, "log")
	require.Equal(t, 0, code)
	assert.Equal(t, "alpha\nbeta\ngamma\n", out)
}

func TestIntegration_StreamLineModeUnterminatedTail(t *testing.T) {
	cfg := streamConfig(`mode="lines"`, `printf 'alpha\nbeta'`)
	code, out := execCmd(t, cfg, "log")
	require.Equal(t, 0, code)
	assert.Equal(t, "alpha\nbeta", out, "the unterminated final line is still emitted")
}

func TestIntegration_StreamSourceExitCodePropagates(t *testing.T) {
	// The source's own exit code is the leaf's, as it is on the unstreamed path.
	cfg := streamConfig(`mode="lines"`, `printf 'partial\n'; exit 7`)
	code, out := execCmd(t, cfg, "log")
	assert.Equal(t, 7, code)
	assert.Equal(t, "partial\n", out)
}

func TestIntegration_StreamPerChunkStepReplacesTheChunk(t *testing.T) {
	// The step uppercases each chunk. Chunk sizes must be preserved, so the
	// transformed output still concatenates to a 10-byte source.
	cfg := streamConfig(`chunk="4"`, `printf 'abcdefghij'`)
	cfg.Commands[0].Stream = mustBuildStreamWith(`chunk="4"`, `<run>tr a-z A-Z</run>`)
	code, out := execCmd(t, cfg, "log")
	require.Equal(t, 0, code)
	assert.Equal(t, "ABCDEFGHIJ", out)
}

func TestIntegration_StreamPerChunkStepMarksEachChunk(t *testing.T) {
	// The step emits a single line per chunk naming its index and byte count:
	// that makes the chunk count and the per-chunk sizes observable on
	// stdout, and they must agree with the chunking asserted above.
	cfg := streamConfig(`chunk="4"`, `printf 'abcdefghij'`)
	cfg.Commands[0].Stream = mustBuildStreamWith(`chunk="4"`,
		`<run>printf '%s:%s:{{.stream.size}}\n' {{.stream.index}} {{.stream.offset}}</run>`)
	code, out := execCmd(t, cfg, "log")
	require.Equal(t, 0, code)
	assert.Equal(t, "1:0:4\n2:4:4\n3:8:2\n", out,
		"three chunks of 4, 4 and 2 bytes, in that order")
}

func TestIntegration_StreamPerChunkStepFailureFailsTheLeaf(t *testing.T) {
	cfg := streamConfig(`chunk="4"`, `printf 'abcdefghij'`)
	cfg.Commands[0].Stream = mustBuildStreamWith(`chunk="4"`, `<run>exit 3</run>`)
	code, out, errOut := execCmdFull(t, cfg, "log")
	assert.NotEqual(t, 0, code, "a failing per-chunk step fails the leaf")
	assert.Empty(t, out, "the raw chunk is not emitted as if nothing went wrong")
	assert.Contains(t, errOut, "stream step", "the diagnostic says where it failed")
	assert.Contains(t, errOut, "chunk 1")
}

func TestIntegration_StreamLineModePerChunkStepCountsLines(t *testing.T) {
	// A filter over lines is the log case: the step sees a single line
	// and its output replaces that line.
	cfg := streamConfig(`mode="lines"`, `printf 'keep me\ndrop me\nkeep me too\n'`)
	cfg.Commands[0].Stream = mustBuildStreamWith(`mode="lines"`, `<run>sed '/drop/d'</run>`)
	code, out := execCmd(t, cfg, "log")
	require.Equal(t, 0, code)
	assert.Equal(t, "keep me\nkeep me too\n", out)
}

// chunkMarks reads the per-chunk timestamps a marking step left behind. Each
// line is a single chunk, in nanoseconds since the epoch, so a test can see
// when the chunker handed each chunk over rather than only that it finished.
func chunkMarks(t *testing.T, path string) []int64 {
	t.Helper()
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	var out []int64
	for _, line := range strings.Fields(string(raw)) {
		n, err := strconv.ParseInt(line, 10, 64)
		require.NoError(t, err)
		out = append(out, n)
	}
	return out
}

// markingStream builds a <stream> whose per-chunk step records the wall clock
// for every chunk and passes it through unchanged.
func markingStream(attrs, marksPath string) *Stream {
	return mustBuildStreamWith(attrs, `<run>date +%s%N >> `+shellQuote(marksPath)+`; cat</run>`)
}

func TestIntegration_StreamFirstChunkArrivesBeforeTheSourceEnds(t *testing.T) {
	// The source emits a single chunk, pauses seconds, then emits the next.
	// the earliest chunk must be handed over during that pause, which is
	// what makes this incremental rather than a buffer-everything implementation.
	t.Serial()

	marks := filepath.Join(t.TempDir(), "chunks.marks")
	cfg := streamConfig(`chunk="2"`, `printf aa; sleep 2; printf bb`)
	cfg.Commands[0].Stream = markingStream(`chunk="2"`, marks)

	ended := time.Now()
	code, out, errOut := execCmdFull(t, cfg, "log")
	ended = time.Now()
	require.Equal(t, 0, code, errOut)
	assert.Equal(t, "aabb", out, "the marking step passed both chunks through unchanged")

	stamps := chunkMarks(t, marks)
	require.Len(t, stamps, 2, "one mark per chunk")
	assert.Greater(t, ended.UnixNano()-stamps[0], int64(1500*time.Millisecond),
		"the first chunk was transformed and emitted about two seconds before the source finished, so it left while the source was paused")
	assert.Greater(t, stamps[1]-stamps[0], int64(1500*time.Millisecond),
		"the second chunk followed the source's pause")
}

func TestIntegration_StreamNeverBuffersTheWholeSource(t *testing.T) {
	// A source much larger than any internal buffer streams through as many
	// chunks, so the run proves the whole output was never captured earliest.
	t.Serial()

	const (
		total = 8 << 20
		chunk = 64 << 10
	)
	marks := filepath.Join(t.TempDir(), "chunks.marks")
	cfg := streamConfig(`chunk="64kb"`, fmt.Sprintf(`head -c %d /dev/zero | tr '\0' 'y'`, total))
	cfg.Commands[0].Stream = markingStream(`chunk="64kb"`, marks)

	code, out, errOut := execCmdFull(t, cfg, "log")
	require.Equal(t, 0, code, errOut)
	assert.Len(t, out, total, "every source byte reached stdout")
	assert.Equal(t, strings.Repeat("y", 128), out[:128], "the source bytes are unaltered")

	stamps := chunkMarks(t, marks)
	assert.Equal(t, total/chunk, len(stamps),
		"the source arrived as %d chunks of %d bytes, not one captured buffer", total/chunk, chunk)
}

func TestIntegration_StreamUnusableChunkSizeIsALoadError(t *testing.T) {
	for _, chunk := range []string{"", "0", "4xb", "kb"} {
		t.Run("chunk="+chunk, func(t *testing.T) {
			attrs := `chunk="` + chunk + `"`
			if chunk == "" {
				attrs = ""
			}
			cfg := streamConfig(attrs, `printf 'x'`)
			err := validate(cfg)
			require.Error(t, err, "chunk=%q must be rejected at load time", chunk)
			assert.Contains(t, err.Error(), "chunk", "the message names the attribute")
		})
	}
}

func TestIntegration_StreamUnknownModeIsALoadError(t *testing.T) {
	cfg := streamConfig(`mode="words" chunk="4"`, `printf 'x'`)
	err := validate(cfg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "mode")
}

func TestIntegration_StreamChunkSizeWithLineModeIsALoadError(t *testing.T) {
	cfg := streamConfig(`mode="lines" chunk="4"`, `printf 'x'`)
	err := validate(cfg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "chunk")
}

func TestIntegration_StreamWithoutARunIsALoadError(t *testing.T) {
	// A <stream> has no source of its own: unlike a <download>, which carries
	// a URL, the bytes are the leaf's run.
	cfg := &Config{
		Name: "t",
		Commands: []Command{{
			Name:   "log",
			Stream: mustBuildStream(`mode="lines"`),
		}},
	}
	err := validate(cfg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "needs a <run>")
}

func TestIntegration_StreamRejectsADownloadOnTheSameLeaf(t *testing.T) {
	// Both are the leaf's action, and accepting the pair would silently drop
	// the stream while the files transferred.
	cfg := streamConfig(`mode="lines"`, `printf 'x'`)
	cfg.Commands[0].Downloads = []Download{{URL: "https://example.test/f.bin", To: "f.bin"}}
	err := validate(cfg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "<download>")
}

func TestIntegration_StreamRejectsFieldsAndFormatAndTML(t *testing.T) {
	base := func() *Config {
		return streamConfig(`chunk="4"`, `printf 'x'`)
	}

	withFields := base()
	withFields.Commands[0].Fields = []FieldsBlock{{Fields: &Fields{}}}
	err := validate(withFields)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "<stream>")

	withFormat := base()
	withFormat.Commands[0].Format = &FormatRef{Inline: &Format{Views: []View{{Name: "v", Template: "x"}}}}
	err = validate(withFormat)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "<stream>")

	withTML := base()
	withTML.Commands[0].TML = &TML{Src: "x.tml"}
	err = validate(withTML)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "<stream>")
}

func TestIntegration_StreamRejectsResponseJQ(t *testing.T) {
	cfg := &Config{
		Name: "t",
		Commands: []Command{{
			Name:    "log",
			Stream:  mustBuildStream(`mode="lines"`),
			Request: &Request{URL: "https://example.test/log", Response: &Response{JQ: "."}},
		}},
	}
	err := validate(cfg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "<response jq=>")
}

func TestIntegration_StreamRejectsATransport(t *testing.T) {
	cfg := &Config{
		Name: "t",
		Transports: map[string]*Transport{
			"corp": {Name: "corp", Command: &Cmd{Shell: true, Template: "curl"}},
		},
		Commands: []Command{{
			Name:    "log",
			Stream:  mustBuildStream(`mode="lines"`),
			Request: &Request{URL: "https://example.test/log", Transport: "corp"},
		}},
	}
	err := validate(cfg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "buffers")
}

func TestBuildStream_RejectsUnknownAttributesAndChildren(t *testing.T) {
	node, err := parseDOM([]byte(`<stream chunk="4" chunkbytes="4"/>`))
	require.NoError(t, err)
	_, err = buildStream(node)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "chunkbytes")

	node, err = parseDOM([]byte(`<stream chunk="4"><filter>x</filter></stream>`))
	require.NoError(t, err)
	_, err = buildStream(node)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "filter")
}

func TestBuildStream_RejectsARequestStep(t *testing.T) {
	node, err := parseDOM([]byte(`<stream chunk="4"><run><request><url>https://x.test/</url></request></run></stream>`))
	require.NoError(t, err)
	_, err = buildStream(node)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "stdin")
}

func TestParseConfigXML_StreamDeclaration(t *testing.T) {
	src := `<config name="t">
	<run>printf 'hi'</run>
	<command name="log">
		<stream mode="lines">
			<run>cat</run>
		</stream>
	</command>
</config>`
	cfg, err := parseConfigXML([]byte(src))
	require.NoError(t, err)
	require.Len(t, cfg.Commands, 1)
	st := cfg.Commands[0].Stream
	require.NotNil(t, st)
	assert.Equal(t, "lines", st.Mode)
	require.NotNil(t, st.Step)
	assert.True(t, st.Step.Command.Defined())
	require.NoError(t, validate(cfg))
}

func TestParseConfigXML_StreamDeclaredTwiceIsRejected(t *testing.T) {
	src := `<config name="t">
	<run>printf 'hi'</run>
	<command name="log">
		<stream chunk="4"/>
		<stream mode="lines"/>
	</command>
</config>`
	_, err := parseConfigXML([]byte(src))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "once")
}

func TestParseConfigXML_StreamRequiresChunkInByteMode(t *testing.T) {
	src := `<config name="t">
	<run>printf 'hi'</run>
	<command name="log">
		<stream/>
	</command>
</config>`
	cfg, err := parseConfigXML([]byte(src))
	require.NoError(t, err)
	err = validate(cfg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "chunk")
}

func TestIntegration_StreamRejectsWatch(t *testing.T) {
	// The guard itself is what decides, so it is called directly: the stream
	// already runs until its source ends, and a watch frame would have to
	// capture the whole thing.
	cfg := streamConfig(`mode="lines"`, `printf 'x'`)
	err := watchable(watchRoot(t), cfg.Commands[0], "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--watch")
}

// A server writes its response in flushed pieces, so the body arrives in more
// than one read. Chunking it proves the request's body is a source like any
// other, rather than something the client had to finish receiving first.
func TestIntegration_StreamRequestSourceBodyIsChunked(t *testing.T) {
	body := []byte("abcdefghij")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(body[:4])
		w.(http.Flusher).Flush()
		_, _ = w.Write(body[4:])
	}))
	defer srv.Close()
	swapHTTPClient(t, srv)

	cfg := &Config{
		Name: "t",
		Commands: []Command{{
			Name:    "feed",
			Stream:  mustBuildStream(`chunk="4"`),
			Request: &Request{Method: "GET", URL: srv.URL + "/feed"},
		}},
	}
	code, out, errOut := execCmdFull(t, cfg, "feed")
	require.Equal(t, 0, code, errOut)
	assert.Equal(t, string(body), out, "the response body concatenates to the source")
}

func TestIntegration_StreamRequestSourceWithAPerChunkStep(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("alpha\nbeta\ngamma\n"))
	}))
	defer srv.Close()
	swapHTTPClient(t, srv)

	cfg := &Config{
		Name: "t",
		Commands: []Command{{
			Name:    "feed",
			Stream:  mustBuildStreamWith(`mode="lines"`, `<run>sed 's/a/A/g'</run>`),
			Request: &Request{Method: "GET", URL: srv.URL + "/feed"},
		}},
	}
	code, out, errOut := execCmdFull(t, cfg, "feed")
	require.Equal(t, 0, code, errOut)
	assert.Equal(t, "AlphA\nbetA\ngAmmA\n", out, "the step ran per line off the response body")
}

func TestIntegration_StreamRequestErrorStatusFailsTheRun(t *testing.T) {
	// A 4xx is the answer rather than the source, so the leaf fails instead of
	// streaming an error page.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte("not found\n"))
	}))
	defer srv.Close()
	swapHTTPClient(t, srv)

	cfg := &Config{
		Name: "t",
		Commands: []Command{{
			Name:    "feed",
			Stream:  mustBuildStream(`mode="lines"`),
			Request: &Request{Method: "GET", URL: srv.URL + "/missing"},
		}},
	}
	code, out, errOut := execCmdFull(t, cfg, "feed")
	assert.NotEqual(t, 0, code)
	assert.Empty(t, out, "an error status is not streamed as if it were the source")
	assert.Contains(t, errOut, "404")
}
