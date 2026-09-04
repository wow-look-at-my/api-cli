package main

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// noPollSleep replaces the wait between attempts, so a test of the loop costs
// no real time. It reports what the loop asked to wait.
func noPollSleep(t *testing.T) func() []time.Duration {
	t.Helper()
	t.Serial()
	var mu sync.Mutex
	var waits []time.Duration
	prev := pollSleep
	pollSleep = func(d time.Duration) {
		mu.Lock()
		waits = append(waits, d)
		mu.Unlock()
	}
	t.Cleanup(func() { pollSleep = prev })
	return func() []time.Duration {
		mu.Lock()
		defer mu.Unlock()
		return append([]time.Duration(nil), waits...)
	}
}

// jobServer answers "pending" until the given attempt, then "done".
func jobServer(t *testing.T, doneOn int) *httptest.Server {
	t.Helper()
	var mu sync.Mutex
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		calls++
		n := calls
		mu.Unlock()
		if n < doneOn {
			fmt.Fprint(w, `{"status":"pending"}`)
			return
		}
		writeJSON(w, map[string]any{"status": "done", "attempt": n})
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestPoll_StepRepeatsUntilTheConditionHolds(t *testing.T) {
	waits := noPollSleep(t)
	srv := jobServer(t, 3)
	swapHTTPClient(t, srv)

	cfg, err := loadStr(t, `<config name="p"><command name="wait">
		<steps>
			<step name="job" until="{{ eq .status &quot;done&quot; }}" interval="250ms" attempts="10">
				<run><request><url>`+srv.URL+`/job</url></request></run>
			</step>
		</steps>
		<run><argv>printf</argv><argv>%s</argv><argv><value name="result.job.attempt"/></argv></run>
	</command></config>`)
	require.NoError(t, err)

	code, out, errOut := execCmdFull(t, cfg, "wait")
	require.Equal(t, 0, code, "stderr: %s", errOut)
	assert.Equal(t, "3", out, "the result is the body that satisfied until=")
	assert.Equal(t, []time.Duration{250 * time.Millisecond, 250 * time.Millisecond}, waits(),
		"one wait between attempts, none after the last")
}

// A poll that never settles fails the run and shows the answer it kept getting.
// A bare timeout would leave the reason to guesswork.
func TestPoll_ExhaustedAttemptsFailWithTheLastBody(t *testing.T) {
	noPollSleep(t)
	calls := 0
	capture := func(*Cmd, string, string, any) (string, int) {
		calls++
		return `{"status":"pending"}`, 0
	}
	step := Step{
		Name:     "job",
		Until:    `{{ eq .status "done" }}`,
		Attempts: 3,
		Command:  &Cmd{Shell: true, Template: "true"},
	}

	var oc stepOutcome
	_, _, err := runStepAction(step, map[string]any{}, step.Command, nil, "", "", capture, io.Discard, &oc)
	require.Error(t, err)
	assert.Contains(t, err.Error(), `did not hold in 3 attempt(s)`)
	assert.Contains(t, err.Error(), `"status":"pending"`)
	assert.Equal(t, 3, calls, "every attempt runs, and none after the cap")
	assert.Equal(t, 3, oc.executions, "each attempt counts as an execution")
}

// A step that answers with a failure has answered. Asking again cannot change
// it, so the poll ends there with that exit code.
func TestPoll_NonZeroExitEndsThePoll(t *testing.T) {
	noPollSleep(t)
	calls := 0
	capture := func(*Cmd, string, string, any) (string, int) {
		calls++
		return "boom", 3
	}
	step := Step{
		Name:     "job",
		Until:    `{{ eq .status "done" }}`,
		Attempts: 5,
		Command:  &Cmd{Shell: true, Template: "false"},
	}

	var oc stepOutcome
	out, code, err := runStepAction(step, map[string]any{}, step.Command, nil, "", "", capture, io.Discard, &oc)
	require.NoError(t, err)
	assert.Equal(t, 3, code)
	assert.Equal(t, "boom", out)
	assert.Equal(t, 1, calls)
}

// The predicate reads the body it just got, and the whole body stays reachable.
func TestPoll_UntilSeesTheBodyAndTheRunContext(t *testing.T) {
	noPollSleep(t)
	srv := jobServer(t, 2)
	swapHTTPClient(t, srv)

	cfg, err := loadStr(t, `<config name="p"><command name="wait">
		<steps>
			<step name="job" until="{{ eq .body.status &quot;done&quot; }}" attempts="4">
				<run><request><url>`+srv.URL+`/job</url></request></run>
			</step>
		</steps>
		<run><argv>printf</argv><argv>%s</argv><argv><value name="result.job.status"/></argv></run>
	</command></config>`)
	require.NoError(t, err)

	code, out, errOut := execCmdFull(t, cfg, "wait")
	require.Equal(t, 0, code, "stderr: %s", errOut)
	assert.Equal(t, "done", out)
}

// A repeated step polls each element in turn, so a fan-out over N jobs needs no
// call from outside.
func TestPoll_RepeatedStepPollsEachElement(t *testing.T) {
	noPollSleep(t)
	var mu sync.Mutex
	calls := map[string]int{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		calls[r.URL.Path]++
		n := calls[r.URL.Path]
		mu.Unlock()
		if n < 2 {
			fmt.Fprint(w, `{"status":"pending"}`)
			return
		}
		writeJSON(w, map[string]any{"status": "done", "path": r.URL.Path})
	}))
	t.Cleanup(srv.Close)
	swapHTTPClient(t, srv)

	cfg, err := loadStr(t, `<config name="p"><command name="wait">
		<arg name="ids" variadic="true"/>
		<steps>
			<step name="jobs" over="arg.ids" until="{{ eq .status &quot;done&quot; }}" attempts="4">
				<run><request><url>`+srv.URL+`/job/<value name="item"/></url></request></run>
			</step>
		</steps>
		<run><argv>printf</argv><argv>%s</argv><argv>{{ range .result.jobs }}{{ .result.path }} {{ end }}</argv></run>
	</command></config>`)
	require.NoError(t, err)

	code, out, errOut := execCmdFull(t, cfg, "wait", "a", "b")
	require.Equal(t, 0, code, "stderr: %s", errOut)
	assert.Equal(t, "/job/a /job/b ", out, "one result per element, in the input order")
}

