package fields

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"github.com/wow-look-at-my/go-containers/set"
)

// timelineBinary is the executable that draws the timeline sink.
const timelineBinary = "ascii-timeline"

// timelineInstallHint names the package that provides timelineBinary.
const timelineInstallHint = "install the package " + timelineBinary + " (apt install " + timelineBinary + " or brew install " + timelineBinary + ")"

// timelineEventKeys are the field names the timeline sink reads off a record.
var timelineEventKeys = set.Of("label", "date", "start", "end", "description", "color")

// renderTimelineSink draws the records as a horizontal ASCII timeline. It pipes
// a JSON document to the ascii-timeline executable on PATH and returns its
// stdout. A record becomes an event, and a record that resolves no placement on
// the axis is skipped. Color follows the context's .tty and the NO_COLOR
// variable, width follows .width. Values come from cellValue, so default=,
// truncate=, firstline= and expr= all apply. A missing executable, a failed
// run, or empty output is an error.
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
	return runTimelineBinary(doc, timelineArgs(ctx))
}

// timelineArgs builds the executable's flags from the format context.
func timelineArgs(ctx map[string]any) []string {
	var args []string
	if w := timelineWidth(ctx); w > 0 {
		args = append(args, "--width", strconv.Itoa(w))
	}
	if timelineNoColor(ctx) {
		args = append(args, "--no-color")
	}
	return args
}

// runTimelineBinary runs the executable with doc on stdin and returns stdout.
func runTimelineBinary(doc []byte, args []string) (string, error) {
	path, err := exec.LookPath(timelineBinary)
	if err != nil {
		return "", fmt.Errorf("timeline: the %s executable is not on PATH; %s", timelineBinary, timelineInstallHint)
	}
	var stdout, stderr bytes.Buffer
	cmd := exec.Command(path, args...)
	cmd.Stdin = bytes.NewReader(doc)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return "", fmt.Errorf("timeline: %s exited with status %d: %s (%s)", timelineBinary, exit.ExitCode(), strings.TrimSpace(stderr.String()), timelineInstallHint)
		}
		return "", fmt.Errorf("timeline: cannot run %s: %w (%s)", path, err, timelineInstallHint)
	}
	if strings.TrimSpace(stdout.String()) == "" {
		return "", fmt.Errorf("timeline: %s printed nothing (%s)", timelineBinary, timelineInstallHint)
	}
	return stdout.String(), nil
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
// is unset, and the executable falls back to its own default.
func timelineWidth(ctx map[string]any) int {
	w, _ := ctx["width"].(int)
	return w
}
