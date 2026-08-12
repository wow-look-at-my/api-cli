package main

import (
	"fmt"
	"io"
)

// stepCapture runs one step's command and returns its stdout and exit code.
// The CLI and MCP paths differ in where stderr goes and whether the child may
// inherit the process's stdin, so each supplies its own.
type stepCapture func(c *Cmd, cwd, stdin string, data any) (string, int)

// stepOutcome reports how a run of steps ended. output carries the failing
// step's stdout (empty on success) for callers that report it to the user.
type stepOutcome struct {
	executions int
	output     string
	code       int
}

// runSteps executes a leaf's steps in order, storing each step's parsed output
// in results under the step's name. Steps see prior results through data, so a
// later step's entry, url, or command can read `.result.<name>`.
//
// A step runs whichever of command/request it declares; declaring neither
// inherits the leaf's effective run, which is how a step reuses the ancestor
// <request> with nothing but a different <entry>. cwdTmpl/stdinTmpl are the
// leaf's, likewise overridable per step.
//
// A non-zero step aborts the run: the outcome carries that step's code, and
// the leaf's own run never happens.
func runSteps(steps []Step, data map[string]any, results map[string]any, cmdTmpl *Cmd, request *Request, cwdTmpl, stdinTmpl string, capture stepCapture, errOut io.Writer) (stepOutcome, error) {
	var oc stepOutcome
	for _, step := range steps {
		if step.When != "" {
			whenOut, err := renderString(step.When, data)
			if err != nil {
				return oc, fmt.Errorf("step %q: render when: %w", step.Name, err)
			}
			logVerbose("step %q: when %q => %q (truthy=%v)", step.Name, step.When, whenOut, isTruthy(whenOut))
			if !isTruthy(whenOut) {
				logVerbose("step %q: skipped", step.Name)
				continue
			}
		}

		stepCmd, stepReq := cmdTmpl, request
		switch {
		case step.Request.Defined():
			stepCmd, stepReq = nil, step.Request
		case step.Command.Defined():
			stepCmd, stepReq = step.Command, nil
		}
		if !stepCmd.Defined() && !stepReq.Defined() {
			return oc, fmt.Errorf("step %q: no command or request available", step.Name)
		}

		stepEntry, err := renderEntry(step.Entry, data)
		if err != nil {
			return oc, fmt.Errorf("step %q: render entry: %w", step.Name, err)
		}
		if stepEntry == nil {
			stepEntry = map[string]any{}
		}
		data["entry"] = stepEntry
		logDebug("step %q: entry: %s", step.Name, jsonCompact(stepEntry))

		var out string
		var code int
		if stepReq.Defined() {
			logVerbose("step %q: requesting", step.Name)
			out, code = runRequest(stepReq, data, errOut)
		} else {
			stepCwdTmpl := cwdTmpl
			if step.Cwd != "" {
				stepCwdTmpl = step.Cwd
			}
			stepCwd, err := renderCwd(stepCwdTmpl, data)
			if err != nil {
				return oc, fmt.Errorf("step %q: render cwd: %w", step.Name, err)
			}

			stepStdinTmpl := stdinTmpl
			if step.Stdin != "" {
				stepStdinTmpl = step.Stdin
			}
			stepStdin, err := renderStdin(stepStdinTmpl, data)
			if err != nil {
				return oc, fmt.Errorf("step %q: render stdin: %w", step.Name, err)
			}

			logVerbose("step %q: executing", step.Name)
			out, code = capture(stepCmd, stepCwd, stepStdin, data)
		}

		oc.executions++
		logVerbose("step %q: exit code %d", step.Name, code)
		logDebugBlock(fmt.Sprintf("step %q: stdout", step.Name), out)
		if code != 0 {
			oc.output, oc.code = out, code
			return oc, nil
		}
		results[step.Name] = parseResult(out)
	}
	return oc, nil
}
