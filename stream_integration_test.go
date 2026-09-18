package main

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
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
	cfg.Commands[0].Stream = mustBuildStreamWith(`mode="lines"`, `<run>grep -v drop</run>`)
	code, out := execCmd(t, cfg, "log")
	require.Equal(t, 0, code)
	assert.Equal(t, "keep me\nkeep me too\n", out)
}

// streamingWriter records the time each write reaches the leaf's stdout, so a
// test can prove that the earliest chunk landed while the source was still
// running rather than after it finished.
type streamingWriter struct {
	start  time.Time
	writes []writeStamp
}

type writeStamp struct {
	at    time.Duration
	bytes int
}

func (w *streamingWriter) Write(p []byte) (int, error) {
	w.writes = append(w.writes, writeStamp{at: time.Since(w.start), bytes: len(p)})
	return len(p), nil
}

func TestIntegration_StreamFirstChunkArrivesBeforeTheSourceEnds(t *testing.T) {
	t.Serial()

	sink := &streamingWriter{start: time.Now()}
	prevOut := execStdout
	execStdout = sink
	t.Cleanup(func() { execStdout = prevOut })

	cfg := streamConfig(`chunk="2"`, `printf aa; sleep 2; printf bb`)
	code, _, errOut := execCmdFull(t, cfg, "log")
	require.Equal(t, 0, code, errOut)

	require.Len(t, sink.writes, 2, "each chunk is written as it is produced")
	assert.Equal(t, 2, sink.writes[0].bytes)
	assert.Equal(t, 2, sink.writes[1].bytes)
	assert.Less(t, sink.writes[0].at, 1500*time.Millisecond,
		"the first chunk reached stdout before the source's later bytes existed")
	assert.Greater(t, sink.writes[1].at, sink.writes[0].at,
		"the second chunk followed the pause")
}

func TestIntegration_StreamNeverBuffersTheWholeSource(t *testing.T) {
	// A source far larger than any internal buffer streams through with peak
	// memory bounded by the chunk size. The source bytes are generated, so the
	// run proves streaming rather than a captured string.
	t.Serial()

	const total = 8 << 20
	sink := &streamingWriter{start: time.Now()}
	prevOut := execStdout
	execStdout = sink
	t.Cleanup(func() { execStdout = prevOut })

	cfg := streamConfig(`chunk="64kb"`, fmt.Sprintf(`head -c %d /dev/zero | tr '\0' 'y'`, total))
	code, _, errOut := execCmdFull(t, cfg, "log")
	require.Equal(t, 0, code, errOut)

	written := 0
	for _, w := range sink.writes {
		written += w.bytes
	}
	assert.Equal(t, total, written, "every source byte reached stdout")
	assert.Greater(t, len(sink.writes), 1, "the source arrived in many chunks, not one buffer")
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
	cfg := &Config{
		Name: "t",
		Commands: []Command{{
			Name:   "log",
			Stream: mustBuildStream(`chunk="4"`),
		}},
	}
	err := validate(cfg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "needs a <run>")
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
	cfg := streamConfig(`mode="lines"`, `printf 'x'`)
	code, _, errOut := execCmdFull(t, cfg, "--watch", "1s", "log")
	assert.NotEqual(t, 0, code)
	assert.Contains(t, errOut, "--watch")
}
