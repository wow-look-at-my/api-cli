package main

import (
	"fmt"
	"io"
	"time"
)

// Defaults for a polling step. One second between attempts, for a minute: an
// async job that answers in that window needs no attributes of its own.
const (
	defaultPollInterval = time.Second
	defaultPollAttempts = 60
)

// pollSleep waits between two attempts of a polling step. A var so a test of
// the loop costs no real seconds.
var pollSleep = time.Sleep

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

		out, code, err := runStepAction(step, data, stepCmd, stepReq, cwdTmpl, stdinTmpl, capture, errOut, &oc)
		if err != nil {
			return oc, err
		}

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

// runStepAction performs one step's action and counts it. Without `until=` that
// is a single run. With it, the step repeats until the predicate holds: an
// async job that answers "pending" is polled here rather than in a shell loop
// around the whole program.
//
// A non-zero exit ends the poll immediately. A job that reports a failure is an
// answer, and asking the same endpoint 59 more times cannot change it.
func runStepAction(step Step, data map[string]any, stepCmd *Cmd, stepReq *Request, cwdTmpl, stdinTmpl string, capture stepCapture, errOut io.Writer, oc *stepOutcome) (string, int, error) {
	if step.Until == "" {
		out, code, err := runStepOnce(step, data, stepCmd, stepReq, cwdTmpl, stdinTmpl, capture, errOut)
		if err == nil {
			oc.executions++
		}
		return out, code, err
	}

	interval, err := pollInterval(step)
	if err != nil {
		return "", 0, fmt.Errorf("step %q: %w", step.Name, err)
	}
	attempts := step.Attempts
	if attempts <= 0 {
		attempts = defaultPollAttempts
	}

	var last any
	for attempt := 1; attempt <= attempts; attempt++ {
		out, code, err := runStepOnce(step, data, stepCmd, stepReq, cwdTmpl, stdinTmpl, capture, errOut)
		if err != nil {
			return "", 0, err
		}
		oc.executions++
		if code != 0 {
			return out, code, nil
		}
		last = parseResult(out)
		verdict, err := renderString(step.Until, pollContext(data, last))
		if err != nil {
			return "", 0, fmt.Errorf("step %q: render until: %w", step.Name, err)
		}
		logVerbose("step %q: attempt %d/%d: until %q => %q", step.Name, attempt, attempts, step.Until, verdict)
		if isTruthy(verdict) {
			return out, code, nil
		}
		if attempt < attempts {
			pollSleep(interval)
		}
	}
	return "", 0, fmt.Errorf("step %q: until %q did not hold in %d attempt(s); last response: %s",
		step.Name, step.Until, attempts, jsonCompact(last))
}

// pollContext is what `until=` is evaluated against: the run's own context with
// the last response promoted onto it, so an async job's status field reads as
// `.status`. The whole body stays reachable as `.body`.
func pollContext(data map[string]any, body any) map[string]any {
	return promoteCtx(data, body, "body")
}

// pollInterval reads a step's interval=, defaulting to one second.
func pollInterval(s Step) (time.Duration, error) {
	if s.Interval == "" {
		return defaultPollInterval, nil
	}
	d, err := time.ParseDuration(s.Interval)
	if err != nil {
		return 0, fmt.Errorf("interval=%q must be a duration such as 500ms or 2s", s.Interval)
	}
	if d < 0 {
		return 0, fmt.Errorf("interval=%q must not be negative", s.Interval)
	}
	return d, nil
}

// validatePoll checks a step's polling attributes at load time, so a bad
// duration is a config error rather than a surprise on the first poll.
func validatePoll(s Step, where string) error {
	if s.Until == "" {
		if s.Interval != "" || s.Attempts != 0 {
			return fmt.Errorf("%s: interval= and attempts= describe a poll, so they need until=", where)
		}
		return nil
	}
	if s.Attempts < 0 {
		return fmt.Errorf("%s: attempts=%d must be >= 0", where, s.Attempts)
	}
	if _, err := pollInterval(s); err != nil {
		return fmt.Errorf("%s: %w", where, err)
	}
	return nil
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
	found, ok, err := overSource(data, step.Over)
	if err != nil {
		return false, fmt.Errorf("step %q: %w", step.Name, err)
	}
	if !ok {
		return false, fmt.Errorf("step %q: over %q is not in the context", step.Name, step.Over)
	}
	list, ok := asList(found)
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
		out, code, err := runStepAction(step, data, stepCmd, stepReq, cwdTmpl, stdinTmpl, capture, errOut, oc)
		if err != nil {
			return false, err
		}
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
