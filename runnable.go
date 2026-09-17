package main

// A runnable parent: a node that has subcommands and also executes, so one name
// serves `tool [id]` and `tool sub`. Cobra decides between the two by reading
// the first positional as a subcommand name, so the split is only unambiguous
// when no argument value can spell a subcommand. That is what `pattern=` buys:
// the loader rejects a pattern that matches one of the node's own subcommand
// names, and the run rejects a value the pattern does not match.

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/spf13/cobra"
)

// executes reports whether this node runs when it is invoked. A leaf always
// does. A node with subcommands does when it declares runnable=.
func (c *Command) executes() bool {
	return len(c.Commands) == 0 || c.Runnable
}

// nodeKind names what this node is, for an error a config author reads.
func nodeKind(c *Command) string {
	if len(c.Commands) == 0 {
		return "leaf"
	}
	return "runnable node"
}

// validateRunnable checks the invariants that keep a runnable parent's dispatch
// decidable. Every check is a load error, so a config that reads as ambiguous
// never reaches a user.
func validateRunnable(c *Command, where string) error {
	if c.Runnable && len(c.Commands) == 0 {
		return fmt.Errorf("%s: runnable= needs subcommands; a node without them already runs", where)
	}
	if c.Runnable && c.Passthrough {
		return fmt.Errorf("%s: runnable= and passthrough= cannot both hold: passthrough takes every argument, which leaves nothing to name a subcommand", where)
	}

	// The node's own subcommands first, in declaration order, then the names
	// cobra owns. A config author reads the first match, so it must be the name
	// they are most likely to have meant.
	names := make([]string, 0, len(c.Commands)+reservedCommandNames.Len())
	for _, sub := range c.Commands {
		names = append(names, sub.Name)
	}
	reserved := reservedCommandNames.Values()
	sort.Strings(reserved)
	names = append(names, reserved...)

	for i, a := range c.Args {
		aw := fmt.Sprintf("%s.args[%d]", where, i)
		if a.Pattern == "" {
			if c.Runnable {
				return fmt.Errorf("%s: arg %q on a runnable= node needs a pattern=, so a value can never spell a subcommand of %q", aw, a.Name, c.Name)
			}
			continue
		}
		re, err := regexp.Compile(a.Pattern)
		if err != nil {
			return fmt.Errorf("%s: pattern %q does not compile: %w", aw, a.Pattern, err)
		}
		if !c.Runnable {
			continue
		}
		for _, name := range names {
			if re.MatchString(name) {
				return fmt.Errorf("%s: pattern %q matches the subcommand name %q, so %q would be ambiguous; narrow the pattern (anchor it with ^ and $)", aw, a.Pattern, name, c.Name+" "+name)
			}
		}
	}
	return nil
}

// argPatterns compiles the declared patterns once per node, for the validator
// the cobra command carries. validateRunnable already proved each one compiles.
func argPatterns(node Command) []*regexp.Regexp {
	out := make([]*regexp.Regexp, len(node.Args))
	for i, a := range node.Args {
		if a.Pattern == "" {
			continue
		}
		out[i] = regexp.MustCompile(a.Pattern)
	}
	return out
}

// matchArgPatterns checks each supplied value against its arg's pattern. The
// error names the two things the value could have been, because a user who
// mistypes a subcommand lands here.
func matchArgPatterns(node Command, res []*regexp.Regexp) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		for i, v := range args {
			idx := i
			if idx >= len(node.Args) {
				if len(node.Args) == 0 || !node.Args[len(node.Args)-1].Variadic {
					break
				}
				idx = len(node.Args) - 1
			}
			re := res[idx]
			if re == nil || re.MatchString(v) {
				continue
			}
			return fmt.Errorf("%q does not match <%s> (pattern %q)%s", v, node.Args[idx].Name, node.Args[idx].Pattern, subcommandHint(cmd))
		}
		return nil
	}
}

// subcommandHint lists what else the value could have been, for a node that has
// subcommands. It is empty on a leaf, where there is nothing else to suggest.
func subcommandHint(cmd *cobra.Command) string {
	var names []string
	for _, sub := range cmd.Commands() {
		if sub.IsAvailableCommand() {
			names = append(names, sub.Name())
		}
	}
	if len(names) == 0 {
		return ""
	}
	return fmt.Sprintf(", and it is not one of the subcommands (%s)", strings.Join(names, ", "))
}

// chainArgs runs each validator in order, so a count error reports before a
// pattern error on the same invocation.
func chainArgs(validators ...cobra.PositionalArgs) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		for _, v := range validators {
			if v == nil {
				continue
			}
			if err := v(cmd, args); err != nil {
				return err
			}
		}
		return nil
	}
}
