package main

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
)

// exitCode is set by a leaf's RunE to the exit status of the child process.
// main reads it after rootCmd.Execute() returns.
var exitCode int

// buildCommand turns a Command node into a cobra.Command, wiring up args,
// flags, and subcommands. inheritedVars flow down the tree (child overrides
// parent on key collision). inheritedCmd is the closest-ancestor command
// template; the node's own command, if set, overrides it for this subtree.
func buildCommand(node Command, inheritedVars map[string]any, inheritedCmd *Cmd) *cobra.Command {
	useStr := node.Name
	requiredArgs := 0
	hasVariadic := false
	for _, a := range node.Args {
		token := a.Name
		if a.Variadic {
			token += "..."
			hasVariadic = true
		}
		if a.Required {
			useStr += " <" + token + ">"
			requiredArgs++
		} else {
			useStr += " [" + token + "]"
		}
	}

	cmd := &cobra.Command{
		Use:          useStr,
		Short:        node.Description,
		SilenceUsage: true,
	}

	if total := len(node.Args); total > 0 {
		switch {
		case hasVariadic:
			cmd.Args = cobra.MinimumNArgs(requiredArgs)
		case requiredArgs == total:
			cmd.Args = cobra.ExactArgs(total)
		default:
			cmd.Args = cobra.RangeArgs(requiredArgs, total)
		}
	}

	for _, f := range node.Flags {
		registerFlag(cmd, f)
	}
	registerConflicts(cmd, node.Flags)

	// Resolve effective vars and command for this subtree.
	effectiveVars := mergeVars(inheritedVars, node.Vars)
	effectiveCmd := inheritedCmd
	if node.Command.Defined() {
		effectiveCmd = node.Command
	}

	// Leaves (no subcommands) execute.
	if len(node.Commands) == 0 {
		nodeCopy := node
		leafVars := effectiveVars
		leafCmd := effectiveCmd
		cmd.RunE = func(c *cobra.Command, args []string) error {
			return runLeaf(c, nodeCopy, args, leafVars, leafCmd)
		}
	}

	for _, child := range node.Commands {
		cmd.AddCommand(buildCommand(child, effectiveVars, effectiveCmd))
	}

	return cmd
}

func registerFlag(cmd *cobra.Command, f Flag) {
	typ := f.Type
	if typ == "" {
		typ = "string"
	}
	switch typ {
	case "string":
		def, _ := f.Default.(string)
		cmd.Flags().StringP(f.Name, f.Short, def, f.Description)
	case "bool":
		def, _ := f.Default.(bool)
		cmd.Flags().BoolP(f.Name, f.Short, def, f.Description)
		// Default-true bool: register a hidden --no-NAME companion that flips
		// the flag back to false. Lets users say `--no-verbose` instead of the
		// awkward `--verbose=false`.
		if def {
			neg := "no-" + f.Name
			cmd.Flags().Bool(neg, false, "Disable --"+f.Name+".")
			_ = cmd.Flags().MarkHidden(neg)
		}
	case "int":
		var def int
		switch v := f.Default.(type) {
		case float64:
			def = int(v) // JSON numbers decode as float64 into any
		case int:
			def = v
		}
		cmd.Flags().IntP(f.Name, f.Short, def, f.Description)
	case "string-slice":
		var def []string
		if raw, ok := f.Default.([]any); ok {
			for _, x := range raw {
				if s, ok := x.(string); ok {
					def = append(def, s)
				}
			}
		}
		// StringArray (not StringSlice) so commas in values are preserved.
		cmd.Flags().StringArrayP(f.Name, f.Short, def, f.Description)
	}
	if f.Required {
		_ = cmd.MarkFlagRequired(f.Name)
	}
}

// registerConflicts wires per-flag `conflicts` lists into cobra's mutual
// exclusion machinery. Each unordered pair is registered once.
func registerConflicts(cmd *cobra.Command, flags []Flag) {
	type pair struct{ a, b string }
	seen := map[pair]bool{}
	for _, f := range flags {
		for _, peer := range f.Conflicts {
			a, b := f.Name, peer
			if a > b {
				a, b = b, a
			}
			p := pair{a, b}
			if seen[p] {
				continue
			}
			seen[p] = true
			cmd.MarkFlagsMutuallyExclusive(a, b)
		}
	}
}

// gatherArgs builds the .arg sub-map by converting positional args to their
// declared types. A variadic arg (always last) collects all remaining values
// into a typed slice; an unsupplied optional variadic arg yields an empty
// slice so templates can range over it without nil checks.
func gatherArgs(node Command, args []string) (map[string]any, error) {
	out := make(map[string]any, len(node.Args))
	for i, a := range node.Args {
		if a.Variadic {
			rest := []string{}
			if i < len(args) {
				rest = args[i:]
			}
			if a.Type == "int" {
				ints := make([]int, len(rest))
				for j, v := range rest {
					n, err := strconv.Atoi(v)
					if err != nil {
						return nil, fmt.Errorf("arg %q[%d]: %w", a.Name, j, err)
					}
					ints[j] = n
				}
				out[a.Name] = ints
			} else {
				out[a.Name] = rest
			}
			break
		}
		if i >= len(args) {
			break
		}
		v := args[i]
		if a.Type == "int" {
			n, err := strconv.Atoi(v)
			if err != nil {
				return nil, fmt.Errorf("arg %q: %w", a.Name, err)
			}
			out[a.Name] = n
			continue
		}
		out[a.Name] = v
	}
	return out, nil
}

