package main

import (
	"fmt"
	"io"
	"os/exec"
	"strings"
)

// streamSource is the leaf's run, opened as a byte stream rather than executed
// to completion: the chunker pulls from it, so the source is never captured.
type streamSource struct {
	cmd  *exec.Cmd
	pipe io.ReadCloser
	body io.ReadCloser
}

// reader returns the source's reader. For a command that is its stdout, and for
// a request the response body.
func (s *streamSource) reader() io.Reader {
	if s.pipe != nil {
		return s.pipe
	}
	return s.body
}

// openStreamSource starts a leaf's effective run as an incremental source.
//
// A command's stdout is a pipe the child writes into as it goes, and a request
// body is read from the socket as it arrives. Either way nothing between the
// source and the chunker accumulates the whole output.
func openStreamSource(cmdTmpl *Cmd, request *Request, cwd, stdin string, data map[string]any, errOut io.Writer) (*streamSource, error) {
	if request.Defined() {
		prepared, err := prepareRequest(request, data)
		if err != nil {
			return nil, err
		}
		body, code := doHTTPStream(prepared, errOut)
		if code != 0 {
			return nil, &streamExit{code: code}
		}
		return &streamSource{body: body}, nil
	}

	if !cmdTmpl.Defined() {
		return nil, fmt.Errorf("no command or request available to stream")
	}
	argv, err := resolveArgv(cmdTmpl, data)
	if err != nil {
		return nil, fmt.Errorf("render command: %w", err)
	}
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Dir = cwd
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	} else {
		cmd.Stdin = execStdin
	}
	cmd.Stderr = errOut
	pipe, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("stream: open the source's stdout: %w", err)
	}
	logVerbose("stream: source: %s", cmdToString(cmd))
	if err := cmd.Start(); err != nil {
		fmt.Fprintf(errOut, "error: %v\n", err)
		return nil, &streamExit{code: 127}
	}
	return &streamSource{cmd: cmd, pipe: pipe}, nil
}

// close releases the source. It reports the source's exit code a single time
// the command has finished, which is how a failing producer fails the leaf.
func (s *streamSource) close() (int, error) {
	if s.body != nil {
		return 0, s.body.Close()
	}
	if s.cmd == nil || s.cmd.Process == nil {
		return 0, nil
	}
	if err := s.cmd.Wait(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return exitErr.ExitCode(), nil
		}
		return 127, nil
	}
	return 0, nil
}

// streamExit reports a source that failed before any chunk was read, so the
// caller can exit with the source's own code.
type streamExit struct{ code int }

func (e *streamExit) Error() string { return fmt.Sprintf("source exited %d", e.code) }

// abandon stops a source the run has finished with early. A chunk whose step
// failed ends the leaf while the producer is still writing into a pipe nobody
// reads, and waiting on that producer would hang on a full pipe.
func (s *streamSource) abandon() {
	if s.body != nil {
		s.body.Close()
		return
	}
	if s.cmd == nil || s.cmd.Process == nil {
		return
	}
	s.cmd.Process.Kill()
	s.cmd.Wait()
}

// runStream runs a leaf's <stream> declaration: the source is the leaf's own
// run, the chunker cuts it, the optional step transforms a single chunk at a
// time, and each finished chunk goes to stdout.
//
// Chunks are written as they are produced, so a source that pauses after its
// earliest chunk has already put that chunk in front of the user. Nothing
// here holds more than a single chunk, and the write adds no bytes: the
// concatenated chunks are the source, byte for byte.
func runStream(st *Stream, cmdTmpl *Cmd, request *Request, cwd, stdin string, data map[string]any) int {
	size := 0
	if st.Mode == streamModeBytes {
		parsed, err := parseChunkSize(st.Chunk)
		if err != nil {
			// validate() rejects an unusable size at load time, so this is a
			// config that bypassed it.
			fmt.Fprintln(execStderr, "error:", err)
			return 1
		}
		size = parsed
	}

	src, err := openStreamSource(cmdTmpl, request, cwd, stdin, data, execStderr)
	if err != nil {
		if exit, ok := err.(*streamExit); ok {
			return exit.code
		}
		fmt.Fprintln(execStderr, "error:", err)
		return 1
	}

	chunks := newChunker(src.reader(), st.Mode, size)
	code := pumpChunks(chunks, st, data)
	if code != 0 {
		// The leaf stops here, so the source is stopped with it rather than
		// left writing into a pipe with no reader.
		src.abandon()
		return code
	}
	srcCode, closeErr := src.close()
	if closeErr != nil {
		fmt.Fprintln(execStderr, "error:", closeErr)
		return 1
	}
	// The source's own exit code is the leaf's, exactly as it is on the
	// unstreamed path.
	return srcCode
}

// pumpChunks drives the chunker to exhaustion, transforming each chunk and
// writing the result.
func pumpChunks(chunks *chunker, st *Stream, data map[string]any) int {
	index, offset := 0, 0
	for {
		chunk, err := chunks.next()
		if err == io.EOF {
			return 0
		}
		if err != nil {
			fmt.Fprintln(execStderr, "error: reading the source:", err)
			return 1
		}
		index++

		out := chunk
		if st.Step != nil {
			out, err = applyStreamStep(st.Step, chunk, index, offset, data)
			if err != nil {
				fmt.Fprintln(execStderr, "error:", err)
				return 1
			}
		}
		if _, err := execStdout.Write(out); err != nil {
			fmt.Fprintln(execStderr, "error: writing a chunk:", err)
			return 1
		}
		logDebug("stream: chunk %d: %d byte(s) in, %d byte(s) out", index, len(chunk), len(out))
		offset += len(chunk)
	}
}

// applyStreamStep runs the per-chunk step on a single chunk. The chunk arrives
// on the step's stdin and the step's stdout replaces it, so a chunk is
// transformed without the source being held in memory.
//
func applyStreamStep(step *StreamStep, chunk []byte, index, offset int, data map[string]any) ([]byte, error) {
	ctx := streamCtx(data, index, offset, len(chunk))

	stdin := string(chunk)
	if step.Stdin != "" {
		rendered, err := renderString(step.Stdin, ctx)
		if err != nil {
			return nil, fmt.Errorf("stream step: render stdin: %w", err)
		}
		stdin = rendered
	}

	cwd, err := renderCwd(step.Cwd, ctx)
	if err != nil {
		return nil, fmt.Errorf("stream step: render cwd: %w", err)
	}

	// The step's own command sees where the chunk sits, so a step can name its
	// position as well as transform the bytes.
	argv, err := resolveArgv(step.Command, ctx)
	if err != nil {
		return nil, fmt.Errorf("stream step: %w", err)
	}
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Dir = cwd
	cmd.Stdin = strings.NewReader(stdin)
	cmd.Stderr = execStderr

	out, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return nil, fmt.Errorf("stream step: chunk %d (bytes %d-%d): command exited %d",
				index, offset, offset+len(chunk), exitErr.ExitCode())
		}
		return nil, fmt.Errorf("stream step: chunk %d (bytes %d-%d): %w", index, offset, offset+len(chunk), err)
	}
	return out, nil
}

// streamCtx layers where a chunk sits in the run onto the leaf context, so a
// per-chunk step can name its position: `.stream.index`, `.stream.offset` and
// `.stream.size`.
func streamCtx(data map[string]any, index, offset, size int) map[string]any {
	ctx := make(map[string]any, len(data)+1)
	for k, v := range data {
		ctx[k] = v
	}
	ctx["stream"] = map[string]any{"index": index, "offset": offset, "size": size}
	return ctx
}
