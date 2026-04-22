package main

import (
	"encoding/json"
	"fmt"
	"strconv"

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
	for _, a := range node.Args {
		if a.Required {
			useStr += " <" + a.Name + ">"
			requiredArgs++
		} else {
			useStr += " [" + a.Name + "]"
		}
	}

	cmd := &cobra.Command{
		Use:          useStr,
		Short:        node.Description,
		SilenceUsage: true,
	}

	if total := len(node.Args); total > 0 {
		if requiredArgs == total {
			cmd.Args = cobra.ExactArgs(total)
		} else {
			cmd.Args = cobra.RangeArgs(requiredArgs, total)
		}
	}

	for _, f := range node.Flags {
		registerFlag(cmd, f)
	}

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

// gatherArgs builds the .arg sub-map by converting positional args to their
// declared types.
func gatherArgs(node Command, args []string) (map[string]any, error) {
	out := make(map[string]any, len(node.Args))
	for i, a := range node.Args {
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
func gatherFlags(cmd *cobra.Command, node Command) map[string]any {
	out := make(map[string]any, len(node.Flags))
	for _, f := range node.Flags {
		typ := f.Type
		if typ == "" {
			typ = "string"
		}
		switch typ {
		case "string":
			v, _ := cmd.Flags().GetString(f.Name)
			out[f.Name] = v
		case "bool":
			v, _ := cmd.Flags().GetBool(f.Name)
			out[f.Name] = v
		case "int":
			v, _ := cmd.Flags().GetInt(f.Name)
			out[f.Name] = v
		case "string-slice":
			v, _ := cmd.Flags().GetStringArray(f.Name)
			out[f.Name] = v
		}
	}
	return out
}

// runLeaf is the per-invocation body for every leaf.
//
// Stages:
//  1. Assemble args, flags, env — the base template context.
//  2. Render the merged vars against the base context to produce .var.
//  3. Render the entry against the context (now including .var) to produce
//     .entry.
//  4. Render the effective command template against the full context and
//     execute it.
func runLeaf(c *cobra.Command, node Command, args []string, vars map[string]any, cmdTmpl *Cmd) error {
	argMap, err := gatherArgs(node, args)
	if err != nil {
		return err
	}
	flagMap := gatherFlags(c, node)

	data := map[string]any{
		"arg":  argMap,
		"flag": flagMap,
		"env":  envMap(),
	}

	renderedVars, err := renderVars(vars, data)
	if err != nil {
		return fmt.Errorf("render vars: %w", err)
	}
	data["var"] = renderedVars

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
	return nil
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
