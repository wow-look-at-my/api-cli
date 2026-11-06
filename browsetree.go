// browseEntry is one row of the browser's command tree.

package main

import (
	"fmt"
	"slices"
	"strings"
)

// browseEntry is one row of the browser's command tree.
type browseEntry struct {
	path    []string
	depth   int
	node    Command
	confirm string
	fields  []browseField
}

// browseField is one input of a runnable entry: a positional arg, a flag, or
// the free-form args of a passthrough node.
type browseField struct {
	id       string
	label    string
	hint     string
	arg      *Arg
	flag     *Flag
	rest     bool
	value    string
	checked  bool
	required bool
}

// flattenBrowse lists every node of the config in document order, depth first.
// The confirm prompt is inherited on the same rule buildCommand applies.
func flattenBrowse(cfg *Config) []*browseEntry {
	var out []*browseEntry
	var walk func(nodes []Command, parent []string, confirm string)
	walk = func(nodes []Command, parent []string, confirm string) {
		for _, n := range nodes {
			path := append(append([]string(nil), parent...), n.Name)
			effective := confirm
			if n.Confirm != "" {
				effective = n.Confirm
			}
			e := &browseEntry{path: path, depth: len(parent), node: n, confirm: effective}
			if n.executes() {
				e.fields = browseFields(n)
			}
			out = append(out, e)
			walk(n.Commands, path, effective)
		}
	}
	walk(cfg.Commands, nil, "")
	return out
}

// label is the row the tree list draws.
func (e *browseEntry) label() string {
	mark := ""
	if len(e.node.Commands) > 0 {
		mark = "/"
	}
	return strings.Repeat("  ", e.depth) + e.node.Name + mark
}

func (e *browseEntry) runnable() bool { return e.node.executes() }

func browseFields(n Command) []browseField {
	var out []browseField
	for i := range n.Args {
		if n.Passthrough {
			break
		}
		a := &n.Args[i]
		hint := a.Description
		if a.Variadic {
			hint = strings.TrimSpace(hint + " (several values, split on spaces)")
		}
		out = append(out, browseField{id: "arg-" + a.Name, label: a.Name, hint: hint, arg: a, required: a.Required})
	}
	for i := range n.Flags {
		f := &n.Flags[i]
		field := browseField{id: "flag-" + f.Name, label: "--" + f.Name, hint: f.Description, flag: f, required: f.Required}
		switch f.Type {
		case "bool":
			field.checked, _ = f.Default.(bool)
			if field.hint == "" {
				field.hint = f.Name
			}
		case "string-slice":
			field.hint = strings.TrimSpace(field.hint + " (comma separated)")
		default:
			if f.Default != nil && f.Default != "" {
				field.hint = strings.TrimSpace(fmt.Sprintf("%s (default %v)", field.hint, f.Default))
			}
		}
		out = append(out, field)
	}
	if n.Passthrough {
		out = append(out, browseField{id: "rest", label: "args", hint: "passed through as is, split on spaces", rest: true})
	}
	return out
}

func (f *browseField) isBool() bool { return f.flag != nil && f.flag.Type == "bool" }

// argv is the command line the browser runs for e, without the program name.
// An empty field is left out, so cobra applies the declared default.
func (e *browseEntry) argv() ([]string, error) {
	var argv, flags []string
	for i := range e.fields {
		f := &e.fields[i]
		value := strings.TrimSpace(f.value)
		switch {
		case f.rest:
			argv = append(argv, strings.Fields(value)...)
		case f.arg != nil:
			if value == "" {
				if f.required {
					return nil, fmt.Errorf("%s is required", f.label)
				}
				continue
			}
			if f.arg.Variadic {
				argv = append(argv, strings.Fields(value)...)
			} else {
				argv = append(argv, value)
			}
		case f.isBool():
			def, _ := f.flag.Default.(bool)
			if f.checked != def {
				flags = append(flags, fmt.Sprintf("--%s=%t", f.flag.Name, f.checked))
			}
		case value == "":
			if f.required {
				return nil, fmt.Errorf("%s is required", f.label)
			}
		case f.flag.Type == "string-slice":
			for _, part := range strings.Split(value, ",") {
				flags = append(flags, "--"+f.flag.Name+"="+strings.TrimSpace(part))
			}
		default:
			flags = append(flags, "--"+f.flag.Name+"="+value)
		}
	}
	out := append(append([]string(nil), e.path...), flags...)
	// A positional arg that starts with a dash reads as a flag without the separator.
	if !e.node.Passthrough && slices.ContainsFunc(argv, func(a string) bool { return strings.HasPrefix(a, "-") }) {
		out = append(out, "--")
	}
	return append(out, argv...), nil
}

// commandLine is argv as a user would type it, for the preview under the form.
func commandLine(name string, argv []string) string {
	parts := []string{name}
	for _, a := range argv {
		parts = append(parts, previewQuote(a))
	}
	return strings.Join(parts, " ")
}

// previewQuote leaves a plain word bare, so the preview reads as typed.
func previewQuote(s string) string {
	if s != "" && !strings.ContainsAny(s, " \t\n'\"\\$`*?[]{}()<>|&;#~!") {
		return s
	}
	return shellQuote(s)
}
