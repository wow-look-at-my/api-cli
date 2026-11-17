package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wow-look-at-my/tml"
)

// lockedBuffer takes the program's writes on its goroutine while the test reads them.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// startTMLProgram runs the watch program on a pipe for keys and a buffer for the screen.
func startTMLProgram(t *testing.T, body func() error) (*io.PipeWriter, *lockedBuffer, <-chan error) {
	t.Helper()
	reader, writer := io.Pipe()
	t.Cleanup(func() { _ = writer.Close() })
	out := &lockedBuffer{}
	done := make(chan error, 1)
	opts := tml.ProgramOptions{Input: reader, Output: out, Width: 40, Height: 10, NoMouse: true}
	go func() { done <- runTMLWith(10*time.Millisecond, body, opts) }()
	return writer, out, done
}

func waitTMLProgram(t *testing.T, done <-chan error) error {
	t.Helper()
	select {
	case err := <-done:
		return err
	case <-time.After(5 * time.Second):
		t.Fatal("the program did not end")
		return nil
	}
}

func TestTMLProgramRepeatsTheLeafUntilQuit(t *testing.T) {
	t.Serial()
	prev := exitCode
	t.Cleanup(func() { exitCode = prev })
	exitCode = 0

	var runs atomic.Int32
	var seen termSize
	keys, out, done := startTMLProgram(t, func() error {
		n := runs.Add(1)
		seen = *ttyOverride
		fmt.Fprintf(execStdout, "frame %d\n", n)
		return nil
	})

	require.Eventually(t, func() bool { return runs.Load() >= 3 }, 5*time.Second, 5*time.Millisecond, "each tick runs the leaf again")
	require.Eventually(t, func() bool { return strings.Contains(out.String(), "frame") }, 5*time.Second, 5*time.Millisecond, "the screen shows what the leaf printed")
	_, err := keys.Write([]byte("r"))
	require.NoError(t, err)
	_, err = keys.Write([]byte("q"))
	require.NoError(t, err)

	require.NoError(t, waitTMLProgram(t, done))
	assert.Equal(t, 0, exitCode, "q is a clean end")
	assert.Equal(t, termSize{width: 40, height: 10}, seen, "the leaf formats for the program's screen")
}

func TestTMLProgramCtrlCAndAFailedRun(t *testing.T) {
	t.Serial()
	prev := exitCode
	t.Cleanup(func() { exitCode = prev })
	exitCode = 0

	keys, out, done := startTMLProgram(t, func() error { return errors.New("boom") })
	require.Eventually(t, func() bool { return strings.Contains(out.String(), "boom") }, 5*time.Second, 5*time.Millisecond, "a failed run shows its error")
	_, err := keys.Write([]byte("\x03"))
	require.NoError(t, err)

	require.NoError(t, waitTMLProgram(t, done))
	assert.Equal(t, interruptExitCode, exitCode)
}
