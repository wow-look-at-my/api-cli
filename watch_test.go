package main

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseWatchInterval(t *testing.T) {
	cases := []struct {
		in      string
		want    time.Duration
		wantErr string
	}{
		{in: "", want: 0},
		{in: "2s", want: 2 * time.Second},
		{in: "500ms", want: 500 * time.Millisecond},
		{in: "2", want: 2 * time.Second},
		{in: "1.5", want: 1500 * time.Millisecond},
		{in: " 3 ", want: 3 * time.Second},
		{in: "10ms", wantErr: "at least"},
		{in: "-2s", wantErr: "at least"},
		{in: "soon", wantErr: "want a duration"},
	}
	for _, c := range cases {
		got, err := parseWatchInterval("--watch", c.in)
		if c.wantErr != "" {
			require.Error(t, err, "input %q", c.in)
			assert.Contains(t, err.Error(), c.wantErr, "input %q", c.in)
			assert.Contains(t, err.Error(), "--watch", "the error names its source")
			continue
		}
		require.NoError(t, err, "input %q", c.in)
		assert.Equal(t, c.want, got, "input %q", c.in)
	}
}

func TestWatchHeader_NamesTheCommandAndInterval(t *testing.T) {
	at := time.Date(2026, 9, 4, 13, 45, 7, 0, time.UTC)
	got := watchHeader("gh repo get golang/go", 2*time.Second, at)
	assert.Contains(t, got, "every 2s:")
	assert.Contains(t, got, "gh repo get golang/go")
	assert.Contains(t, got, "13:45:07")
}

func TestWatchPainter_RepaintsOverTheLastFrame(t *testing.T) {
	var buf bytes.Buffer
	p := &watchPainter{out: &buf, width: 80, height: 24, tty: true}

	p.paint("head", "one\ntwo")
	first := buf.String()
	assert.NotRegexp(t, `\x1b\[\d+A`, first, "the first frame has nothing above it to move over")
	assert.Contains(t, first, ansiClearLine)
	assert.Contains(t, first, "two")
	painted := p.painted
	assert.Equal(t, 4, painted, "header, blank, and the two body lines")

	buf.Reset()
	p.paint("head", "only")
	second := buf.String()
	assert.Contains(t, second, fmt.Sprintf(ansiUp, painted), "the repaint starts at the last frame's top")
	assert.Contains(t, second, "only")
	assert.Contains(t, second, fmt.Sprintf(ansiUp, 1), "the row the shorter frame vacated is cleared, then stepped back over")
	assert.Equal(t, 3, p.painted)
}

func TestWatchPainter_AppendsWithoutATerminal(t *testing.T) {
	var buf bytes.Buffer
	p := &watchPainter{out: &buf, width: 80, height: 24}

	p.paint("head", "one")
	p.paint("head", "two")
	out := buf.String()
	assert.NotContains(t, out, "\x1b[", "a redirected watch reads as a log, not as escape sequences")
	assert.Contains(t, out, "one")
	assert.Contains(t, out, "two")
	p.finish()
	assert.Equal(t, out, buf.String(), "finish writes nothing without a terminal")
}

func TestWatchPainter_ClipsToTheTerminalHeight(t *testing.T) {
	var buf bytes.Buffer
	p := &watchPainter{out: &buf, width: 80, height: 8, tty: true}

	body := make([]string, 0, 20)
	for i := range 20 {
		body = append(body, fmt.Sprintf("row %d", i))
	}
	lines := p.frame("head", strings.Join(body, "\n"))
	require.Len(t, lines, 7, "the frame leaves the terminal a row for the cursor")
	assert.Contains(t, lines[6], "more line(s)", "a clipped frame says what it dropped")
	assert.Contains(t, lines[6], "16", "header, blank, rows 0-3 shown; 16 lines dropped")
}

func TestWatchPainter_EmptyBodyPaintsTheHeaderAlone(t *testing.T) {
	p := &watchPainter{out: &bytes.Buffer{}, width: 80, height: 24, tty: true}
	assert.Equal(t, []string{"head", ""}, p.frame("head", ""))
	assert.Equal(t, []string{"head", ""}, p.frame("head", "\n\n"))
}

func TestWatchLoop_PaintsAFramePerRunUntilStopped(t *testing.T) {
	t.Serial()
	prevCode := exitCode
	exitCode = 0
	t.Cleanup(func() { exitCode = prevCode })
	prevOut, prevErr := execStdout, execStderr
	t.Cleanup(func() { execStdout, execStderr = prevOut, prevErr })

	var buf bytes.Buffer
	p := &watchPainter{out: &buf, width: 200, height: 24}
	stop := make(chan struct{})

	runs := 0
	err := watchLoop(p, "t leaf", watchMinInterval, stop, func() error {
		runs++
		fmt.Fprintf(execStdout, "run %d", runs)
		if runs == 3 {
			close(stop)
		}
		return nil
	})
	require.NoError(t, err)
	assert.Equal(t, 3, runs)
	assert.Equal(t, interruptExitCode, exitCode, "an interrupted watch reports 130")
	out := buf.String()
	for i := 1; i <= 3; i++ {
		assert.Contains(t, out, fmt.Sprintf("run %d", i))
	}
	assert.Equal(t, prevOut, execStdout, "the frame gives the output channels back")
}

