package main

import (
	"fmt"
	"io"
	"strings"
	"time"
)

// Defaults for a polling step.
const (
	defaultPollInterval = time.Second
	defaultPollAttempts = 60
)

// pollSleep waits between attempts of a polling step and between retries.
var pollSleep = time.Sleep

// stepCapture runs a single step's command and returns its stdout and exit
// code. The CLI and MCP paths differ in where stderr goes and whether the
// child may inherit the process's stdin, so each supplies its own.
type stepCapture func(c *Cmd, cwd, stdin string, data any) (string, int)

// stepOutcome reports how a run of steps ended. output carries the failing
// step's stdout (empty on success) for callers that report it to the user.
type stepOutcome struct {
	executions int
	output     string
	code       int
	// skipped lists what each on-error="skip" step left out.
	skipped []stepSkips
}

// stepSkips is what a single repeated step left out of its result.
type stepSkips struct {
	step  string
	total int
	items []string
}

// reportSkips writes a summary line per step that skipped elements, and
// reports whether there was one. A run that skipped anything must not exit 0.
func reportSkips(w io.Writer, skips []stepSkips) bool {
	for _, s := range skips {
		fmt.Fprintf(w, "%s: %d of %d items skipped: %s\n", s.step, len(s.items), s.total, strings.Join(s.items, ", "))
	}
	return len(skips) > 0
}

// runSteps executes a leaf's steps in order, storing each step's parsed output
// in results under the step's name. Steps see prior results through data, so a
// later step's entry, url, or command can read `.result.<name>`.
//
// A step runs whichever of command/request it declares; declaring neither
// inherits the leaf's effective run, which is how a step reuses the ancestor
// <request> with nothing but a different <entry>. cwdTmpl/stdinTmpl are the
// leaf's, likewise overridable per step.
func runSteps(steps []Step, data map[string]any, results map[string]any, cmdTmpl *Cmd, request *Request, cwdTmpl, stdinTmpl string, capture stepCapture, errOut io.Writer) (stepOutcome, error) {
	var oc stepOutcome
	defer stepWatch.idle()
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

		r := &stepRunner{step: step, data: data, cmd: cmdTmpl, req: request, cwdTmpl: cwdTmpl, stdinTmpl: stdinTmpl, capture: capture, errOut: errOut, oc: &oc}
		switch {
		case step.Request.Defined():
			r.cmd, r.req = nil, step.Request
		case step.Command.Defined():
			r.cmd, r.req = step.Command, nil
		}
		if !r.cmd.Defined() && !r.req.Defined() {
			return oc, fmt.Errorf("step %q: no command or request available", step.Name)
		}

		if step.Over != "" {
			done, err := r.runOver(results)
			if err != nil || !done {
				return oc, err
			}
			continue
		}

		out, fail, err := r.action()
		if err != nil {
			return oc, err
		}
		logDebugBlock(fmt.Sprintf("step %q: stdout", step.Name), out)
		if fail != nil {
			logVerbose("step %q: exit code %d", step.Name, fail.code)
			if !fail.quiet {
				fail.report(errOut, "")
			}
			oc.output, oc.code = out, fail.code
			return oc, nil
		}
		results[step.Name] = parseResult(out)
	}
	return oc, nil
}

// pollContext is what `until=` is evaluated against: the run's own context with
// the last response promoted onto it, so an async job's status field reads as
// `.status`. The whole body stays reachable as `.body`.
func pollContext(data map[string]any, body any) map[string]any {
	return promoteCtx(data, body, "body")
}

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
// duration is a config error rather than a surprise on the earliest poll.
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

// validateStepFailure checks retries= and on-error= at load time.
func validateStepFailure(s Step, where string) error {
	if s.Retries < 0 {
		return fmt.Errorf("%s: retries=%d must be >= 0", where, s.Retries)
	}
	switch s.OnError {
	case "", "fail":
	case "skip":
		if s.Over == "" {
			return fmt.Errorf("%s: on-error=\"skip\" leaves failed elements out of a repeated step, so it needs over=", where)
		}
	default:
		return fmt.Errorf("%s: on-error=%q must be fail or skip", where, s.OnError)
	}
	return nil
}

// restore puts a context key back the way the step found it.
func restore(data map[string]any, key string, value any, had bool) {
	if had {
		data[key] = value
		return
	}
	delete(data, key)
}
