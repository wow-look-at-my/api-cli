package fields

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeTimeline puts an ascii-timeline script first on PATH. The script saves
// its stdin to in.json and its arguments to args.txt, then runs body. It
// returns the directory that holds the saved files.
func fakeTimeline(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	script := "#!/bin/sh\n" +
		"cat > \"" + dir + "/in.json\"\n" +
		"printf '%s\\n' \"$@\" > \"" + dir + "/args.txt\"\n" +
		body + "\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, timelineBinary), []byte(script), 0o755))
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return dir
}

func timelineFields() *Fields {
	return &Fields{List: []Field{
		{Name: "label", Path: "name"},
		{Name: "date", Path: "when"},
		{Name: "color", Path: "hue"},
	}}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	require.NoError(t, err)
	return string(b)
}

func TestTimelineSink_PipesJSONAndReturnsStdout(t *testing.T) {
	t.Setenv("NO_COLOR", "")
	dir := fakeTimeline(t, "printf 'DRAWN BY FAKE\\n'")
	parsed := []any{
		map[string]any{"name": "Kickoff", "when": "2024-01-08", "hue": "green"},
		map[string]any{"name": "No date"},
	}
	ctx := map[string]any{"data": parsed, "tty": true, "width": 60}

	out, err := Render(testRenderer, timelineFields(), parsed, ctx, "timeline", 0)
	require.NoError(t, err)
	assert.Equal(t, "DRAWN BY FAKE\n", out)

	var doc map[string][]map[string]string
	require.NoError(t, json.Unmarshal([]byte(readFile(t, filepath.Join(dir, "in.json"))), &doc))
	assert.Equal(t, []map[string]string{
		{"label": "Kickoff", "date": "2024-01-08", "color": "green"},
	}, doc["events"])
	assert.Equal(t, "--width\n60\n", readFile(t, filepath.Join(dir, "args.txt")))
}

func TestTimelineSink_NoColorOffTerminal(t *testing.T) {
	t.Setenv("NO_COLOR", "")
	dir := fakeTimeline(t, "printf 'x'")
	parsed := []any{map[string]any{"name": "A", "when": "2024-01-01"}}
	ctx := map[string]any{"data": parsed, "tty": false}

	_, err := Render(testRenderer, timelineFields(), parsed, ctx, "timeline", 0)
	require.NoError(t, err)
	assert.Equal(t, "--no-color\n", readFile(t, filepath.Join(dir, "args.txt")))
}

func TestTimelineSink_NoColorEnvOnTerminal(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	dir := fakeTimeline(t, "printf 'x'")
	parsed := []any{map[string]any{"name": "A", "when": "2024-01-01"}}
	ctx := map[string]any{"data": parsed, "tty": true, "width": 80}

	_, err := Render(testRenderer, timelineFields(), parsed, ctx, "timeline", 0)
	require.NoError(t, err)
	assert.Equal(t, "--width\n80\n--no-color\n", readFile(t, filepath.Join(dir, "args.txt")))
}

func TestTimelineSink_ColorOnTerminalPassesNoFlag(t *testing.T) {
	t.Setenv("NO_COLOR", "")
	dir := fakeTimeline(t, "printf 'x'")
	parsed := []any{map[string]any{"name": "A", "when": "2024-01-01"}}
	ctx := map[string]any{"data": parsed, "tty": true}

	_, err := Render(testRenderer, timelineFields(), parsed, ctx, "timeline", 0)
	require.NoError(t, err)
	assert.Equal(t, "\n", readFile(t, filepath.Join(dir, "args.txt")))
}

func TestTimelineSink_DurationAndShowIn(t *testing.T) {
	t.Setenv("NO_COLOR", "")
	dir := fakeTimeline(t, "printf 'x'")
	parsed := []any{map[string]any{"name": "Build", "from": "2024-02-01", "to": "2024-05-01"}}
	f := &Fields{List: []Field{
		{Name: "label", Path: "name", ShowIn: "!timeline"},
		{Name: "start", Path: "from"},
		{Name: "end", Path: "to"},
	}}
	_, err := Render(testRenderer, f, parsed, map[string]any{"data": parsed}, "timeline", 0)
	require.NoError(t, err)

	var doc map[string][]map[string]string
	require.NoError(t, json.Unmarshal([]byte(readFile(t, filepath.Join(dir, "in.json"))), &doc))
	assert.Equal(t, []map[string]string{{"start": "2024-02-01", "end": "2024-05-01"}}, doc["events"])
}

func TestTimelineSink_AllSkippedSendsEmptyEvents(t *testing.T) {
	t.Setenv("NO_COLOR", "")
	dir := fakeTimeline(t, "printf '(no events)\\n'")
	parsed := []any{map[string]any{"name": "x"}}
	out, err := Render(testRenderer, timelineFields(), parsed, map[string]any{"data": parsed}, "timeline", 0)
	require.NoError(t, err)
	assert.Equal(t, "(no events)\n", out)
	assert.JSONEq(t, `{"events":[]}`, readFile(t, filepath.Join(dir, "in.json")))
}

func TestTimelineSink_MissingBinaryErrors(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	parsed := []any{map[string]any{"name": "A", "when": "2024-01-01"}}
	_, err := Render(testRenderer, timelineFields(), parsed, map[string]any{"data": parsed}, "timeline", 0)
	require.Error(t, err)
	msg := err.Error()
	assert.Contains(t, msg, "ascii-timeline")
	assert.Contains(t, msg, "not on PATH")
	assert.Contains(t, msg, "apt install ascii-timeline")
	assert.Contains(t, msg, "brew install ascii-timeline")
}

func TestTimelineSink_NonZeroExitErrors(t *testing.T) {
	t.Setenv("NO_COLOR", "")
	fakeTimeline(t, "echo 'bad date' >&2\nexit 3")
	parsed := []any{map[string]any{"name": "A", "when": "nope"}}
	out, err := Render(testRenderer, timelineFields(), parsed, map[string]any{"data": parsed}, "timeline", 0)
	require.Error(t, err)
	assert.Empty(t, out)
	msg := err.Error()
	assert.Contains(t, msg, "ascii-timeline exited with status 3")
	assert.Contains(t, msg, "bad date")
	assert.Contains(t, msg, "apt install ascii-timeline")
}

func TestTimelineSink_EmptyOutputErrors(t *testing.T) {
	t.Setenv("NO_COLOR", "")
	fakeTimeline(t, "true")
	parsed := []any{map[string]any{"name": "A", "when": "2024-01-01"}}
	_, err := Render(testRenderer, timelineFields(), parsed, map[string]any{"data": parsed}, "timeline", 0)
	require.Error(t, err)
	assert.True(t, strings.Contains(err.Error(), "printed nothing"))
}
