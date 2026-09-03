package fields

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/wow-look-at-my/ascii-timeline/timeline"
	"github.com/wow-look-at-my/go-containers/set"
)

// timelineEventKeys are the field names the timeline sink reads off a record.
var timelineEventKeys = set.Of("label", "date", "start", "end", "description", "color")

// renderTimelineSink draws the records as a horizontal ASCII timeline through
// the ascii-timeline library. A record becomes an event, and a record that
// resolves no placement on the axis is skipped. Color follows the context's
// .tty and the NO_COLOR variable, width follows .width. Values come from
// cellValue, so default=, truncate=, firstline= and expr= all apply.
func renderTimelineSink(rnd Renderer, recs []record, fields []Field, ctx map[string]any) (string, error) {
	inc := includedFields(fields, "timeline")

	events := make([]map[string]string, 0, len(recs))
	for _, r := range recs {
		ev := map[string]string{}
		for _, fld := range inc {
			if !timelineEventKeys.Contains(fld.Name) {
				continue
			}
			v, err := cellValue(rnd, fld, r, ctx)
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

// timelineNoColor drops color off a terminal, and whenever NO_COLOR is set.
func timelineNoColor(ctx map[string]any) bool {
	if os.Getenv("NO_COLOR") != "" {
		return true
	}
	tty, _ := ctx["tty"].(bool)
	return !tty
}

// timelineWidth reads the axis width from the format context. Off a terminal it
// is unset, and the library falls back to its own default.
func timelineWidth(ctx map[string]any) int {
	w, _ := ctx["width"].(int)
	return w
}
