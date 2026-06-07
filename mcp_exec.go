package main

import (
	"bytes"
	"fmt"
	"io"
	"math"
	"os/exec"
	"strconv"
	"strings"
)

// mcpExecLeaf runs a leaf command and returns (output, isError).
// Confirmation prompts are skipped — MCP callers cannot respond interactively.
func mcpExecLeaf(leaf *mcpLeaf, arguments map[string]any) (string, bool) {
	argMap, err := mcpGatherArgs(leaf.node, arguments)
	if err != nil {
		return "error: " + err.Error(), true
	}

	envM := envMap()
	preFlagData := map[string]any{
		"arg":  argMap,
		"flag": map[string]any{},
		"env":  envM,
	}
	renderedVars, err := renderVars(leaf.vars, preFlagData)
	if err != nil {
		return fmt.Sprintf("error: render vars: %v", err), true
	}
	preFlagData["var"] = renderedVars

	flagMap, err := mcpGatherFlags(leaf.node, arguments, preFlagData)
	if err != nil {
		return "error: " + err.Error(), true
	}

	data := map[string]any{
		"arg":  argMap,
		"flag": flagMap,
		"env":  envM,
		"var":  renderedVars,
	}

	for i, p := range leaf.node.Preconditions {
		msg, perr := renderString(p, data)
		if perr != nil {
			return fmt.Sprintf("precondition[%d]: %v", i, perr), true
		}
		if msg = strings.TrimSpace(msg); msg != "" {
			return "error: " + msg, true
		}
	}

	resultMap := map[string]any{}
	data["result"] = resultMap

	for _, step := range leaf.node.Steps {
		stepCmd := leaf.cmdTmpl
		if step.Command.Defined() {
			stepCmd = step.Command
		}
		if !stepCmd.Defined() {
			return fmt.Sprintf("step %q: no command available", step.Name), true
		}

		stepEntry, err := renderEntry(step.Entry, data)
		if err != nil {
			return fmt.Sprintf("step %q: render entry: %v", step.Name, err), true
		}
		if stepEntry == nil {
			stepEntry = map[string]any{}
		}
		data["entry"] = stepEntry

		stepCwd := leaf.cwdTmpl
		if step.Cwd != "" {
			stepCwd = step.Cwd
		}
		renderedCwd, err := renderCwd(stepCwd, data)
		if err != nil {
			return fmt.Sprintf("step %q: render cwd: %v", step.Name, err), true
		}

		stepStdin := leaf.stdinTmpl
		if step.Stdin != "" {
			stepStdin = step.Stdin
		}
		renderedStdin, err := renderStdin(stepStdin, data)
		if err != nil {
			return fmt.Sprintf("step %q: render stdin: %v", step.Name, err), true
		}

		var errBuf bytes.Buffer
		out, code := mcpCapture(stepCmd, renderedCwd, renderedStdin, data, &errBuf)
		if code != 0 {
			return mcpCombine(out, errBuf.String()), true
		}
		resultMap[step.Name] = parseResult(out)
	}

	entry, err := renderEntry(leaf.node.Entry, data)
	if err != nil {
		return fmt.Sprintf("render entry: %v", err), true
	}
	if entry == nil {
		entry = map[string]any{}
	}
	data["entry"] = entry

	leafCwd, err := renderCwd(leaf.cwdTmpl, data)
	if err != nil {
		return fmt.Sprintf("render cwd: %v", err), true
	}
	leafStdin, err := renderStdin(leaf.stdinTmpl, data)
	if err != nil {
		return fmt.Sprintf("render stdin: %v", err), true
	}

	var errBuf bytes.Buffer
	var out string
	var code int
	if leaf.request.Defined() {
		out, code = runRequest(leaf.request, data, &errBuf)
	} else {
		out, code = mcpCapture(leaf.cmdTmpl, leafCwd, leafStdin, data, &errBuf)
	}
	if code != 0 {
		return mcpCombine(out, errBuf.String()), true
	}

	// The <fields> auto-formatter takes precedence. MCP behaves like
	// --format=always: .tty is true, .width is 80, no width-based dropping.
	if leaf.node.Fields != nil {
		parsed := parseInput(out, "json")
		ctx := formatContext(parsed, data, true, 80)
		rendered, ferr := renderFields(leaf.node.Fields, parsed, ctx, "", 0)
		if ferr != nil {
			return "error: " + ferr.Error(), true
		}
		return rendered, false
	}

	if formatted, ok := mcpFormat(leaf, out, data); ok {
		return formatted, false
	}
	return out, false
}

func mcpCombine(stdout, stderr string) string {
	stdout = strings.TrimSpace(stdout)
	stderr = strings.TrimSpace(stderr)
	switch {
	case stdout != "" && stderr != "":
		return stdout + "\n" + stderr
	case stdout != "":
		return stdout
	default:
		return stderr
	}
}

// mcpCapture runs a command capturing stdout; stderr goes to errBuf.
// Child stdin is always explicit — never inherited from the process — because
// in stdio MCP mode the process stdin is the protocol channel.
func mcpCapture(c *Cmd, cwd, stdin string, data any, errBuf io.Writer) (string, int) {
	if !c.Defined() {
		fmt.Fprintln(errBuf, "error: command is empty")
		return "", 1
	}
	cmd, err := buildExecCmd(c, data)
	if err != nil {
		fmt.Fprintln(errBuf, "error:", err)
		return "", 1
	}
	cmd.Dir = cwd
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	} else {
		cmd.Stdin = strings.NewReader("")
	}
	var outBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = errBuf
	if err := cmd.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return outBuf.String(), exitErr.ExitCode()
		}
		fmt.Fprintln(errBuf, "error:", err)
		return outBuf.String(), 127
	}
	return outBuf.String(), 0
}

