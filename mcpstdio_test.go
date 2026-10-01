package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// rpcLines frames a JSON-RPC conversation the way a stdio client writes it: a
// single message per line, and nothing after the last newline.
func rpcLines(msgs ...string) string {
	return strings.Join(msgs, "\n") + "\n"
}

const (
	rpcInitialize  = `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"t","version":"1"}}}`
	rpcInitialized = `{"jsonrpc":"2.0","method":"notifications/initialized"}`
)

// serveStdio runs a single stdio session over the given request stream and
// returns the exit code and everything the server wrote.
func serveStdio(t *testing.T, cfg *Config, requests string) (int, string) {
	t.Helper()
	t.Serial()

	var out, errBuf bytes.Buffer
	prevIn, prevOut, prevErr := execStdin, execStdout, execStderr
	execStdin, execStdout, execStderr = strings.NewReader(requests), &out, &errBuf
	t.Cleanup(func() { execStdin, execStdout, execStderr = prevIn, prevOut, prevErr })

	code := runMCP("stdio", cfg, CorsStrict)
	return code, out.String()
}

// A client that writes its last request and closes stdin in the same breath
// still gets the answer.
func TestMCPStdio_AnswersTheLastCallWhenStdinClosesAtOnce(t *testing.T) {
	cfg, err := parseConfigXML([]byte(`<config name="t">
		<command name="ping" description="p"><run>printf pong</run></command>
	</config>`))
	require.NoError(t, err)
	require.NoError(t, validate(cfg))

	code, out := serveStdio(t, cfg, rpcLines(
		rpcInitialize,
		rpcInitialized,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"ping","arguments":{}}}`,
	))

	assert.Equal(t, 0, code)
	assert.Contains(t, out, `"id":1`, "the initialize response is missing")
	assert.Contains(t, out, `"id":2`, "the tools/call response is missing")
	assert.Contains(t, out, "pong")
}

// Several calls back to back land the same way, so the hold-back waits for the
// whole batch rather than for whichever answer happens to be earliest.
func TestMCPStdio_AnswersEveryCallWhenStdinClosesAtOnce(t *testing.T) {
	cfg, err := parseConfigXML([]byte(`<config name="t">
		<command name="ping" description="p"><run>printf pong</run></command>
	</config>`))
	require.NoError(t, err)
	require.NoError(t, validate(cfg))

	code, out := serveStdio(t, cfg, rpcLines(
		rpcInitialize,
		rpcInitialized,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"ping","arguments":{}}}`,
		`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"ping","arguments":{}}}`,
	))

	assert.Equal(t, 0, code)
	for _, id := range []string{`"id":1`, `"id":2`, `"id":3`, `"id":4`} {
		assert.Contains(t, out, id)
	}
}

// --- the ledger both halves share ---

func TestNdjsonLines_SplitsAndBuffersAPartialLine(t *testing.T) {
	var got []rpcMessage
	var s ndjsonLines
	collect := func(m rpcMessage) { got = append(got, m) }

	s.feed([]byte(`{"id":1,"method":"a"}`+"\n"+`{"id":2,"me`), collect)
	require.Len(t, got, 1)
	assert.Equal(t, "1", got[0].id())
	assert.True(t, got[0].isCall())

	s.feed([]byte(`thod":"b"}`+"\n"), collect)
	require.Len(t, got, 2)
	assert.Equal(t, "2", got[1].id())
}

func TestRPCMessages_ReadsABatchAndClassifies(t *testing.T) {
	msgs := rpcMessages([]byte(`[{"id":1,"method":"a"},{"id":1,"result":{}},{"method":"n"}]`))
	require.Len(t, msgs, 3)
	assert.True(t, msgs[0].isCall())
	assert.False(t, msgs[0].isResponse())
	assert.True(t, msgs[1].isResponse())
	assert.False(t, msgs[2].isCall(), "a notification carries no id, so nothing answers it")
	assert.False(t, msgs[2].isResponse())

	assert.Nil(t, rpcMessages([]byte("   ")))
	assert.Nil(t, rpcMessages([]byte("not json")))
}

func TestPendingCalls_ReleaseEndsTheWait(t *testing.T) {
	p := newPendingCalls()
	p.opened("1")
	go p.release()
	p.waitIdle() // returns because release broadcast, not because "1" was answered
}

func TestPendingCalls_WaitsUntilTheLastAnswer(t *testing.T) {
	p := newPendingCalls()
	p.opened("1")
	p.opened("2")
	go func() {
		p.answered("1")
		p.answered("2")
	}()
	p.waitIdle()
}
