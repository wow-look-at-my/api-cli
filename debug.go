package main

import (
	"encoding/json"
	"fmt"
	"os/exec"
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

func jsonCompact(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return "<marshal error>"
	}
	return string(b)
}