// mcpGatherArgs converts the JSON-decoded arguments map to a typed arg map.
func mcpGatherArgs(node Command, arguments map[string]any) (map[string]any, error) {
	out := make(map[string]any, len(node.Args))
	for _, a := range node.Args {
		val, provided := arguments[a.Name]
		if a.Variadic {
			if !provided {
				if a.Type == "int" {
					out[a.Name] = []int{}
				} else {
					out[a.Name] = []string{}
				}
				continue
			}
			arr, ok := val.([]any)
			if !ok {
				return nil, fmt.Errorf("arg %q: expected array", a.Name)
			}
			if a.Type == "int" {
				ints := make([]int, len(arr))
				for i, v := range arr {
					switch n := v.(type) {
					case float64:
						if n != math.Trunc(n) {
							return nil, fmt.Errorf("arg %q[%d]: expected integer, got %v", a.Name, i, n)
						}
						ints[i] = int(n)
					default:
						parsed, err := strconv.Atoi(fmt.Sprintf("%v", v))
						if err != nil {
							return nil, fmt.Errorf("arg %q[%d]: expected integer, got %v", a.Name, i, v)
						}
						ints[i] = parsed
					}
				}
				out[a.Name] = ints
			} else {
				strs := make([]string, len(arr))
				for i, v := range arr {
					strs[i] = fmt.Sprintf("%v", v)
				}
				out[a.Name] = strs
			}
			continue
		}
		if !provided {
			continue
		}
		if a.Type == "int" {
			switch v := val.(type) {
			case float64:
				if v != math.Trunc(v) {
					return nil, fmt.Errorf("arg %q: expected integer, got %v", a.Name, v)
				}
				out[a.Name] = int(v)
			default:
				n, err := strconv.Atoi(fmt.Sprintf("%v", v))
				if err != nil {
					return nil, fmt.Errorf("arg %q: expected integer", a.Name)
				}
				out[a.Name] = n
			}
		} else {
			out[a.Name] = fmt.Sprintf("%v", val)
		}
	}
	return out, nil
}

// mcpGatherFlags converts the JSON-decoded arguments map to a typed flag map.
// preFlagData is used to evaluate templated string defaults.
func mcpGatherFlags(node Command, arguments map[string]any, preFlagData any) (map[string]any, error) {
	out := make(map[string]any, len(node.Flags))
	for _, f := range node.Flags {
		typ := f.Type
		if typ == "" {
			typ = "string"
		}
		val, provided := arguments[f.Name]
		switch typ {
		case "bool":
			if provided {
				b, ok := val.(bool)
				if !ok {
					return nil, fmt.Errorf("flag %q: expected boolean", f.Name)
				}
				out[f.Name] = b
			} else {
				def, _ := f.Default.(bool)
				out[f.Name] = def
			}
		case "int":
			if provided {
				switch v := val.(type) {
				case float64:
					if v != math.Trunc(v) {
						return nil, fmt.Errorf("flag %q: expected integer, got %v", f.Name, v)
					}
					out[f.Name] = int(v)
				default:
					n, err := strconv.Atoi(fmt.Sprintf("%v", v))
					if err != nil {
						return nil, fmt.Errorf("flag %q: expected integer", f.Name)
					}
					out[f.Name] = n
				}
			} else {
				switch v := f.Default.(type) {
				case float64:
					out[f.Name] = int(v)
				case int:
					out[f.Name] = v
				default:
					out[f.Name] = 0
				}
			}
		case "string-slice":
			if provided {
				arr, ok := val.([]any)
				if !ok {
					return nil, fmt.Errorf("flag %q: expected array", f.Name)
				}
				strs := make([]string, len(arr))
				for i, v := range arr {
					strs[i] = fmt.Sprintf("%v", v)
				}
				out[f.Name] = strs
			} else {
				if raw, ok := f.Default.([]any); ok {
					strs := make([]string, len(raw))
					for i, v := range raw {
						if s, ok := v.(string); ok {
							strs[i] = s
						}
					}
					out[f.Name] = strs
				} else {
					out[f.Name] = []string{}
				}
			}
		default: // string
			if provided {
				out[f.Name] = fmt.Sprintf("%v", val)
			} else {
				def, _ := f.Default.(string)
				if strings.Contains(def, "{{") {
					rendered, err := renderString(def, preFlagData)
					if err != nil {
						return nil, fmt.Errorf("flag %q default: %w", f.Name, err)
					}
					out[f.Name] = rendered
				} else {
					out[f.Name] = def
				}
			}
		}
	}
	return out, nil
}

// mcpFormat applies the format system to raw command output in MCP mode.
// Behaves like --format=always: .tty is true so the default when predicate
// passes, but an author's explicit when: "false" is still respected.
func mcpFormat(leaf *mcpLeaf, raw string, data map[string]any) (string, bool) {
	effFmt := resolveFormat(leaf.formatRef, leaf.formats)
	if effFmt == nil {
		return "", false
	}

	cache := map[predicateKey]bool{}
	preCtx := formatContext(nil, data, true, 80)
	authorOK, err := renderPredicate(effFmt.When, preCtx, cache)
	if err != nil || !authorOK {
		return "", false
	}

	parsed := parseInput(raw, effFmt.Input)
	ctx := formatContext(parsed, data, true, 80)

	view, err := selectView(effFmt.Views, ctx, "", cache)
	if err != nil {
		return "", false
	}

	rendered, err := renderString(view.Template, ctx)
	if err != nil {
		return "", false
	}
	return rendered, true
}
