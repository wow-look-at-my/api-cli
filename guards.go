package main

// The pieces a config guards its inputs with: the path-segment rule, and the
// load-time resolution that lets a pattern= name a <var> instead of repeating a
// regular expression.

import (
	"fmt"
	"maps"
	"regexp"
	"strings"
)

// A path segment is one component of a path: unreserved characters only, and
// never "." or "..". A value that satisfies it cannot split a path and cannot
// climb out of one, which is the whole guard a traversal argument needs.
const (
	segmentChars = `A-Za-z0-9._~-`
	segmentSafe  = `A-Za-z0-9_~-` // the same set without the dot
)

// segmentPatternText spells that rule as an anchored RE2 expression. RE2 has no
// lookahead, so "never . or .." is a length split: one character that is not a
// dot, two that are not both dots, or three and more of anything in the set.
var segmentPatternText = `^(?:[` + segmentSafe + `]|[` + segmentSafe + `][` + segmentChars + `]|\.[` + segmentSafe + `]|[` + segmentChars + `]{3,})$`

var segmentRe = regexp.MustCompile(segmentPatternText)

// segmentPattern hands that expression to a template, so a pattern= states the
// rule by name and no config carries a copy of the regular expression.
func segmentPattern() string { return segmentPatternText }

// safeSegments is the same rule as a predicate, for a <precondition> or an <if>.
// It reads several values, and a value that is a list contributes each element.
// An empty value is an absent one, and an absent value has nothing to check, so
// a guard written over the whole tree does not fire on a leaf that declares no
// such arg.
func safeSegments(values ...any) bool {
	for _, v := range values {
		if list, ok := asList(v); ok {
			if !safeSegments(list...) {
				return false
			}
			continue
		}
		if v == nil {
			continue
		}
		s := fmt.Sprintf("%v", v)
		if s == "" {
			continue
		}
		if !segmentRe.MatchString(s) {
			return false
		}
	}
	return true
}

// validatePreconditions rejects a guard that reads data it cannot see. A
// precondition gates the run before any step, so .result holds nothing there,
// and the template error a config gets instead says nothing about why.
func validatePreconditions(pre []string, where string) error {
	for i, p := range pre {
		if strings.Contains(p, ".result") {
			return fmt.Errorf("%s.preconditions[%d]: a precondition runs before <steps>, so .result is empty; move the check into a <step when=> or into the leaf", where, i)
		}
	}
	return nil
}

// inheritedPreconditions is what a node runs: the guards it inherited, then its
// own. Ancestors come first, so a config-level rule decides before a leaf's own
// check and a reader gets the broader message.
func inheritedPreconditions(inherited, own []string) []string {
	if len(inherited) == 0 {
		return own
	}
	if len(own) == 0 {
		return inherited
	}
	out := make([]string, 0, len(inherited)+len(own))
	out = append(out, inherited...)
	return append(out, own...)
}

// resolveArgPatterns renders every pattern= that carries a template, against the
// vars in scope at that node plus the environment. A pattern has to be known
// before any value arrives, so this runs at load time and a var that needs an
// arg or a flag cannot feed one.
func resolveArgPatterns(cfg *Config) error {
	base := map[string]any{"arg": map[string]any{}, "flag": map[string]any{}, "env": envMap()}
	for i := range cfg.Commands {
		if err := resolveNodePatterns(&cfg.Commands[i], cfg.Vars, base, fmt.Sprintf("commands[%d]", i)); err != nil {
			return err
		}
	}
	return nil
}

func resolveNodePatterns(c *Command, inheritedVars, base map[string]any, where string) error {
	vars := mergeVars(inheritedVars, c.Vars)
	for i := range c.Args {
		a := &c.Args[i]
		if !strings.Contains(a.Pattern, "{{") {
			continue
		}
		rendered, err := renderPatternTemplate(a.Pattern, vars, base)
		if err != nil {
			return fmt.Errorf("%s.args[%d]: pattern: %w", where, i, err)
		}
		// A pattern that resolved to nothing matches everything, and one that
		// resolved to the missing-key marker matches nothing a caller can send.
		// Either way the config named a var it does not have.
		if rendered == "" || strings.Contains(rendered, "<no value>") {
			return fmt.Errorf("%s.args[%d]: pattern %q rendered empty or unresolved (%q); name a <var> that is in scope", where, i, a.Pattern, rendered)
		}
		a.Pattern = rendered
	}
	for i := range c.Commands {
		if err := resolveNodePatterns(&c.Commands[i], vars, base, fmt.Sprintf("%s.commands[%d]", where, i)); err != nil {
			return err
		}
	}
	return nil
}

// renderPatternTemplate resolves the node's vars, then the pattern against them.
func renderPatternTemplate(pattern string, vars, base map[string]any) (string, error) {
	data := maps.Clone(base)
	rendered, err := renderVars(vars, data)
	if err != nil {
		return "", fmt.Errorf("render vars: %w", err)
	}
	data["var"] = rendered
	return renderString(pattern, data)
}
