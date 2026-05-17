package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
)

// execStdin / execStdout / execStderr are the I/O channels used when running
// the rendered command. Package-level so tests can redirect them.
var (
	execStdin  io.Reader = os.Stdin
	execStdout io.Writer = os.Stdout
	execStderr io.Writer = os.Stderr
)

// doExec renders the command template against data and executes it.
//
// cwd, if non-empty, is set as the child's working directory; the empty
// string means "use the calling process's cwd". cwd is taken as already
// rendered — callers are expected to template-evaluate cwd themselves so it
// can use any data context they choose.
//
// stdin, if non-empty, is fed to the child's standard input (and closed
// after). When empty, the child inherits the parent process's stdin.
//
// Returns the child's exit code on normal exit; 127 if the binary couldn't
// be located or the command was malformed; 1 on render errors or unexpected
// I/O failures. A nil *Cmd is a bug caught by validation — this function
// treats it as a render error.
func doExec(c *Cmd, cwd, stdin string, data any) int {
	if !c.Defined() {
		fmt.Fprintln(execStderr, "error: command is empty")
		return 1
	}
	cmd, err := buildExecCmd(c, data)
	if err != nil {
		fmt.Fprintln(execStderr, "error:", err)
		return 1
	}
	cmd.Dir = cwd
	logVerbose("exec: %s", cmdToString(cmd))
	logDebug("exec: cwd=%q stdin=%q", cwd, truncate(stdin, 200))
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	} else {
		cmd.Stdin = execStdin
	}
	cmd.Stdout = execStdout
	cmd.Stderr = execStderr

	if err := cmd.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			logVerbose("exec: exit code %d", exitErr.ExitCode())
			return exitErr.ExitCode()
		}
		fmt.Fprintln(execStderr, "error:", err)
		return 127
	}
	logVerbose("exec: exit code 0")
	return 0
}

// captureExec is like doExec but captures the child's stdout and returns it
// as a string. stderr still flows to execStderr. Returns the captured output
// and the child's exit code (non-zero on failure).
func captureExec(c *Cmd, cwd, stdin string, data any) (string, int) {
	if !c.Defined() {
		fmt.Fprintln(execStderr, "error: command is empty")
		return "", 1
	}
	cmd, err := buildExecCmd(c, data)
	if err != nil {
		fmt.Fprintln(execStderr, "error:", err)
		return "", 1
	}
	cmd.Dir = cwd
	logVerbose("capture: %s", cmdToString(cmd))
	logDebug("capture: cwd=%q stdin=%q", cwd, truncate(stdin, 200))
	var buf bytes.Buffer
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	} else {
		cmd.Stdin = execStdin
	}
	cmd.Stdout = &buf
	cmd.Stderr = execStderr

	if err := cmd.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			logVerbose("capture: exit code %d", exitErr.ExitCode())
			logDebugBlock("capture: stdout", buf.String())
			return "", exitErr.ExitCode()
		}
		fmt.Fprintln(execStderr, "error:", err)
		return "", 127
	}
	logVerbose("capture: exit code 0")
	logDebugBlock("capture: stdout", buf.String())
	return buf.String(), 0
}

// captureExecCapped is the format-path capture variant. It buffers the
// child's stdout up to maxBytes; if the child exceeds the cap, the buffered
// prefix is flushed to execStdout and the remainder streams through. The
// caller treats overflow == true as "skip formatting, output already streamed."
//
// Returns (capturedOrEmpty, overflowed, exitCode). When overflowed is true the
// returned string is empty — the caller MUST NOT format it; bytes are already
// on stdout.
func captureExecCapped(c *Cmd, cwd, stdin string, data any, maxBytes int) (string, bool, int) {
	if !c.Defined() {
		fmt.Fprintln(execStderr, "error: command is empty")
		return "", false, 1
	}
	cmd, err := buildExecCmd(c, data)
	if err != nil {
		fmt.Fprintln(execStderr, "error:", err)
		return "", false, 1
	}
	cmd.Dir = cwd
	logVerbose("capture-capped: %s", cmdToString(cmd))
	logDebug("capture-capped: cwd=%q stdin=%q max=%d", cwd, truncate(stdin, 200), maxBytes)
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	} else {
		cmd.Stdin = execStdin
	}
	tee := &cappedTee{buf: &bytes.Buffer{}, out: execStdout, max: maxBytes}
	cmd.Stdout = tee
	cmd.Stderr = execStderr

	if err := cmd.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			logVerbose("capture-capped: exit code %d overflowed=%v", exitErr.ExitCode(), tee.overflowed)
			if tee.overflowed {
				return "", true, exitErr.ExitCode()
			}
			return tee.buf.String(), false, exitErr.ExitCode()
		}
		fmt.Fprintln(execStderr, "error:", err)
		return "", false, 127
	}
	logVerbose("capture-capped: exit code 0 overflowed=%v", tee.overflowed)
	if tee.overflowed {
		return "", true, 0
	}
	return tee.buf.String(), false, 0
}

