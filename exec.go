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
// Returns the child's exit code on normal exit; 127 if the binary couldn't
// be located or the command was malformed; 1 on render errors or unexpected
// I/O failures. A nil *Cmd is a bug caught by validation — this function
// treats it as a render error.
func doExec(c *Cmd, cwd string, data any) int {
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
	cmd.Stdin = execStdin
	cmd.Stdout = execStdout
	cmd.Stderr = execStderr

	if err := cmd.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return exitErr.ExitCode()
		}
		fmt.Fprintln(execStderr, "error:", err)
		return 127
	}
	return 0
}

// captureExec is like doExec but captures the child's stdout and returns it
// as a string. stderr still flows to execStderr. Returns the captured output
// and the child's exit code (non-zero on failure).
func captureExec(c *Cmd, cwd string, data any) (string, int) {
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
	var buf bytes.Buffer
	cmd.Stdin = execStdin
	cmd.Stdout = &buf
	cmd.Stderr = execStderr

	if err := cmd.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return "", exitErr.ExitCode()
		}
		fmt.Fprintln(execStderr, "error:", err)
		return "", 127
	}
	return buf.String(), 0
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
		// `spread` output is recognised by a leading NUL; expand into
		// zero or more argv slots.
		if strings.HasPrefix(rendered, spreadSentinel) {
			rest := strings.TrimPrefix(rendered, spreadSentinel)
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
