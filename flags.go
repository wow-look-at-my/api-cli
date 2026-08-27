package main

// Declared <arg> and <flag> elements, on both sides of a run: registering them
// on the cobra command, and collecting the invocation's values back out into
// the .arg / .flag halves of the template context.

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
	"github.com/wow-look-at-my/go-containers/set"
)

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
	seen := set.New[pair]()
	for _, f := range flags {
		for _, peer := range f.Conflicts {
			a, b := f.Name, peer
			if a > b {
				a, b = b, a
			}
			p := pair{a, b}
			if seen.Contains(p) {
				continue
			}
			seen.Add(p)
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

// passthroughParse extracts known flags from a raw arg list. Everything not
// recognized as a known flag (or its value) goes into rest. Flags are matched
// with either one or two leading dashes (to support tools like CUDA's cicc
// that use single-dash long flags). A flag's short alias is also recognized.
// A bare "--" in the args is forwarded into rest verbatim (along with
// everything after it), since it may be meaningful to the wrapped command.
func passthroughParse(rawArgs []string, flags []Flag) (flagMap map[string]any, rest []string) {
	type flagDef struct {
		name string
		typ  string
	}
	lookup := map[string]*flagDef{}
	for i := range flags {
		f := &flags[i]
		typ := f.Type
		if typ == "" {
			typ = "string"
		}
		def := &flagDef{name: f.Name, typ: typ}
		lookup[f.Name] = def
		if f.Short != "" {
			lookup[f.Short] = def
		}
	}

	flagMap = make(map[string]any, len(flags))
	for _, f := range flags {
		typ := f.Type
		if typ == "" {
			typ = "string"
		}
		switch typ {
		case "string":
			def, _ := f.Default.(string)
			flagMap[f.Name] = def
		case "bool":
			def, _ := f.Default.(bool)
			flagMap[f.Name] = def
		case "int":
			var def int
			switch v := f.Default.(type) {
			case float64:
				def = int(v)
			case int:
				def = v
			}
			flagMap[f.Name] = def
		case "string-slice":
			flagMap[f.Name] = []string{}
		}
	}

	rest = make([]string, 0, len(rawArgs))
	for i := 0; i < len(rawArgs); i++ {
		arg := rawArgs[i]

		if arg == "--" {
			rest = append(rest, rawArgs[i:]...)
			break
		}

		if !strings.HasPrefix(arg, "-") {
			rest = append(rest, arg)
			continue
		}

		stripped := strings.TrimLeft(arg, "-")
		name := stripped
		value := ""
		hasEquals := false
		if idx := strings.IndexByte(stripped, '='); idx >= 0 {
			name = stripped[:idx]
			value = stripped[idx+1:]
			hasEquals = true
		}

		def, known := lookup[name]
		if !known {
			rest = append(rest, arg)
			continue
		}

		switch def.typ {
		case "bool":
			if hasEquals {
				flagMap[def.name] = value == "true" || value == "1" || value == "yes"
			} else {
				flagMap[def.name] = true
			}
		case "int":
			var raw string
			if hasEquals {
				raw = value
			} else if i+1 < len(rawArgs) {
				i++
				raw = rawArgs[i]
			}
			n, _ := strconv.Atoi(raw)
			flagMap[def.name] = n
		case "string-slice":
			var raw string
			if hasEquals {
				raw = value
			} else if i+1 < len(rawArgs) {
				i++
				raw = rawArgs[i]
			}
			flagMap[def.name] = append(flagMap[def.name].([]string), raw)
		default:
			if hasEquals {
				flagMap[def.name] = value
			} else if i+1 < len(rawArgs) {
				i++
				flagMap[def.name] = rawArgs[i]
			}
		}
	}
	return flagMap, rest
}