// gatherFlags builds the .flag sub-map from the cobra-parsed flag set.
//
// Two non-trivial cases:
//  1. Bool flags with default=true register a hidden --no-NAME companion;
//     when set, it flips the value to false.
//  2. String flags whose configured default is itself a template (contains
//     `{{`) are rendered against the current context — but only when the
//     user did not explicitly set the flag.
func gatherFlags(cmd *cobra.Command, node Command, data any) (map[string]any, error) {
	out := make(map[string]any, len(node.Flags))
	for _, f := range node.Flags {
		typ := f.Type
		if typ == "" {
			typ = "string"
		}
		switch typ {
		case "string":
			v, _ := cmd.Flags().GetString(f.Name)
			if !cmd.Flags().Changed(f.Name) {
				if def, ok := f.Default.(string); ok && strings.Contains(def, "{{") {
					rendered, err := renderString(def, data)
					if err != nil {
						return nil, fmt.Errorf("flag %q default: %w", f.Name, err)
					}
					v = rendered
				}
			}
			out[f.Name] = v
		case "bool":
			v, _ := cmd.Flags().GetBool(f.Name)
			neg := "no-" + f.Name
			if cmd.Flags().Lookup(neg) != nil && cmd.Flags().Changed(neg) {
				if no, _ := cmd.Flags().GetBool(neg); no {
					v = false
				}
			}
			out[f.Name] = v
		case "int":
			v, _ := cmd.Flags().GetInt(f.Name)
			out[f.Name] = v
		case "string-slice":
			v, _ := cmd.Flags().GetStringArray(f.Name)
			out[f.Name] = v
		}
	}
	return out, nil
}

// runLeaf is the per-invocation body for every leaf.
//
// Stages:
//  1. Assemble args, flags, env — the base template context.
//  2. Render the merged vars against the base context to produce .var.
//  3. Execute each step in order, capturing its stdout into .result.<name>.
//     Each step's entry template is rendered against the current context
//     (including .result.* from prior steps) before the step runs.
//  4. Render the leaf's own entry against the full context (including
//     .result.*) to produce .entry.
//  5. Render the effective command template against the full context and
//     execute it, streaming output to the user.
//  6. If more than one command was executed and --quiet is not set, print
//     the execution count to stderr.
func runLeaf(c *cobra.Command, node Command, args []string, vars map[string]any, cmdTmpl *Cmd) error {
	argMap, err := gatherArgs(node, args)
	if err != nil {
		return err
	}

	// Templated flag defaults render against {arg, env, var} — not other
	// flags — so build vars from that partial context first, then resolve
	// flags, then complete the data map.
	envM := envMap()
	preFlagData := map[string]any{
		"arg":  argMap,
		"flag": map[string]any{},
		"env":  envM,
	}
	renderedVars, err := renderVars(vars, preFlagData)
	if err != nil {
		return fmt.Errorf("render vars: %w", err)
	}
	preFlagData["var"] = renderedVars

	flagMap, err := gatherFlags(c, node, preFlagData)
	if err != nil {
		return err
	}

	data := map[string]any{
		"arg":  argMap,
		"flag": flagMap,
		"env":  envM,
		"var":  renderedVars,
	}

	for i, p := range node.Preconditions {
		msg, perr := renderString(p, data)
		if perr != nil {
			return fmt.Errorf("precondition[%d]: %w", i, perr)
		}
		msg = strings.TrimSpace(msg)
		if msg != "" {
			fmt.Fprintln(execStderr, "error:", msg)
			exitCode = 1
			return nil
		}
	}

	resultMap := map[string]any{}
	data["result"] = resultMap

	executions := 0

	for _, step := range node.Steps {
		stepCmd := cmdTmpl
		if step.Command.Defined() {
			stepCmd = step.Command
		}
		if !stepCmd.Defined() {
			return fmt.Errorf("step %q: no command available", step.Name)
		}

		stepEntry, err := renderEntry(step.Entry, data)
		if err != nil {
			return fmt.Errorf("step %q: render entry: %w", step.Name, err)
		}
		if stepEntry == nil {
			stepEntry = map[string]any{}
		}
		data["entry"] = stepEntry

		out, code := captureExec(stepCmd, data)
		executions++
		if code != 0 {
			exitCode = code
			reportExecutions(c, executions)
			return nil
		}
		resultMap[step.Name] = parseResult(out)
	}

	entry, err := renderEntry(node.Entry, data)
	if err != nil {
		return fmt.Errorf("render entry: %w", err)
	}
	if entry == nil {
		entry = map[string]any{}
	}
	data["entry"] = entry

	if !cmdTmpl.Defined() {
		return fmt.Errorf("no command available to run")
	}

	exitCode = doExec(cmdTmpl, data)
	executions++
	reportExecutions(c, executions)
	return nil
}

// reportExecutions prints the number of commands run to stderr when n > 1
// and --quiet is not set.
func reportExecutions(c *cobra.Command, n int) {
	if n <= 1 {
		return
	}
	quiet, _ := c.Root().PersistentFlags().GetBool("quiet")
	if !quiet {
		fmt.Fprintf(execStderr, "%d executions\n", n)
	}
}

// renderVars runs each string leaf of the merged vars map through the
// template engine with the given context. Non-string values pass through.
func renderVars(vars map[string]any, data any) (map[string]any, error) {
	if len(vars) == 0 {
		return map[string]any{}, nil
	}
	// Round-trip via JSON so we can reuse the entry walker. This preserves
	// the structure exactly and handles nested maps/slices of strings.
	raw, err := json.Marshal(vars)
	if err != nil {
		return nil, err
	}
	v, err := renderEntry(raw, data)
	if err != nil {
		return nil, err
	}
	if v == nil {
		return map[string]any{}, nil
	}
	m, ok := v.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("vars did not render to a map: got %T", v)
	}
	return m, nil
}
