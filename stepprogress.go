package main

import (
	"fmt"
	"strings"
)

// stallReportEvery is how many unsettled poll attempts pass between the lines that say a poll still waits.
const stallReportEvery = 10

type stepProgress struct {
	Step              string
	Index, Total      int
	Item              string
	Attempt, Attempts int
	Retry, Retries    int
	Status            string
}

// label is the step and the element: "jobs 7/20 JOB-7".
func (p stepProgress) label() string {
	if p.Total == 0 {
		return p.Step
	}
	return fmt.Sprintf("%s %d/%d %s", p.Step, p.Index, p.Total, p.Item)
}

// line is the live status line: the label, the attempt, the retry and the
// last status, spaces apart.
func (p stepProgress) line() string {
	parts := []string{p.label()}
	if p.Attempts > 0 {
		parts = append(parts, fmt.Sprintf("attempt %d/%d", p.Attempt, p.Attempts))
	}
	if p.Retry > 0 {
		parts = append(parts, fmt.Sprintf("retry %d/%d", p.Retry, p.Retries))
	}
	if p.Status != "" {
		parts = append(parts, "status="+p.Status)
	}
	return strings.Join(parts, "  ")
}

// stepWatcher receives step progress.
type stepWatcher struct {
	show func(p *stepProgress)
	log  func(line string)
}

// stepWatch is the active watcher, nil outside a download session. The methods accept a nil receiver.
var stepWatch *stepWatcher

func (w *stepWatcher) update(p stepProgress) {
	if w != nil && w.show != nil {
		w.show(&p)
	}
}

func (w *stepWatcher) stalled(p stepProgress) {
	if w == nil || w.log == nil {
		return
	}
	line := fmt.Sprintf("%s: until not true after %d/%d attempts", p.label(), p.Attempt, p.Attempts)
	if p.Status != "" {
		line += ", status=" + p.Status
	}
	w.log(line)
}

func (w *stepWatcher) finished(p stepProgress, outcome string) {
	if w != nil && w.log != nil && p.Total > 0 {
		w.log(p.label() + ": " + outcome)
	}
}

func (w *stepWatcher) idle() {
	if w != nil && w.show != nil {
		w.show(nil)
	}
}

// responseStatus is the `status` field of a parsed response, or "".
func responseStatus(body any) string {
	m, ok := body.(map[string]any)
	if !ok {
		return ""
	}
	s, ok := m["status"]
	if !ok || s == nil {
		return ""
	}
	return fmt.Sprint(s)
}
