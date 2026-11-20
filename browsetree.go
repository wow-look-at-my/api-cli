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
	hints   TUIHints
	confirm string
	watch   string
	fields  []browseField
	// parent is the entry of the enclosing group, nil at the top level.
	parent    *browseEntry
	collapsed bool
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
// The confirm prompt and watch= are inherited on the rule buildCommand applies.
// A node a <tui hidden="true"> covers is left out, with its subtree.
func flattenBrowse(cfg *Config) []*browseEntry {
	var out []*browseEntry
	var walk func(nodes []Command, parent *browseEntry, confirm, watch string)
	walk = func(nodes []Command, parent *browseEntry, confirm, watch string) {
		for _, n := range nodes {
			if n.TUI != nil && n.TUI.Hidden {
				continue
			}
			var path []string
			depth := 0
			if parent != nil {
				path = append(path, parent.path...)
				depth = parent.depth + 1
			}
			path = append(path, n.Name)
			effConfirm, effWatch := confirm, watch
			if n.Confirm != "" {
				effConfirm = n.Confirm
			}
			if n.Watch != "" {
				effWatch = n.Watch
			}
			e := &browseEntry{path: path, depth: depth, node: n, confirm: effConfirm, watch: effWatch, parent: parent}
			if n.TUI != nil {
				e.hints = *n.TUI
			}
			if n.executes() {
				e.fields = browseFields(n)
			}
			out = append(out, e)
			walk(n.Commands, e, effConfirm, effWatch)
		}
	}
	walk(cfg.Commands, nil, "", "")
	return out
}

func (e *browseEntry) name() string {
	if e.hints.Label != "" {
		return e.hints.Label
	}
	return e.node.Name
}

func (e *browseEntry) group() bool { return len(e.node.Commands) > 0 }

func (e *browseEntry) pathString() string { return strings.Join(e.path, " ") }

// label is the row the tree list draws. A group carries a mark that says whether it is open.
func (e *browseEntry) label() string {
	mark := ""
	switch {
	case e.group() && e.collapsed:
		mark = " ▸"
	case e.group():
		mark = " ▾"
	}
	return strings.Repeat("  ", e.depth) + e.name() + mark
}

func (e *browseEntry) runnable() bool { return e.node.executes() }

// hiddenBy reports whether a collapsed ancestor hides e.
func (e *browseEntry) hiddenBy() bool {
	for p := e.parent; p != nil; p = p.parent {
		if p.collapsed {
			return true
		}
	}
	return false
}

// matches reports whether e answers the tree filter: every word of it appears in the path, the label or the description.
func (e *browseEntry) matches(filter string) bool {
	hay := strings.ToLower(e.pathString() + " " + e.name() + " " + e.node.Description)
	for _, w := range strings.Fields(strings.ToLower(filter)) {
		if !strings.Contains(hay, w) {
			return false
		}
	}
	return true
}

// visibleEntries is the tree as drawn: the filter's matches with their ancestors, or every entry no collapsed group hides.
func visibleEntries(entries []*browseEntry, filter string) []*browseEntry {
	var out []*browseEntry
	if strings.TrimSpace(filter) == "" {
		for _, e := range entries {
			if !e.hiddenBy() {
				out = append(out, e)
			}
		}
		return out
	}
	keep := map[*browseEntry]bool{}
	for _, e := range entries {
		if e.matches(filter) {
			for p := e; p != nil; p = p.parent {
				keep[p] = true
			}
		}
	}
	for _, e := range entries {
		if keep[e] {
			out = append(out, e)
		}
	}
	return out
}

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

// name is the arg or flag name the field fills: the key a link, a suggestion and the shared memory match on.
func (f *browseField) name() string {
	switch {
	case f.arg != nil:
		return f.arg.Name
	case f.flag != nil:
		return f.flag.Name
	}
	return ""
}

// choices is the closed set the field takes, or nil. An input that takes several values stays a text field.
func (f *browseField) choices() []string {
	switch {
	case f.arg != nil && !f.arg.Variadic:
		return f.arg.Choices
	case f.flag != nil && f.flag.Type != "string-slice":
		return f.flag.Choices
	}
	return nil
}

func (f *browseField) suggest() *Suggest {
	switch {
	case f.arg != nil:
		return f.arg.Suggest
	case f.flag != nil:
		return f.flag.Suggest
	}
	return nil
}

// set fills the field from text. A bool reads "true" as checked.
func (f *browseField) set(v string) {
	if f.isBool() {
		f.checked = v == "true"
		return
	}
	f.value = v
}

// get is the field as text, the inverse of set.
func (f *browseField) get() string {
	if f.isBool() {
		return fmt.Sprint(f.checked)
	}
	return f.value
}

// cycle steps a choice field through its choices, with the empty value standing for the default.
func (f *browseField) cycle(delta int) {
	opts := append([]string{""}, f.choices()...)
	i := max(0, slices.Index(opts, f.value))
	f.value = opts[((i+delta)%len(opts)+len(opts))%len(opts)]
}

// missing is the required fields that hold nothing.
func (e *browseEntry) missing() []*browseField {
	var out []*browseField
	for i := range e.fields {
		f := &e.fields[i]
		if f.required && !f.isBool() && strings.TrimSpace(f.value) == "" {
			out = append(out, f)
		}
	}
	return out
}

// snapshot is every field's value by id, which a result frame keeps so going back restores the form.
func (e *browseEntry) snapshot() map[string]string {
	out := make(map[string]string, len(e.fields))
	for i := range e.fields {
		out[e.fields[i].id] = e.fields[i].get()
	}
	return out
}

func (e *browseEntry) restore(values map[string]string) {
	for i := range e.fields {
		if v, ok := values[e.fields[i].id]; ok {
			e.fields[i].set(v)
		}
	}
}

// inputs is every named text field's value by arg or flag name, for carrying into another command.
func (e *browseEntry) inputs() map[string]string {
	out := map[string]string{}
	for i := range e.fields {
		f := &e.fields[i]
		if n := f.name(); n != "" && !f.isBool() && strings.TrimSpace(f.value) != "" {
			out[n] = strings.TrimSpace(f.value)
		}
	}
	return out
}

// fieldNamed is the field that fills the named arg or flag, or nil.
func (e *browseEntry) fieldNamed(name string) *browseField {
	for i := range e.fields {
		if e.fields[i].name() == name {
			return &e.fields[i]
		}
	}
	return nil
}

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
