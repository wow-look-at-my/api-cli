package main

import (
	"errors"
	"fmt"
	"io"
	"strings"
)

// stepRunner is a single step's run, with everything its attempts share.
type stepRunner struct {
	step      Step
	data      map[string]any
	cmd       *Cmd
	req       *Request
	cwdTmpl   string
	stdinTmpl string
	capture   stepCapture
	errOut    io.Writer
	oc        *stepOutcome
	// pos is the element of over= this run is for. nil for a plain step.
	pos *stepPos
}

// stepPos is a single element of a repeated step.
type stepPos struct {
	index, total int
	label        string
}

// where names the step and, for a repeated step, the element. Every error
// from a run starts with it.
func (r *stepRunner) where() string {
	if r.pos == nil {
		return fmt.Sprintf("step %q", r.step.Name)
	}
	return fmt.Sprintf("step %q [%d/%d] %s", r.step.Name, r.pos.index+1, r.pos.total, r.pos.label)
}

// progress is the live state of this run, for the step watcher.
func (r *stepRunner) progress(attempt, attempts, retry int, last any) stepProgress {
	p := stepProgress{Step: r.step.Name, Attempt: attempt, Attempts: attempts, Retry: retry, Retries: r.step.Retries, Status: responseStatus(last)}
	if r.pos != nil {
		p.Index, p.Total, p.Item = r.pos.index+1, r.pos.total, r.pos.label
	}
	return p
}

// untilExhausted is a poll whose predicate never held. A repeated step with on-error="skip" skips the element.
type untilExhausted struct{ msg string }

func (e *untilExhausted) Error() string { return e.msg }

// action performs the step's action a single time, or, with `until=`, until
// the predicate holds: an async job that answers "pending" is polled here
// rather than in a shell loop around the whole program.
//
// A failed run comes back as a *callFailure. The error return is for a config
// problem and for an exhausted poll.
func (r *stepRunner) action() (string, *callFailure, error) {
	if r.step.Until == "" {
		stepWatch.update(r.progress(0, 0, 0, nil))
		return r.tryOnce(0, 0, nil)
	}

	interval, err := pollInterval(r.step)
	if err != nil {
		return "", nil, fmt.Errorf("%s: %w", r.where(), err)
	}
	attempts := r.step.Attempts
	if attempts <= 0 {
		attempts = defaultPollAttempts
	}

	var last any
	for attempt := 1; attempt <= attempts; attempt++ {
		if done := attempt - 1; done > 0 && done%stallReportEvery == 0 {
			stepWatch.stalled(r.progress(done, attempts, 0, last))
		}
		stepWatch.update(r.progress(attempt, attempts, 0, last))
		out, fail, err := r.tryOnce(attempt, attempts, last)
		if err != nil || fail != nil {
			return out, fail, err
		}
		last = parseResult(out)
		verdict, err := renderString(r.step.Until, pollContext(r.data, last))
		if err != nil {
			return "", nil, fmt.Errorf("%s: render until: %w", r.where(), err)
		}
		logVerbose("%s: attempt %d/%d: until %q => %q", r.where(), attempt, attempts, r.step.Until, verdict)
		if isTruthy(verdict) {
			return out, nil, nil
		}
		if attempt < attempts {
			pollSleep(interval)
		}
	}
	return "", nil, &untilExhausted{fmt.Sprintf("%s: until %q did not hold in %d attempt(s); last response: %s",
		r.where(), r.step.Until, attempts, jsonCompact(last))}
}

// tryOnce runs the step, and runs it again after a failure up to retries=
// times, at the download queue's fixed cadence. A failed try with a retry
// left is reported, so a slow recovery is visible.
func (r *stepRunner) tryOnce(attempt, attempts int, last any) (string, *callFailure, error) {
	for try := 0; ; try++ {
		out, fail, err := r.runOnce()
		if err != nil {
			return "", nil, err
		}
		r.oc.executions++
		if fail == nil || try >= r.step.Retries {
			return out, fail, nil
		}
		fail.report(r.errOut, r.where())
		fmt.Fprintf(r.errOut, "%s: retry %d/%d in %s\n", r.where(), try+1, r.step.Retries, retryDelay)
		stepWatch.update(r.progress(attempt, attempts, try+1, last))
		pollSleep(retryDelay)
	}
}

