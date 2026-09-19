package main

// MCP over stdio. A client that closes stdin right after its last request still
// wants that request's answer, so this file holds the EOF back until every
// request it read has a response on the way out.
//
// The reason it has to: the jsonrpc2 layer under the SDK refuses every write as
// soon as the reader reports EOF. A client that writes its calls and closes in
// the same breath therefore loses the answers, and the server reports "server is
// closing: EOF" and exits 1.

import (
	"bytes"
	"encoding/json"
	"io"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// stdioTransport wires both halves of the stdio stream to a single pending-call
// ledger. The reader fills it, the writer empties it, and the reader waits on it
// at EOF.
func stdioTransport(in io.Reader, out io.Writer) *mcp.IOTransport {
	p := newPendingCalls()
	return &mcp.IOTransport{
		Reader: &lingerReader{src: in, pending: p},
		Writer: &answerWriter{dst: out, pending: p},
	}
}

// pendingCalls tracks the request ids that arrived without an answer yet.
type pendingCalls struct {
	mu     sync.Mutex
	idle   *sync.Cond
	open   map[string]bool
	closed bool
}

func newPendingCalls() *pendingCalls {
	p := &pendingCalls{open: map[string]bool{}}
	p.idle = sync.NewCond(&p.mu)
	return p
}

func (p *pendingCalls) opened(id string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.open[id] = true
}

func (p *pendingCalls) answered(id string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.open, id)
	if len(p.open) == 0 {
		p.idle.Broadcast()
	}
}

// waitIdle blocks until nothing is outstanding. A handler that never returns
// holds the session open exactly as it did before, because the session already
// waited for its own handlers to drain.
func (p *pendingCalls) waitIdle() {
	p.mu.Lock()
	defer p.mu.Unlock()
	for len(p.open) > 0 && !p.closed {
		p.idle.Wait()
	}
}

// release stops the wait. Closing the transport is a decision to stop reading,
// so an answer that has not been written by then is not going to be.
func (p *pendingCalls) release() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.closed = true
	p.idle.Broadcast()
}

// lingerReader passes stdin through and notes every call it carries. At EOF it
// waits for the answers.
type lingerReader struct {
	src     io.Reader
	pending *pendingCalls
	lines   ndjsonLines
}

func (r *lingerReader) Read(p []byte) (int, error) {
	n, err := r.src.Read(p)
	if n > 0 {
		r.lines.feed(p[:n], func(m rpcMessage) {
			if m.isCall() {
				r.pending.opened(m.id())
			}
		})
	}
	if err == io.EOF {
		r.pending.waitIdle()
	}
	return n, err
}

func (r *lingerReader) Close() error {
	r.pending.release()
	if c, ok := r.src.(io.Closer); ok {
		return c.Close()
	}
	return nil
}

// answerWriter passes stdout through and notes every response it carries.
type answerWriter struct {
	dst     io.Writer
	pending *pendingCalls
	lines   ndjsonLines
}

func (w *answerWriter) Write(p []byte) (int, error) {
	n, err := w.dst.Write(p)
	if n > 0 {
		w.lines.feed(p[:n], func(m rpcMessage) {
			if m.isResponse() {
				w.pending.answered(m.id())
			}
		})
	}
	return n, err
}

// Close leaves the underlying stream alone, the way the SDK's own stdio
// transport leaves stdout alone.
func (w *answerWriter) Close() error {
	w.pending.release()
	return nil
}

// ndjsonLines splits a byte stream into the newline-delimited messages the
// protocol is made of. A raw newline cannot appear inside an encoded JSON value,
// so a line is a single whole message.
type ndjsonLines struct {
	buf []byte
}

func (s *ndjsonLines) feed(p []byte, fn func(rpcMessage)) {
	s.buf = append(s.buf, p...)
	for {
		i := bytes.IndexByte(s.buf, '\n')
		if i < 0 {
			return
		}
		line := s.buf[:i]
		s.buf = s.buf[i+1:]
		for _, m := range rpcMessages(line) {
			fn(m)
		}
	}
}

// rpcMessage is the part of a JSON-RPC message that decides what it is. An id
// beside a method is a call. An id with no method is that call's answer.
type rpcMessage struct {
	ID     json.RawMessage `json:"id"`
	Method string          `json:"method"`
}

func (m rpcMessage) id() string       { return string(bytes.TrimSpace(m.ID)) }
func (m rpcMessage) isCall() bool     { return len(m.ID) > 0 && m.Method != "" }
func (m rpcMessage) isResponse() bool { return len(m.ID) > 0 && m.Method == "" }

// rpcMessages reads a single line as a message or as a batch of them. A line
// this cannot parse is left to the SDK, which reports it.
func rpcMessages(line []byte) []rpcMessage {
	line = bytes.TrimSpace(line)
	if len(line) == 0 {
		return nil
	}
	if line[0] == '[' {
		var batch []rpcMessage
		if json.Unmarshal(line, &batch) != nil {
			return nil
		}
		return batch
	}
	var one rpcMessage
	if json.Unmarshal(line, &one) != nil {
		return nil
	}
	return []rpcMessage{one}
}
