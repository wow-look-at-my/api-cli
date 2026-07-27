package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// tctx builds a format context with explicit tty/width (the timeline sink reads
// both: tty gates color, width sets the axis width).
func tctx(parsed any, tty bool, width int) map[string]any {
	return map[string]any{"data": parsed, "tty": tty, "width": width}
}

// axisLine returns the axis row of a rendered timeline (the line bounded by the
// box-drawing end caps), or "" if not found.
func axisLine(out string) string {
	for _, ln := range strings.Split(out, "\n") {
		if strings.ContainsRune(ln, '├') && strings.ContainsRune(ln, '┤') {
			return ln
		}
	}
	return ""
}

func TestTimeline_PointEventsNoColor(t *testing.T) {
	parsed := []any{
		map[string]any{"name": "Kickoff", "when": "2024-01-08"},
		map[string]any{"name": "Launch", "when": "2024-06-30"},
	}
	f := &Fields{List: []Field{
		{Name: "label", Path: "name"},
		{Name: "date", Path: "when"},
	}}
	out, err := renderFields(f, parsed, tctx(parsed, false, 0), "timeline", 0)
	require.NoError(t, err)

	assert.Contains(t, out, "# Timeline") // default header
	assert.Contains(t, out, "●")          // point markers
	assert.Contains(t, out, "Kickoff")    // mapped labels
	assert.Contains(t, out, "Launch")
	assert.NotContains(t, out, "\x1b[") // tty=false => no ANSI color
}

func TestTimeline_DurationEvents(t *testing.T) {
	parsed := []any{
		map[string]any{"phase": "Build", "from": "2024-02-01", "to": "2024-05-01"},
	}
	f := &Fields{List: []Field{
		{Name: "label", Path: "phase"},
		{Name: "start", Path: "from"},
		{Name: "end", Path: "to"},
	}}
	out, err := renderFields(f, parsed, tctx(parsed, false, 0), "timeline", 0)
	require.NoError(t, err)
	assert.Contains(t, out, "█") // duration bar
	assert.Contains(t, out, "Build")
}

func TestTimeline_SkipsRecordsWithoutPlacement(t *testing.T) {
	parsed := []any{
		map[string]any{"name": "Has date", "when": "2024-01-08"},
		map[string]any{"name": "No date"}, // skipped: no temporal field
	}
	f := &Fields{List: []Field{
		{Name: "label", Path: "name"},
		{Name: "date", Path: "when"},
	}}
	out, err := renderFields(f, parsed, tctx(parsed, false, 0), "timeline", 0)
	require.NoError(t, err)
	assert.Contains(t, out, "Has date")
	assert.NotContains(t, out, "No date")
}

func TestTimeline_AllSkippedRendersNoEvents(t *testing.T) {
	parsed := []any{map[string]any{"name": "x"}}
	f := &Fields{List: []Field{{Name: "label", Path: "name"}}}
	out, err := renderFields(f, parsed, tctx(parsed, false, 0), "timeline", 0)
	require.NoError(t, err)
	assert.Contains(t, out, "(no events)")
}

func TestTimeline_ColorOnTTY(t *testing.T) {
	t.Setenv("NO_COLOR", "")
	parsed := []any{map[string]any{"name": "Kickoff", "when": "2024-01-08"}}
	f := &Fields{List: []Field{
		{Name: "label", Path: "name"},
		{Name: "date", Path: "when"},
	}}
	out, err := renderFields(f, parsed, tctx(parsed, true, 80), "timeline", 0)
	require.NoError(t, err)
	assert.Contains(t, out, "\x1b[") // tty + no NO_COLOR => ANSI color present
}

func TestTimeline_NoColorEnvForcesPlain(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	parsed := []any{map[string]any{"name": "Kickoff", "when": "2024-01-08"}}
	f := &Fields{List: []Field{
		{Name: "label", Path: "name"},
		{Name: "date", Path: "when"},
	}}
	out, err := renderFields(f, parsed, tctx(parsed, true, 80), "timeline", 0)
	require.NoError(t, err)
	assert.NotContains(t, out, "\x1b[") // NO_COLOR wins even on a TTY
}

func TestTimeline_WidthFromContext(t *testing.T) {
	parsed := []any{
		map[string]any{"name": "A", "when": "2024-01-01"},
		map[string]any{"name": "B", "when": "2024-12-31"},
	}
	f := &Fields{List: []Field{
		{Name: "label", Path: "name"},
		{Name: "date", Path: "when"},
	}}
	// tty=false keeps color off so the axis line is plain (no ANSI escapes to
	// inflate the rune count); width is applied independently of color.
	out, err := renderFields(f, parsed, tctx(parsed, false, 40), "timeline", 0)
	require.NoError(t, err)
	line := axisLine(out)
	require.NotEmpty(t, line)
	assert.Equal(t, 40, len([]rune(line))) // axis width follows .width
}

