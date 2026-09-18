package main

import (
	"bytes"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"

	"github.com/wow-look-at-my/go-containers/set"
)

// Stream is a leaf's chunked-streaming action. The leaf's effective <run>
// (shell, argv or request) supplies the bytes, this declaration cuts them into
// chunks at a boundary, and each chunk goes to stdout as soon as it is whole.
// Nothing upstream buffers the source, so the run works on a log, an audio
// feed, or a video stream, and the bytes are treated as bytes rather than as
// text.
//
// Boundaries are available. `mode="bytes"` (the default) emits chunks of
// exactly `chunk=` bytes, with the last chunk the remainder. `mode="lines"`
// emits a single chunk per newline-terminated line, of any length, and a final
// line with no newline as a chunk of its own.
type Stream struct {
	Mode  string `json:"mode,omitempty"`
	Chunk string `json:"chunk,omitempty"`
	// Step, when present, runs a single time per chunk: the chunk is its
	// stdin and its stdout replaces the chunk. A chunk therefore reaches
	// stdout transformed without the source ever being held in memory.
	Step *StreamStep `json:"step,omitempty"`
}

// StreamStep is the optional per-chunk step. The chunk arrives on stdin unless
// `stdin` renders something else, and the step sees where it sits in the run at
// `.stream.index` (1-based), `.stream.offset` (bytes before this chunk) and
// `.stream.size` (bytes in this chunk).
type StreamStep struct {
	Command *Cmd   `json:"command,omitempty"`
	Cwd     string `json:"cwd,omitempty"`
	Stdin   string `json:"stdin,omitempty"`
}

// The accepted <stream mode=> values. The empty string is the default,
// streamModeBytes.
const (
	streamModeBytes = "bytes"
	streamModeLines = "lines"
)

var streamModes = set.Of(streamModeBytes, streamModeLines)

// streamReadBlock is how much a line-mode chunker reads per call. A line longer
// than this still arrives whole: the chunker keeps reading until it reaches the
// newline that ends the line.
const streamReadBlock = 32 << 10

// chunkSizeUnits maps a <stream chunk=> suffix to its multiplier. Both the
// short and the long spelling are accepted, in any capitalization.
var chunkSizeUnits = map[string]int{
	"b":  1,
	"k":  1 << 10,
	"kb": 1 << 10,
	"m":  1 << 20,
	"mb": 1 << 20,
	"g":  1 << 30,
	"gb": 1 << 30,
}

// parseChunkSize reads a <stream chunk=> value: a plain byte count, or a count
// with a unit suffix (4kb, 64k, 4mb). A size that is absent, unparseable or
// empty is rejected, because a chunk boundary the author did not mean is a
// streaming bug that only shows up as missing or doubled bytes much later.
func parseChunkSize(raw string) (int, error) {
	s := strings.ToLower(strings.TrimSpace(raw))
	if s == "" {
		return 0, fmt.Errorf("chunk= needs a size such as 4kb, 64k or 4mb, or a plain byte count")
	}
	digits := 0
	for digits < len(s) && s[digits] >= '0' && s[digits] <= '9' {
		digits++
	}
	if digits == 0 {
		return 0, fmt.Errorf("chunk=%q must start with a number (4kb, 64k, 4mb, or plain bytes)", raw)
	}
	n, err := strconv.Atoi(s[:digits])
	if err != nil {
		return 0, fmt.Errorf("chunk=%q is not a usable number: %w", raw, err)
	}
	unit := strings.TrimSpace(s[digits:])
	mult, ok := chunkSizeUnits[unit]
	if !ok {
		return 0, fmt.Errorf("chunk=%q has an unknown unit %q; use b, k, kb, m, mb, g or gb, or a plain byte count", raw, unit)
	}
	if n > math.MaxInt/mult {
		return 0, fmt.Errorf("chunk=%q is larger than this machine can address", raw)
	}
	size := n * mult
	if size == 0 {
		return 0, fmt.Errorf("chunk=%q is zero bytes, which cannot cut a source into chunks", raw)
	}
	return size, nil
}

func (s *Stream) step() *StreamStep {
	if s.Step == nil {
		s.Step = &StreamStep{}
	}
	return s.Step
}

// buildStream parses a <stream> declaration.
func buildStream(n *xnode) (*Stream, error) {
	if err := checkAttrs(n, "mode", "chunk"); err != nil {
		return nil, err
	}
	st := &Stream{Mode: strings.TrimSpace(n.Attr("mode")), Chunk: strings.TrimSpace(n.Attr("chunk"))}
	if st.Mode == "" {
		st.Mode = streamModeBytes
	}
	for _, child := range n.Children() {
		switch child.Name() {
		case "run":
			cmd, req, err := buildRun(child)
			if err != nil {
				return nil, err
			}
			if req != nil {
				return nil, fmt.Errorf("<stream><run>: the per-chunk step is a command, because one chunk arrives on its stdin and a request has no way to read that")
			}
			if st.Step != nil && st.Step.Command.Defined() {
				return nil, fmt.Errorf("<stream><run>: declared twice")
			}
			st.step().Command = cmd
		case "cwd":
			s, err := compileTextElem(child)
			if err != nil {
				return nil, err
			}
			st.step().Cwd = s
		case "stdin":
			s, err := compileTextElem(child)
			if err != nil {
				return nil, err
			}
			st.step().Stdin = s
		default:
			return nil, fmt.Errorf("<stream>: unexpected child element <%s>", child.Name())
		}
	}
	return st, nil
}

