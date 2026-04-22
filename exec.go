package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"
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
// Returns the child's exit code on normal exit; 127 if the binary couldn't
// be located or the command was malformed; 1 on render errors or unexpected
// I/O failures. A nil *Cmd is a bug caught by validation — this function
// treats it as a render error.
func doExec(c *Cmd, data any) int {
	if !c.Defined() {
		fmt.Fprintln(execStderr, "error: command is empty")
		return 1
	}
	cmd, err := buildExecCmd(c, data)
	if err != nil {
		fmt.Fprintln(execStderr, "error:", err)
		return 1
	}
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
	argv := make([]string, len(c.Argv))
	for i, el := range c.Argv {
		rendered, err := renderString(el, data)
		if err != nil {
			return nil, fmt.Errorf("render argv[%d]: %w", i, err)
		}
		argv[i] = rendered
	}
	return exec.Command(argv[0], argv[1:]...), nil
}