func TestTimeline_ColorFieldAndDescription(t *testing.T) {
	parsed := []any{
		map[string]any{"name": "Kickoff", "when": "2024-01-08", "note": "first", "hue": "green"},
	}
	f := &Fields{List: []Field{
		{Name: "label", Path: "name"},
		{Name: "date", Path: "when"},
		{Name: "description", Path: "note"},
		{Name: "color", Path: "hue"},
	}}
	out, err := renderFields(f, parsed, tctx(parsed, false, 0), "timeline", 0)
	require.NoError(t, err)
	assert.Contains(t, out, "Kickoff")
	assert.Contains(t, out, "first") // description rendered after the date
}

func TestTimeline_ShowInGating(t *testing.T) {
	parsed := []any{map[string]any{"name": "Kickoff", "when": "2024-01-08"}}
	// A label visible everywhere except timeline is dropped from the event.
	f := &Fields{List: []Field{
		{Name: "label", Path: "name", ShowIn: "!timeline"},
		{Name: "date", Path: "when"},
	}}
	out, err := renderFields(f, parsed, tctx(parsed, false, 0), "timeline", 0)
	require.NoError(t, err)
	assert.NotContains(t, out, "Kickoff") // label gated out of the timeline
	assert.Contains(t, out, "●")          // still placed by its date
}

func TestTimeline_DerivedFieldsFromMatchingKeys(t *testing.T) {
	// No <fields> declared: keys named label/date are derived and map directly,
	// so --as=timeline works on already-shaped data.
	parsed := []any{
		map[string]any{"label": "Kickoff", "date": "2024-01-08"},
	}
	f := &Fields{}
	out, err := renderFields(f, parsed, tctx(parsed, false, 0), "timeline", 0)
	require.NoError(t, err)
	assert.Contains(t, out, "Kickoff")
	assert.Contains(t, out, "●")
}

func TestTimeline_BadDateErrors(t *testing.T) {
	parsed := []any{map[string]any{"name": "x", "when": "not-a-date"}}
	f := &Fields{List: []Field{
		{Name: "label", Path: "name"},
		{Name: "date", Path: "when"},
	}}
	_, err := renderFields(f, parsed, tctx(parsed, false, 0), "timeline", 0)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "timeline:")
}

func TestTimeline_ExprAndDefaultApply(t *testing.T) {
	// Field machinery (expr=, default=) flows through cellValue into the event.
	parsed := []any{map[string]any{"d": "2024-03-01"}}
	f := &Fields{List: []Field{
		{Name: "label", Expr: "{{.d}} milestone"},
		{Name: "date", Path: "d"},
		{Name: "color", Default: "magenta"},
	}}
	out, err := renderFields(f, parsed, tctx(parsed, false, 0), "timeline", 0)
	require.NoError(t, err)
	assert.Contains(t, out, "2024-03-01 milestone")
}

func TestIntegration_AsTimeline(t *testing.T) {
	events := `[{"name":"Kickoff","when":"2024-01-08"},{"name":"Launch","when":"2024-06-30"}]`
	cfg := &Config{
		Name: "t",
		Commands: []Command{{
			Name:    "roadmap",
			Command: &Cmd{Shell: true, Template: "printf '%s' '" + events + "'"},
			Fields: &Fields{List: []Field{
				{Name: "label", Path: "name"},
				{Name: "date", Path: "when"},
			}},
		}},
	}
	code, out := execCmd(t, cfg, "roadmap", "--as", "timeline")
	require.Equal(t, 0, code)
	assert.Contains(t, out, "# Timeline")
	assert.Contains(t, out, "Kickoff")
	assert.Contains(t, out, "Launch")
	assert.Contains(t, out, "●")
	assert.NotContains(t, out, "\x1b[") // test stdout is not a TTY
}

// sanity: the marshaled doc the sink builds is valid timeline JSON.
func TestTimeline_DocShapeIsValidJSON(t *testing.T) {
	doc := map[string]any{"events": []map[string]string{{"label": "x", "date": "2024-01-01"}}}
	b, err := json.Marshal(doc)
	require.NoError(t, err)
	assert.JSONEq(t, `{"events":[{"label":"x","date":"2024-01-01"}]}`, string(b))
}
