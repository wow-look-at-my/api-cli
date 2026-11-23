// debug.go holds debugContext.

package main

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"sort"
	"strings"
)

var (
	verboseMode bool
	debugMode   bool
)

func logVerbose(format string, args ...any) {
	if !verboseMode {
		return
	}
	fmt.Fprintf(execStderr, "[verbose] "+format+"\n", args...)
}

func logDebug(format string, args ...any) {
	if !debugMode {
		return
	}
	fmt.Fprintf(execStderr, "[debug]   "+format+"\n", args...)
}

func logDebugBlock(label, content string) {
	if !debugMode {
		return
	}
	if content == "" {
		fmt.Fprintf(execStderr, "[debug]   %s: (empty)\n", label)
		return
	}
	lines := strings.Split(strings.TrimRight(content, "\n"), "\n")
	if len(lines) == 1 {
		fmt.Fprintf(execStderr, "[debug]   %s: %s\n", label, lines[0])
		return
	}
	fmt.Fprintf(execStderr, "[debug]   %s:\n", label)
	for _, line := range lines {
		fmt.Fprintf(execStderr, "[debug]     %s\n", line)
	}
}

func cmdToString(cmd *exec.Cmd) string {
	return strings.Join(cmd.Args, " ")
}

func truncate(s string, maxLen int) string {
	s = strings.TrimSpace(s)
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

// debugContext is a copy of a data context that is safe to print. .env shows
// the variable names only, because the environment holds unrelated secrets. A
// value in .arg, .flag or .var whose name looks secret shows as [redacted].
func debugContext(data map[string]any) map[string]any {
	out := make(map[string]any, len(data))
	for k, v := range data {
		out[k] = v
	}
	if env, ok := data["env"].(map[string]string); ok {
		names := make([]string, 0, len(env))
		for name := range env {
			names = append(names, name)
		}
		sort.Strings(names)
		out["env"] = names
	}
	for _, k := range []string{"arg", "flag", "var"} {
		m, ok := data[k].(map[string]any)
		if !ok {
			continue
		}
		cp := make(map[string]any, len(m))
		for name, v := range m {
			if secretInput.MatchString(name) {
				v = "[redacted]"
			}
			cp[name] = v
		}
		out[k] = cp
	}
	return out
}

func jsonCompact(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return "<marshal error>"
	}
	return string(b)
}
