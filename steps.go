package main

import (
	"fmt"
	"io"

	"github.com/wow-look-at-my/api-cli/fields"
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

		if step.Over != "" {
			done, err := runStepOver(step, data, results, stepCmd, stepReq, cwdTmpl, stdinTmpl, capture, errOut, &oc)
			if err != nil || !done {
				return oc, err
			}
			continue
		}

		out, code, err := runStepOnce(step, data, stepCmd, stepReq, cwdTmpl, stdinTmpl, capture, errOut)
		if err != nil {
			return oc, err
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

// runStepOnce renders a step's entry and runs it one time.
func runStepOnce(step Step, data map[string]any, stepCmd *Cmd, stepReq *Request, cwdTmpl, stdinTmpl string, capture stepCapture, errOut io.Writer) (string, int, error) {
	stepEntry, err := renderEntry(step.Entry, data)
	if err != nil {
		return "", 0, fmt.Errorf("step %q: render entry: %w", step.Name, err)
	}
	if stepEntry == nil {
		stepEntry = map[string]any{}
	}
	data["entry"] = stepEntry
	logDebug("step %q: entry: %s", step.Name, jsonCompact(stepEntry))

	if stepReq.Defined() {
		logVerbose("step %q: requesting", step.Name)
		out, code := runRequest(stepReq, data, errOut)
		return out, code, nil
	}

	stepCwdTmpl := cwdTmpl
	if step.Cwd != "" {
		stepCwdTmpl = step.Cwd
	}
	stepCwd, err := renderCwd(stepCwdTmpl, data)
	if err != nil {
		return "", 0, fmt.Errorf("step %q: render cwd: %w", step.Name, err)
	}

	stepStdinTmpl := stdinTmpl
	if step.Stdin != "" {
		stepStdinTmpl = step.Stdin
	}
	stepStdin, err := renderStdin(stepStdinTmpl, data)
	if err != nil {
		return "", 0, fmt.Errorf("step %q: render stdin: %w", step.Name, err)
	}

	logVerbose("step %q: executing", step.Name)
	out, code := capture(stepCmd, stepCwd, stepStdin, data)
	return out, code, nil
}

// runStepOver repeats a step once per element of the list at step.Over. The
// element rides in the data context as `.item`, so the step's own entry names
// the part of it that says what to fetch.
//
// The result pairs each element with its own response. A screen that draws one
// card per build then walks ONE list, rather than reaching across two by
// position, which is the shape a data template can actually take.
//
// A failing element fails the whole step: half a screen of builds, with the
// rest silently missing, reads as a shorter queue rather than as a broken run.
func runStepOver(step Step, data map[string]any, results map[string]any, stepCmd *Cmd, stepReq *Request, cwdTmpl, stdinTmpl string, capture stepCapture, errOut io.Writer, oc *stepOutcome) (bool, error) {
	found, ok := fields.Lookup(data, step.Over)
	if !ok {
		return false, fmt.Errorf("step %q: over %q is not in the context", step.Name, step.Over)
	}
	list, ok := found.([]any)
	if !ok {
		return false, fmt.Errorf("step %q: over %q is %T, and a repeated step needs a list", step.Name, step.Over, found)
	}
	logVerbose("step %q: repeating over %d element(s) of %q", step.Name, len(list), step.Over)

	prevItem, hadItem := data["item"]
	prevIndex, hadIndex := data["index"]
	defer func() {
		restore(data, "item", prevItem, hadItem)
		restore(data, "index", prevIndex, hadIndex)
	}()

	collected := make([]any, 0, len(list))
	for i, element := range list {
		data["item"], data["index"] = element, i
		out, code, err := runStepOnce(step, data, stepCmd, stepReq, cwdTmpl, stdinTmpl, capture, errOut)
		if err != nil {
			return false, err
		}
		oc.executions++
		logDebugBlock(fmt.Sprintf("step %q[%d]: stdout", step.Name, i), out)
		if code != 0 {
			oc.output, oc.code = out, code
			return false, nil
		}
		collected = append(collected, map[string]any{"item": element, "result": parseResult(out)})
	}
	results[step.Name] = collected
	return true, nil
}

// restore puts a context key back the way the step found it.
func restore(data map[string]any, key string, value any, had bool) {
	if had {
		data[key] = value
		return
	}
	delete(data, key)
}