// cappedTee buffers writes up to max bytes. Once the cap would be exceeded by
// the next Write, it flushes the buffered prefix to out and switches to
// passthrough mode, where every subsequent Write goes straight to out. This
// gives the format path a "first 32MB or whatever fits" buffer with a
// transparent fallback to streaming for larger outputs.
type cappedTee struct {
	buf        *bytes.Buffer
	out        io.Writer
	max        int
	overflowed bool
}

func (c *cappedTee) Write(p []byte) (int, error) {
	if c.overflowed {
		return c.out.Write(p)
	}
	if c.buf.Len()+len(p) > c.max {
		c.overflowed = true
		if c.buf.Len() > 0 {
			if _, err := c.out.Write(c.buf.Bytes()); err != nil {
				return 0, err
			}
		}
		c.buf.Reset()
		return c.out.Write(p)
	}
	return c.buf.Write(p)
}

// parseResult tries to decode s as JSON. If s is valid JSON, the decoded value
// is returned (arrays and objects remain structured so templates can index
// into them). JSON numbers are normalized to int64 or float64 so that sprig
// arithmetic helpers (mul, add, etc.) work without extra casting. If s is not
// valid JSON, the trimmed raw string is returned.
func parseResult(s string) any {
	s = strings.TrimSpace(s)
	dec := json.NewDecoder(strings.NewReader(s))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return s
	}
	if dec.More() {
		return s
	}
	if strings.TrimSpace(s[dec.InputOffset():]) != "" {
		return s
	}
	return normalizeNumbers(v)
}

// normalizeNumbers walks a decoded JSON value and converts json.Number leaves
// to int64 (if the value fits) or float64. This makes numeric results
// compatible with Go template arithmetic functions without extra casts.
func normalizeNumbers(v any) any {
	switch x := v.(type) {
	case json.Number:
		if i, err := x.Int64(); err == nil {
			return i
		}
		if f, err := x.Float64(); err == nil {
			return f
		}
		return x.String()
	case map[string]any:
		for k, val := range x {
			x[k] = normalizeNumbers(val)
		}
		return x
	case []any:
		for i, val := range x {
			x[i] = normalizeNumbers(val)
		}
		return x
	default:
		return v
	}
}

func buildExecCmd(c *Cmd, data any) (*exec.Cmd, error) {
	if c.Shell {
		rendered, err := renderString(c.Template, data)
		if err != nil {
			return nil, fmt.Errorf("render command: %w", err)
		}
		rendered = expandSpreadForShell(rendered)
		return exec.Command("/bin/sh", "-c", rendered), nil
	}
	if len(c.Argv) == 0 {
		return nil, fmt.Errorf("argv command is empty")
	}
	argv := make([]string, 0, len(c.Argv))
	for i, el := range c.Argv {
		rendered, err := renderString(el, data)
		if err != nil {
			return nil, fmt.Errorf("render argv[%d]: %w", i, err)
		}
		if strings.HasPrefix(rendered, spreadSentinel) {
			rest := rendered[len(spreadSentinel):]
			rest = strings.TrimSuffix(rest, spreadEndSentinel)
			if rest == "" {
				continue
			}
			argv = append(argv, strings.Split(rest, spreadSentinel)...)
			continue
		}
		argv = append(argv, rendered)
	}
	if len(argv) == 0 {
		return nil, fmt.Errorf("argv command rendered to no arguments")
	}
	return exec.Command(argv[0], argv[1:]...), nil
}

// expandSpreadForShell replaces spread sentinel regions in a rendered shell
// command with shell-quoted elements. Each spread region is delimited by a
// leading NUL (spreadSentinel) and a trailing SOH (spreadEndSentinel); elements
// within the region are separated by NUL. Each element is individually
// shell-quoted so metacharacters like brackets, spaces, and quotes are
// preserved literally when passed to /bin/sh -c.
func expandSpreadForShell(s string) string {
	if strings.IndexByte(s, 0) < 0 {
		return s
	}
	var b strings.Builder
	for len(s) > 0 {
		startIdx := strings.IndexByte(s, 0)
		if startIdx < 0 {
			b.WriteString(s)
			break
		}
		b.WriteString(s[:startIdx])
		s = s[startIdx:]

		endIdx := strings.IndexByte(s, 1)
		if endIdx < 0 {
			b.WriteString(s)
			break
		}

		region := s[1:endIdx]
		s = s[endIdx+1:]

		if region == "" {
			continue
		}

		parts := strings.Split(region, spreadSentinel)
		for i, p := range parts {
			if i > 0 {
				b.WriteByte(' ')
			}
			b.WriteString(shellQuote(p))
		}
	}
	return b.String()
}