// runOnce renders a step's entry and runs it a single time.
func (r *stepRunner) runOnce() (string, *callFailure, error) {
	step, data := r.step, r.data
	stepEntry, err := renderEntry(step.Entry, data)
	if err != nil {
		return "", nil, fmt.Errorf("%s: render entry: %w", r.where(), err)
	}
	if stepEntry == nil {
		stepEntry = map[string]any{}
	}
	data["entry"] = stepEntry
	logDebug("%s: entry: %s", r.where(), jsonCompact(stepEntry))

	if r.req.Defined() {
		logVerbose("%s: requesting", r.where())
		out, fail := performRequest(r.req, data, r.errOut)
		return out, fail, nil
	}

	stepCwdTmpl := r.cwdTmpl
	if step.Cwd != "" {
		stepCwdTmpl = step.Cwd
	}
	stepCwd, err := renderCwd(stepCwdTmpl, data)
	if err != nil {
		return "", nil, fmt.Errorf("%s: render cwd: %w", r.where(), err)
	}

	stepStdinTmpl := r.stdinTmpl
	if step.Stdin != "" {
		stepStdinTmpl = step.Stdin
	}
	stepStdin, err := renderStdin(stepStdinTmpl, data)
	if err != nil {
		return "", nil, fmt.Errorf("%s: render stdin: %w", r.where(), err)
	}

	logVerbose("%s: executing", r.where())
	out, code := r.capture(r.cmd, stepCwd, stepStdin, data)
	if code != 0 {
		return out, &callFailure{code: code, msg: fmt.Sprintf("command exited %d", code), quiet: true}, nil
	}
	return out, nil, nil
}

// runOver repeats a step a single time per element of the list at step.Over.
// The element rides in the data context as `.item`, and its position as
// `.index`. The result pairs each element with its own response, in the
// source order.
//
// A failing element fails the whole step unless on-error="skip": a screen
// with some builds silently missing reads as a shorter queue, not as a broken
// run. So a skip is always named, and the caller exits non-zero with a summary.
func (r *stepRunner) runOver(results map[string]any) (bool, error) {
	step, data := r.step, r.data
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
	var skipped []string
	for i, element := range list {
		data["item"], data["index"] = element, i
		r.pos = &stepPos{index: i, total: len(list), label: itemLabel(element)}
		out, fail, err := r.action()
		logDebugBlock(fmt.Sprintf("%s: stdout", r.where()), out)

		var exhausted *untilExhausted
		if err != nil && !errors.As(err, &exhausted) {
			return false, err
		}
		if err == nil && fail == nil {
			stepWatch.finished(r.progress(0, 0, 0, nil), "done")
			collected = append(collected, map[string]any{"item": element, "result": parseResult(out)})
			continue
		}

		if step.OnError != "skip" {
			if err != nil {
				return false, err
			}
			fail.report(r.errOut, r.where())
			r.oc.output, r.oc.code = out, fail.code
			return false, nil
		}
		if err != nil {
			fmt.Fprintf(r.errOut, "error: %v (skipped)\n", err)
		} else {
			fail.report(r.errOut, r.where()+" (skipped)")
		}
		stepWatch.finished(r.progress(0, 0, 0, nil), "skipped")
		skipped = append(skipped, r.pos.label)
	}
	results[step.Name] = collected
	if len(skipped) > 0 {
		r.oc.skipped = append(r.oc.skipped, stepSkips{step: step.Name, total: len(list), items: skipped})
	}
	return true, nil
}

// itemLabel names an element in an error and on the progress line: a string
// or a number as itself, a record or a list as compact JSON. The clip keeps a
// large record from flooding the line.
func itemLabel(element any) string {
	var s string
	switch v := element.(type) {
	case string:
		s = v
	case map[string]any, []any, nil:
		s = jsonCompact(v)
	default:
		s = fmt.Sprint(v)
	}
	return clipDisplay(strings.TrimSpace(s), 60)
}
