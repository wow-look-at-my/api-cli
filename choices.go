// choices= on an arg or a flag: the closed set of values it takes.

package main

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
	"github.com/wow-look-at-my/go-containers/set"
)

// validateChoices is the load-time check on one input's choices=.
func validateChoices(choices []string, typ, where string) error {
	if len(choices) == 0 {
		return nil
	}
	if typ == "bool" {
		return fmt.Errorf("%s: a bool takes no choices=", where)
	}
	seen := set.New[string]()
	for _, c := range choices {
		if seen.Contains(c) {
			return fmt.Errorf("%s: choice %q is listed twice", where, c)
		}
		seen.Add(c)
		if typ == "int" {
			if _, err := strconv.Atoi(c); err != nil {
				return fmt.Errorf("%s: choice %q is not an integer", where, c)
			}
		}
	}
	return nil
}

// validateFlagDefaultChoice rejects a default the choices exclude, since every run that omits the flag would then fail.
func validateFlagDefaultChoice(f Flag, where string) error {
	if len(f.Choices) == 0 || f.Default == nil {
		return nil
	}
	for _, v := range choiceValues(f.Default) {
		if v == "" || strings.Contains(v, "{{") {
			continue
		}
		if !slices.Contains(f.Choices, v) {
			return fmt.Errorf("%s: default %q is not one of the choices (%s)", where, v, strings.Join(f.Choices, ", "))
		}
	}
	return nil
}

// checkChoices holds every gathered arg and flag value to its choices=. The CLI and the MCP server both call it, after the data context resolves. supplied reports whether the caller gave the named input.
func checkChoices(node Command, data map[string]any, supplied func(name string) bool) error {
	args, _ := data["arg"].(map[string]any)
	for _, a := range node.Args {
		if err := checkOne("arg", a.Name, a.Choices, args[a.Name], supplied(a.Name)); err != nil {
			return err
		}
	}
	flags, _ := data["flag"].(map[string]any)
	for _, f := range node.Flags {
		if err := checkOne("flag", f.Name, f.Choices, flags[f.Name], supplied(f.Name)); err != nil {
			return err
		}
	}
	return nil
}

func checkOne(kind, name string, choices []string, val any, supplied bool) error {
	if len(choices) == 0 {
		return nil
	}
	for _, v := range choiceValues(val) {
		// An omitted input holds its zero value, which is not a choice the user made.
		if !supplied && (v == "" || v == "0") {
			continue
		}
		if !slices.Contains(choices, v) {
			return fmt.Errorf("%s %q: %q is not one of %s", kind, name, v, strings.Join(choices, ", "))
		}
	}
	return nil
}

// cliSupplied reports whether a command line gave the named input: a positional at its index, or a flag cobra saw.
func cliSupplied(c *cobra.Command, node Command, args []string, name string) bool {
	for i, a := range node.Args {
		if a.Name == name {
			return i < len(args)
		}
	}
	return c.Flags().Changed(name)
}

// argChoiceCompletion completes a positional from its arg's choices=. A variadic arg completes each further word.
func argChoiceCompletion(node Command) cobra.CompletionFunc {
	return func(_ *cobra.Command, args []string, _ string) ([]string, cobra.ShellCompDirective) {
		if len(node.Args) == 0 {
			return nil, cobra.ShellCompDirectiveDefault
		}
		idx := min(len(args), len(node.Args)-1)
		if len(args) >= len(node.Args) && !node.Args[idx].Variadic {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		if choices := node.Args[idx].Choices; len(choices) > 0 {
			return choices, cobra.ShellCompDirectiveNoFileComp
		}
		return nil, cobra.ShellCompDirectiveDefault
	}
}

// choiceValues reads a gathered value as the strings a choice applies to. A list holds one per element.
func choiceValues(val any) []string {
	switch v := val.(type) {
	case nil:
		return nil
	case []string:
		return v
	case []any:
		out := make([]string, len(v))
		for i, e := range v {
			out[i] = fmt.Sprintf("%v", e)
		}
		return out
	default:
		return []string{fmt.Sprintf("%v", v)}
	}
}

// choicesEnum is the JSON Schema enum for a choices= list, typed like the input.
func choicesEnum(choices []string, typ string) []any {
	out := make([]any, len(choices))
	for i, c := range choices {
		out[i] = c
		if typ == "int" {
			n, _ := strconv.Atoi(c)
			out[i] = n
		}
	}
	return out
}