// A variadic arg is a []string, and over= walks it without a JSON detour.
func TestOver_IteratesAVariadicArgDirectly(t *testing.T) {
	srv, seen := recordingServer(t, map[string]string{"/x/a": `1`, "/x/b": `2`})
	swapHTTPClient(t, srv)

	cfg, err := loadStr(t, `<config name="p"><command name="go">
		<arg name="ids" variadic="true"/>
		<steps>
			<step name="each" over="arg.ids">
				<run><request><url>`+srv.URL+`/x/<value name="item"/></url></request></run>
			</step>
		</steps>
		<run><argv>true</argv></run>
	</command></config>`)
	require.NoError(t, err)

	code, _, errOut := execCmdFull(t, cfg, "go", "a", "b")
	require.Equal(t, 0, code, "stderr: %s", errOut)
	assert.Equal(t, []string{"/x/a", "/x/b"}, seen())
}

func TestPoll_RejectsPollAttributesWithoutUntil(t *testing.T) {
	_, err := loadStr(t, `<config name="p"><command name="wait">
		<steps><step name="job" interval="1s"><run><argv>true</argv></run></step></steps>
		<run><argv>true</argv></run>
	</command></config>`)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "they need until=")
}

func TestPoll_RejectsABadInterval(t *testing.T) {
	_, err := loadStr(t, `<config name="p"><command name="wait">
		<steps><step name="job" until="{{ true }}" interval="soon"><run><argv>true</argv></run></step></steps>
		<run><argv>true</argv></run>
	</command></config>`)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "must be a duration")
}
