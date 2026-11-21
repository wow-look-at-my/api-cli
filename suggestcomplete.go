// Shell completion from suggest=: the source command runs, and its records become the candidates.

package main

import (
	"strings"

	"github.com/spf13/cobra"
)

// installSuggestCompletion gives every input that declares suggest= a completion function. An input with choices=
// keeps the completion of its choices, because those are the only legal values.
func installSuggestCompletion(root *cobra.Command, cfg *Config) {
	var walk func(nodes []Command, parent []string)
	walk = func(nodes []Command, parent []string) {
		for _, n := range nodes {
			path := append(append([]string(nil), parent...), n.Name)
			if cmd, _, err := root.Find(path); err == nil && cmd.Name() == n.Name {
				attachSuggest(cmd, n, cfg)
			}
			walk(n.Commands, path)
		}
	}
	walk(cfg.Commands, nil)
}

func attachSuggest(cmd *cobra.Command, node Command, cfg *Config) {
	if len(node.Args) > 0 {
		prev := cmd.ValidArgsFunction
		cmd.ValidArgsFunction = func(c *cobra.Command, args []string, toComplete string) ([]cobra.Completion, cobra.ShellCompDirective) {
			idx := min(len(args), len(node.Args)-1)
			a := node.Args[idx]
			if (len(args) < len(node.Args) || a.Variadic) && a.Suggest != nil && len(a.Choices) == 0 {
				return completeSuggest(cfg, a.Name, a.Suggest, typedInputs(c, node, args), toComplete), cobra.ShellCompDirectiveNoFileComp
			}
			if prev == nil {
				return nil, cobra.ShellCompDirectiveDefault
			}
			return prev(c, args, toComplete)
		}
	}
	for _, f := range node.Flags {
		if f.Suggest == nil || len(f.Choices) > 0 || f.Type == "bool" {
			continue
		}
		name, s := f.Name, f.Suggest
		_ = cmd.RegisterFlagCompletionFunc(name, func(c *cobra.Command, args []string, toComplete string) ([]cobra.Completion, cobra.ShellCompDirective) {
			return completeSuggest(cfg, name, s, typedInputs(c, node, args), toComplete), cobra.ShellCompDirectiveNoFileComp
		})
	}
}

// typedInputs is what the command line holds so far, by input name: the positionals typed, and the flags given.
func typedInputs(c *cobra.Command, node Command, args []string) map[string]string {
	out := map[string]string{}
	for i, a := range node.Args {
		if i < len(args) && !a.Variadic && args[i] != "" {
			out[a.Name] = args[i]
		}
	}
	for _, f := range node.Flags {
		if fl := c.Flags().Lookup(f.Name); fl != nil && fl.Changed {
			out[f.Name] = fl.Value.String()
		}
	}
	return out
}

// completeSuggest runs a suggestion source and returns the values that start with toComplete, each with its record's
// title as the description. The source takes its inputs from the command line, then from what the tui saved. A source
// that asks for confirmation never runs from a keypress, and neither does one that still lacks a required input.
// Completion must not write to the terminal, so a source that fails yields nothing, and says why in cobra's debug log.
func completeSuggest(cfg *Config, name string, s *Suggest, typed map[string]string, toComplete string) []cobra.Completion {
	var src *browseEntry
	for _, e := range flattenBrowse(cfg) {
		if e.pathString() == s.From {
			src = e
		}
	}
	if src == nil || src.confirm != "" {
		cobra.CompDebugln("suggest: "+s.From+" is missing or asks for confirmation", false)
		return nil
	}
	saved := map[string]string{}
	if path, err := statePath(cfg); err == nil {
		if st, err := loadState(path); err == nil {
			saved = st.Memory
		}
	}
	for i := range src.fields {
		f := &src.fields[i]
		if v, ok := typed[f.name()]; ok {
			f.value = v
		} else if v, ok := saved[f.name()]; ok && (f.arg != nil || f.required) {
			f.value = v
		}
	}
	argv, err := src.argv()
	if err != nil {
		cobra.CompDebugln("suggest: "+s.From+": "+err.Error(), false)
		return nil
	}
	out := browseExec(cfg, argv, 80, 24)
	if out.code != 0 {
		cobra.CompDebugln("suggest: "+s.From+" failed", false)
		return nil
	}
	values, labels := suggestions(out.raw, src, &browseField{arg: &Arg{Name: name, Suggest: s}})
	var comps []cobra.Completion
	for i, v := range values {
		if !strings.HasPrefix(v, toComplete) {
			continue
		}
		desc := strings.TrimSpace(strings.TrimPrefix(labels[i], v))
		comps = append(comps, cobra.CompletionWithDesc(v, desc))
	}
	return comps
}