// validateStream checks a <stream> declaration at load time: the boundary it
// names, that the leaf has a source to read, that the per-chunk step is a
// command, and that the source is a single this can read incrementally.
func validateStream(st *Stream, where string, haveRun bool, request *Request, transports map[string]*Transport) error {
	if st == nil {
		return nil
	}
	if !streamModes.Contains(st.Mode) {
		return fmt.Errorf("%s: mode=%q must be one of bytes|lines", where, st.Mode)
	}
	if st.Mode == streamModeLines {
		if st.Chunk != "" {
			return fmt.Errorf("%s: chunk= names a size and applies to mode=bytes; mode=lines cuts at each newline, so remove chunk=", where)
		}
	} else if _, err := parseChunkSize(st.Chunk); err != nil {
		return fmt.Errorf("%s: %w", where, err)
	}

	// Unlike a <download>, which carries its own URL, a <stream> has no source
	// of its own: the bytes are the leaf's run, so a single must exist.
	if !haveRun {
		return fmt.Errorf("%s: <stream> is the leaf's output shape, so it needs a <run> (its own or an ancestor's) to supply the bytes", where)
	}
	if st.Step != nil && !st.Step.Command.Defined() {
		return fmt.Errorf("%s: the per-chunk step needs a <run> command", where)
	}
	if !request.Defined() {
		return nil
	}
	if request.Response != nil {
		return fmt.Errorf("%s: <stream> needs the response body as it arrives, and <response jq=> shapes a whole body at once; remove <response> from this request", where)
	}
	if name := streamTransportName(request, transports); name != "" && name != builtinTransportName {
		return fmt.Errorf("%s: <stream> reads the response as it arrives, and transport %q buffers the program's stdout; write transport=%q on this request, or drop <stream>", where, name, builtinTransportName)
	}
	return nil
}

// streamTransportName reports which transport a request would travel over,
// without consulting the published registry: validate runs before the registry
// is installed, so it reads the config's own entries.
func streamTransportName(req *Request, transports map[string]*Transport) string {
	if name := strings.TrimSpace(req.Transport); name != "" {
		return name
	}
	for name, t := range transports {
		if t != nil && t.Default {
			return name
		}
	}
	return ""
}

// chunker yields a single chunk at a time from a source. It holds a single
// chunk plus a single read block, so peak memory follows the chunk size rather
// than the size of the source: a stream that never ends is fine.
type chunker struct {
	src  io.Reader
	mode string
	size int
	buf  []byte
	eof  bool
}

// newChunker builds the chunker for a mode. size is ignored in line mode.
func newChunker(src io.Reader, mode string, size int) *chunker {
	return &chunker{src: src, mode: mode, size: size}
}

// next returns the next chunk, or io.EOF a single time the source is
// exhausted. The returned slice belongs to the caller.
func (c *chunker) next() ([]byte, error) {
	if c.mode == streamModeLines {
		return c.nextLine()
	}
	return c.nextFixed()
}

// nextFixed gathers exactly size bytes, and hands the last, shorter chunk over
// at end of source. It reads through io.ReadFull so a chunk boundary is never
// decided by how the source happened to split its writes.
func (c *chunker) nextFixed() ([]byte, error) {
	for len(c.buf) < c.size && !c.eof {
		start := len(c.buf)
		c.buf = append(c.buf, make([]byte, c.size-start)...)
		n, err := io.ReadFull(c.src, c.buf[start:])
		c.buf = c.buf[:start+n]
		switch err {
		case nil:
		case io.EOF, io.ErrUnexpectedEOF:
			c.eof = true
		default:
			return nil, err
		}
	}
	if len(c.buf) == 0 {
		return nil, io.EOF
	}
	chunk := c.buf
	c.buf = nil
	return chunk, nil
}

// nextLine returns a single newline-terminated line, newline included. A line
// longer than the read block arrives whole, because the chunker keeps reading
// until it reaches the newline. A final line with no newline is still a chunk.
//
// Each read is a single call rather than a fill of the buffer: a line source
// that pauses mid-stream emits what it has, and a chunker that waited for a
// full block would sit on it.
func (c *chunker) nextLine() ([]byte, error) {
	for {
		if i := bytes.IndexByte(c.buf, '\n'); i >= 0 {
			chunk := make([]byte, i+1)
			copy(chunk, c.buf[:i+1])
			c.buf = append(c.buf[:0], c.buf[i+1:]...)
			return chunk, nil
		}
		if c.eof {
			if len(c.buf) == 0 {
				return nil, io.EOF
			}
			chunk := make([]byte, len(c.buf))
			copy(chunk, c.buf)
			c.buf = nil
			return chunk, nil
		}
		if err := c.readLine(); err != nil {
			return nil, err
		}
	}
}

// readLine appends whatever the source hands over in a single read.
func (c *chunker) readLine() error {
	start := len(c.buf)
	c.buf = append(c.buf, make([]byte, streamReadBlock)...)
	n, err := c.src.Read(c.buf[start:])
	c.buf = c.buf[:start+n]
	switch err {
	case nil, io.EOF:
		if err == io.EOF {
			c.eof = true
		}
		return nil
	default:
		return err
	}
}
