// The <tui> hints on a command, and the suggest= source on an arg or a flag.

package main

import (
	"fmt"
	"strings"
)

// TUIHints shapes how the tui subcommand shows one command. Every hint is
// optional: the browser infers each when it is absent.
type TUIHints struct {
	Label   string    `json:"label,omitempty"`
	Hidden  bool      `json:"hidden,omitempty"`
	Autorun bool      `json:"autorun,omitempty"`
	Rows    string    `json:"rows,omitempty"`
	Columns []string  `json:"columns,omitempty"`
	Title   string    `json:"title,omitempty"`
	Links   []TUILink `json:"links,omitempty"`
}

// TUILink opens another command from a record of this command's result.
type TUILink struct {
	To    string    `json:"to"`
	Label string    `json:"label,omitempty"`
	With  []TUIWith `json:"with,omitempty"`
}

// TUIWith fills one arg or flag of a link's target. Field reads the record. Expr renders with the record promoted to the top level.
type TUIWith struct {
	Name  string `json:"name"`
	Field string `json:"field,omitempty"`
	Expr  string `json:"expr,omitempty"`
}

// Suggest names a command whose result lists the values an arg or a flag can take.
type Suggest struct {
	From  string `json:"from"`
	Field string `json:"field,omitempty"`
}

func buildTUI(n *xnode) (*TUIHints, error) {
	if err := checkAttrs(n, "label", "hidden", "autorun", "rows", "columns", "title"); err != nil {
		return nil, err
	}
	h := &TUIHints{
		Label:   strings.TrimSpace(n.Attr("label")),
		Hidden:  n.Attr("hidden") == "true",
		Autorun: n.Attr("autorun") == "true",
		Rows:    strings.TrimSpace(n.Attr("rows")),
		Columns: splitList(n.Attr("columns")),
		Title:   strings.TrimSpace(n.Attr("title")),
	}
	for _, child := range n.Children() {
		if child.Name() != "link" {
			return nil, fmt.Errorf("<tui>: unexpected child element <%s>", child.Name())
		}
		if err := checkAttrs(child, "to", "label"); err != nil {
			return nil, err
		}
		link := TUILink{To: normalizePath(child.Attr("to")), Label: strings.TrimSpace(child.Attr("label"))}
		for _, w := range child.Children() {
			if w.Name() != "with" {
				return nil, fmt.Errorf("<link to=%q>: unexpected child element <%s>", link.To, w.Name())
			}
			if err := checkAttrs(w, "name", "field", "expr"); err != nil {
				return nil, err
			}
			link.With = append(link.With, TUIWith{Name: w.Attr("name"), Field: strings.TrimSpace(w.Attr("field")), Expr: w.Attr("expr")})
		}
		h.Links = append(h.Links, link)
	}
	return h, nil
}

// buildSuggest reads suggest= and suggest-field= off an <arg> or a <flag>.
func buildSuggest(n *xnode) *Suggest {
	from := normalizePath(n.Attr("suggest"))
	if from == "" {
		return nil
	}
	return &Suggest{From: from, Field: strings.TrimSpace(n.Attr("suggest-field"))}
}

// splitList reads a comma-separated attribute. Blank items are dropped.
func splitList(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// normalizePath reads a command path as the words a user types, separated by any run of whitespace.
func normalizePath(s string) string { return strings.Join(strings.Fields(s), " ") }

// runnableIndex maps each runnable node's path to the node.
func runnableIndex(cmds []Command, parent string, out map[string]*Command) {
	for i := range cmds {
		c := &cmds[i]
		path := strings.TrimSpace(parent + " " + c.Name)
		if c.executes() {
			out[path] = c
		}
		runnableIndex(c.Commands, path, out)
	}
}

// validateTUIRefs checks every reference a <tui> link or a suggest= makes to another command. It reads the whole tree, so it runs once the tree is complete.
func validateTUIRefs(cfg *Config) error {
	index := map[string]*Command{}
	runnableIndex(cfg.Commands, "", index)
	var walk func(cmds []Command, parent string) error
	walk = func(cmds []Command, parent string) error {
		for i := range cmds {
			c := &cmds[i]
			path := strings.TrimSpace(parent + " " + c.Name)
			if err := validateNodeTUI(c, path, index); err != nil {
				return err
			}
			if err := walk(c.Commands, path); err != nil {
				return err
			}
		}
		return nil
	}
	return walk(cfg.Commands, "")
}

func validateNodeTUI(c *Command, path string, index map[string]*Command) error {
	for _, a := range c.Args {
		if err := validateSuggest(a.Suggest, fmt.Sprintf("%s: arg %q", path, a.Name), index); err != nil {
			return err
		}
	}
	for _, f := range c.Flags {
		if err := validateSuggest(f.Suggest, fmt.Sprintf("%s: flag %q", path, f.Name), index); err != nil {
			return err
		}
		if f.Suggest != nil && f.Type == "bool" {
			return fmt.Errorf("%s: flag %q is a bool, so it takes no suggest=", path, f.Name)
		}
	}
	h := c.TUI
	if h == nil {
		return nil
	}
	if !c.executes() && (h.Autorun || h.Rows != "" || len(h.Columns) > 0 || h.Title != "" || len(h.Links) > 0) {
		return fmt.Errorf("%s: <tui> on a group takes only label= and hidden=; the rest describe a result, and a group has none", path)
	}
	for i, l := range h.Links {
		where := fmt.Sprintf("%s: <tui> link[%d]", path, i)
		target, ok := index[l.To]
		if !ok {
			return fmt.Errorf("%s: to=%q names no command that runs", where, l.To)
		}
		seen := map[string]bool{}
		for _, w := range l.With {
			if seen[w.Name] {
				return fmt.Errorf("%s: <with name=%q> is declared twice", where, w.Name)
			}
			seen[w.Name] = true
			if !declaresInput(target, w.Name) {
				return fmt.Errorf("%s: <with name=%q>: %q declares no arg or flag of that name", where, w.Name, l.To)
			}
			if (w.Field == "") == (w.Expr == "") {
				return fmt.Errorf("%s: <with name=%q> takes field= or expr=, exactly one", where, w.Name)
			}
		}
	}
	return nil
}

func validateSuggest(s *Suggest, where string, index map[string]*Command) error {
	if s == nil {
		return nil
	}
	if _, ok := index[s.From]; !ok {
		return fmt.Errorf("%s: suggest=%q names no command that runs", where, s.From)
	}
	return nil
}

// declaresInput reports whether c declares an arg or a flag called name.
func declaresInput(c *Command, name string) bool {
	for _, a := range c.Args {
		if a.Name == name {
			return true
		}
	}
	for _, f := range c.Flags {
		if f.Name == name {
			return true
		}
	}
	return false
}
