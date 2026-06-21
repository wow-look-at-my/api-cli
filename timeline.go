package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/wow-look-at-my/ascii-timeline/timeline"
)

// timelineEventKeys are the field names the timeline sink understands. A record
// becomes one timeline event by reading the fields with these names; any other
// field is ignored. "date" makes a point event; "start"+"end" make a duration
// event. The remaining keys are optional annotations.
var timelineEventKeys = map[string]bool{
	"label":       true,
	"date":        true,
	"start":       true,
	"end":         true,
	"description": true,
	"color":       true,
}

// renderTimelineSink represents the records as a horizontal ASCII timeline,
// rendered by the ascii-timeline library. Each record is one event; the field
// names label/date/start/end/description/color select the event properties
// (show_in still gates visibility). Records that resolve no placement on the
// axis (no "date" and not both "start" and "end") are skipped.
//
// Color follows the format context's .tty (and the NO_COLOR env var); width
// follows .width, where 0 means the library default. The event values reuse
// cellValue, so default=/truncate=/firstline=/expr= all apply as usual.
func renderTimelineSink(recs []record, fields []Field, ctx map[string]any) (string, error) {
	inc := includedFields(fields, "timeline")

	events := make([]map[string]string, 0, len(recs))
	for _, r := range recs {
		ev := map[string]string{}
		for _, fld := range inc {
			if !timelineEventKeys[fld.Name] {
				continue
			}
			v, err := cellValue(fld, r, ctx)
			if err != nil {
				return "", err
			}
			if strings.TrimSpace(v) == "" {
				continue
			}
			ev[fld.Name] = v
		}
		// Skip records that can't be placed on the axis.
		if ev["date"] == "" && (ev["start"] == "" || ev["end"] == "") {
			continue
		}
		events = append(events, ev)
	}

	doc, err := json.Marshal(map[string]any{"events": events})
	if err != nil {
		return "", err
	}
	tl, err := timeline.ParseBytes(doc)
	if err != nil {
		return "", fmt.Errorf("timeline: %w", err)
	}
	tl.NoColor = timelineNoColor(ctx)
	if w := timelineWidth(ctx); w > 0 {
		tl.Width = w
	}
	return tl.String(), nil
}

// timelineNoColor reports whether the timeline should drop ANSI color: when
// stdout is not a TTY, or NO_COLOR is set (any non-empty value, NO_COLOR-style).
func timelineNoColor(ctx map[string]any) bool {
	if os.Getenv("NO_COLOR") != "" {
		return true
	}
	tty, _ := ctx["tty"].(bool)
	return !tty
}

// timelineWidth returns the axis width from the format context's .width
// (the terminal width, or 0 when not a TTY — the library then uses its default).
func timelineWidth(ctx map[string]any) int {
	w, _ := ctx["width"].(int)
	return w
}
