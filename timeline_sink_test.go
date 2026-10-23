package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// echoTimeline puts an ascii-timeline script first on PATH. The script prints
// the JSON document it reads on stdin, so a test sees what the sink sent.
func echoTimeline(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "ascii-timeline"), []byte("#!/bin/sh\ncat\n"), 0o755))
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func TestTimeline_ExprAndDefaultApply(t *testing.T) {
	echoTimeline(t)
	// Field machinery (expr=, default=) flows through cellValue into the event.
	parsed := []any{map[string]any{"d": "2024-03-01"}}
	f := &Fields{List: []Field{
		{Name: "label", Expr: "{{.d}} milestone"},
		{Name: "date", Path: "d"},
		{Name: "color", Path: "hue", Default: "magenta"},
	}}
	out, err := renderFields(f, parsed, map[string]any{"data": parsed}, "timeline", 0)
	require.NoError(t, err)
	assert.JSONEq(t, `{"events":[{"label":"2024-03-01 milestone","date":"2024-03-01","color":"magenta"}]}`, out)
}

func TestTimeline_DerivedFieldsFromMatchingKeys(t *testing.T) {
	echoTimeline(t)
	// No <fields> declared: keys named label/date are derived and map directly.
	parsed := []any{map[string]any{"label": "Kickoff", "date": "2024-01-08"}}
	out, err := renderFields(&Fields{}, parsed, map[string]any{"data": parsed}, "timeline", 0)
	require.NoError(t, err)
	assert.JSONEq(t, `{"events":[{"label":"Kickoff","date":"2024-01-08"}]}`, out)
}

func TestIntegration_AsTimeline(t *testing.T) {
	echoTimeline(t)
	events := `[{"name":"Kickoff","when":"2024-01-08"},{"name":"Launch","when":"2024-06-30"}]`
	cfg := &Config{
		Name: "t",
		Commands: []Command{{
			Name:    "roadmap",
			Command: &Cmd{Shell: true, Template: "printf '%s' '" + events + "'"},
			Fields: fieldsBlocks(&Fields{List: []Field{
				{Name: "label", Path: "name"},
				{Name: "date", Path: "when"},
			}}),
		}},
	}
	code, out := execCmd(t, cfg, "roadmap", "--as", "timeline")
	require.Equal(t, 0, code)
	assert.JSONEq(t, `{"events":[{"label":"Kickoff","date":"2024-01-08"},{"label":"Launch","date":"2024-06-30"}]}`, out)
}