func TestWatchLoop_KeepsGoingAndShowsAFailedFrame(t *testing.T) {
	t.Serial()
	prevCode := exitCode
	exitCode = 0
	t.Cleanup(func() { exitCode = prevCode })
	prevOut, prevErr := execStdout, execStderr
	t.Cleanup(func() { execStdout, execStderr = prevOut, prevErr })

	var buf bytes.Buffer
	p := &watchPainter{out: &buf, width: 200, height: 24}
	stop := make(chan struct{})

	runs := 0
	require.NoError(t, watchLoop(p, "t leaf", watchMinInterval, stop, func() error {
		runs++
		if runs == 1 {
			return errors.New("upstream is down")
		}
		fmt.Fprint(execStdout, "recovered")
		close(stop)
		return nil
	}))
	out := buf.String()
	assert.Contains(t, out, "error: upstream is down", "a failing frame reports the failure in the frame")
	assert.Contains(t, out, "recovered", "a failure does not end the watch")
}

// watchRoot builds a root command carrying the persistent flags watchable and
// watchInterval read.
func watchRoot(t *testing.T, args ...string) *cobra.Command {
	t.Helper()
	root := &cobra.Command{Use: "t"}
	root.PersistentFlags().String("watch", "", "")
	root.PersistentFlags().BoolP("yes", "y", false, "")
	require.NoError(t, root.PersistentFlags().Parse(args))
	return root
}

func TestWatchInterval_ReadsTheFlag(t *testing.T) {
	got, fromFlag, err := watchInterval(watchRoot(t, "--watch", "2s"), "")
	require.NoError(t, err)
	assert.Equal(t, 2*time.Second, got)
	assert.True(t, fromFlag)

	got, fromFlag, err = watchInterval(watchRoot(t), "")
	require.NoError(t, err)
	assert.Zero(t, got, "no flag and no watch= means the leaf runs one time")
	assert.False(t, fromFlag)

	_, _, err = watchInterval(watchRoot(t, "--watch", "nope"), "")
	require.Error(t, err)
}

func TestWatchInterval_TheFlagOverridesTheConfig(t *testing.T) {
	got, fromFlag, err := watchInterval(watchRoot(t), "5s")
	require.NoError(t, err)
	assert.Equal(t, 5*time.Second, got, "watch= is the default interval")
	assert.False(t, fromFlag)

	got, fromFlag, err = watchInterval(watchRoot(t, "--watch", "1s"), "5s")
	require.NoError(t, err)
	assert.Equal(t, time.Second, got, "--watch wins over watch=")
	assert.True(t, fromFlag)

	for _, off := range []string{"off", "0"} {
		got, fromFlag, err = watchInterval(watchRoot(t, "--watch", off), "5s")
		require.NoError(t, err, "--watch %s", off)
		assert.Zero(t, got, "--watch %s runs the leaf one time whatever the config says", off)
		assert.True(t, fromFlag)
	}

	_, _, err = watchInterval(watchRoot(t), "soon")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "watch=", "the error names the config attribute")
}

func TestValidateWatch(t *testing.T) {
	require.NoError(t, validateWatch(&Command{Name: "a"}, "a"))
	require.NoError(t, validateWatch(&Command{Name: "a", Watch: "5s"}, "a"))
	require.NoError(t, validateWatch(&Command{Name: "a", Watch: "2"}, "a"))

	err := validateWatch(&Command{Name: "a", Watch: "soon"}, "commands[0]")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "commands[0]: watch=")

	err = validateWatch(&Command{Name: "a", Watch: "10ms"}, "a")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "at least")

	err = validateWatch(&Command{Name: "a", Watch: "5s", Downloads: []Download{{URL: "http://x/f"}}}, "a")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "<download>")
}

func TestValidate_WatchOnAGroupCoversItsLeaves(t *testing.T) {
	cfg, err := loadStr(t, `<config name="w">
		<run>echo hi</run>
		<command name="board" watch="5s">
			<command name="a"/>
			<command name="b" watch="500ms"/>
		</command></config>`)
	require.NoError(t, err)
	assert.Equal(t, "5s", cfg.Commands[0].Watch)
	assert.Equal(t, "", cfg.Commands[0].Commands[0].Watch, "inheritance happens at build time, not at parse time")
	assert.Equal(t, "500ms", cfg.Commands[0].Commands[1].Watch)

	_, err = loadStr(t, `<config name="w">
		<command name="get" watch="5s"><download><url>http://x/f</url></download></command></config>`)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "watch= does not apply to a <download> leaf")
}

func TestWatchable_RejectsALeafThatCannotRepeat(t *testing.T) {
	dl := Command{Name: "get", Downloads: []Download{{URL: "http://x/f"}}}
	err := watchable(watchRoot(t), dl, "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "<download>")

	err = watchable(watchRoot(t), Command{Name: "rm"}, "really delete?")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--yes")

	require.NoError(t, watchable(watchRoot(t, "--yes"), Command{Name: "rm"}, "really delete?"))
	require.NoError(t, watchable(watchRoot(t), Command{Name: "get"}, ""))
}

func TestTTYOverride_AnswersTheTerminalProbes(t *testing.T) {
	t.Serial()
	prev := ttyOverride
	t.Cleanup(func() { ttyOverride = prev })

	ttyOverride = &termSize{width: 120, height: 40}
	isTTY, width := stdoutTTY()
	assert.True(t, isTTY, "a captured frame still lands on a terminal")
	assert.Equal(t, 120, width)

	isTTY, width, height := stdoutSize()
	assert.True(t, isTTY)
	assert.Equal(t, 120, width)
	assert.Equal(t, 40, height)
}
