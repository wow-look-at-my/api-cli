package main

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// parseConfigXML maps an XML config document to the Config model.
//
// api-dsl tokenizes the document into an order-preserving DOM (xnode), and this
// file walks that DOM to build the structs. api-dsl also compiles the
// placeholders (<value>/<if>/<for>) in element content to Go text/template
// source, so the rest of the runtime renders them like any other template
// string. Unknown elements and attributes are rejected to catch a typo early.
func parseConfigXML(src []byte) (*Config, error) {
	root, err := parseDOM(src)
	if err != nil {
		return nil, err
	}
	if root.Name() != "config" {
		return nil, fmt.Errorf("root element must be <config>, got <%s>", root.Name())
	}
	return buildConfig(root)
}

func buildConfig(root *xnode) (*Config, error) {
	if err := checkAttrs(root, "name", "schema"); err != nil {
		return nil, err
	}
	cfg := &Config{Name: root.Attr("name"), Schema: root.Attr("schema")}
	for _, child := range root.Children() {
		switch child.Name() {
		case "description":
			if err := checkAttrs(child); err != nil {
				return nil, err
			}
			d, err := textOf(child)
			if err != nil {
				return nil, err
			}
			cfg.Description = d
		case "vars":
			v, err := buildVars(child)
			if err != nil {
				return nil, err
			}
			cfg.Vars = v
		case "run":
			cmd, req, err := buildRun(child)
			if err != nil {
				return nil, err
			}
			cfg.Command, cfg.Request = cmd, req
		case "cwd":
			s, err := compileTextElem(child)
			if err != nil {
				return nil, err
			}
			cfg.Cwd = s
		case "stdin":
			s, err := compileTextElem(child)
			if err != nil {
				return nil, err
			}
			cfg.Stdin = s
		case "formats":
			f, err := buildFormats(child)
			if err != nil {
				return nil, err
			}
			cfg.Formats = f
		case "transports":
			t, err := buildTransports(child)
			if err != nil {
				return nil, err
			}
			cfg.Transports = t
		case "downloads":
			d, err := buildDownloads(child)
			if err != nil {
				return nil, err
			}
			cfg.Downloads = d
		case "command":
			c, err := buildCommandNode(child)
			if err != nil {
				return nil, err
			}
			cfg.Commands = append(cfg.Commands, *c)
		default:
			return nil, fmt.Errorf("<config>: unexpected child element <%s>", child.Name())
		}
	}
	return cfg, nil
}

func buildVars(n *xnode) (map[string]any, error) {
	out := map[string]any{}
	for _, child := range n.Children() {
		if child.Name() != "var" {
			return nil, fmt.Errorf("<vars>: unexpected child element <%s>", child.Name())
		}
		if err := checkAttrs(child, "name"); err != nil {
			return nil, err
		}
		name := child.Attr("name")
		if name == "" {
			return nil, fmt.Errorf("<var>: name= is required")
		}
		v, err := compileContent(child)
		if err != nil {
			return nil, err
		}
		out[name] = v
	}
	return out, nil
}

// buildRun parses a <run> element into either a Cmd (shell or argv form) or a
// Request. Exactly one form applies.
func buildRun(n *xnode) (*Cmd, *Request, error) {
	if err := checkAttrs(n); err != nil {
		return nil, nil, err
	}
	elems := n.Children()
	for _, e := range elems {
		if e.Name() == "request" {
			if len(elems) != 1 {
				return nil, nil, fmt.Errorf("<run>: <request> must be the only child")
			}
			req, err := buildRequest(e)
			return nil, req, err
		}
	}
	hasArgv := false
	for _, e := range elems {
		if e.Name() == "argv" {
			hasArgv = true
			break
		}
	}
	if hasArgv {
		var argv []string
		for _, e := range elems {
			if e.Name() != "argv" {
				return nil, nil, fmt.Errorf("<run>: cannot mix <argv> with <%s>", e.Name())
			}
			s, err := compileTextElem(e)
			if err != nil {
				return nil, nil, err
			}
			argv = append(argv, s)
		}
		return &Cmd{Argv: argv}, nil, nil
	}
	tmpl, err := compileContent(n)
	if err != nil {
		return nil, nil, err
	}
	return &Cmd{Shell: true, Template: strings.TrimSpace(tmpl)}, nil, nil
}

func buildFields(n *xnode) (FieldsBlock, error) {
	f, err := buildFieldsBody(n)
	if err != nil {
		return FieldsBlock{}, err
	}
	return FieldsBlock{When: strings.TrimSpace(n.Attr("when")), Fields: f}, nil
}

func buildFieldsBody(n *xnode) (*Fields, error) {
	if err := checkAttrs(n, "over", "footer", "when"); err != nil {
		return nil, err
	}
	f := &Fields{Over: strings.TrimSpace(n.Attr("over")), Footer: n.Attr("footer")}
	for _, child := range n.Children() {
		if child.Name() != "field" {
			return nil, fmt.Errorf("<fields>: unexpected child element <%s>", child.Name())
		}
		fld, err := buildField(child)
		if err != nil {
			return nil, err
		}
		f.List = append(f.List, fld)
	}
	return f, nil
}

func buildField(n *xnode) (Field, error) {
	if err := checkAttrs(n, "name", "default", "truncate", "firstline", "priority", "show_in", "expr"); err != nil {
		return Field{}, err
	}
	name := n.Attr("name")
	if name == "" {
		return Field{}, fmt.Errorf("<field>: name= is required")
	}
	path, err := textOf(n)
	if err != nil {
		return Field{}, err
	}
	fld := Field{
		Name:      name,
		Path:      path,
		Expr:      n.Attr("expr"),
		Default:   n.Attr("default"),
		FirstLine: n.Attr("firstline") == "true",
		ShowIn:    strings.TrimSpace(n.Attr("show_in")),
	}
	if fld.Expr != "" && fld.Path != "" {
		return Field{}, fmt.Errorf("<field %q>: cannot set both a source path and expr=", name)
	}
	if fld.Expr == "" && fld.Path == "" {
		return Field{}, fmt.Errorf("<field %q>: needs a source path or expr=", name)
	}
	if t := n.Attr("truncate"); t != "" {
		v, err := strconv.Atoi(t)
		if err != nil {
			return Field{}, fmt.Errorf("<field %q>: truncate=%q must be an integer", name, t)
		}
		fld.Truncate = v
	}
	if p := n.Attr("priority"); p != "" {
		v, err := strconv.Atoi(p)
		if err != nil {
			return Field{}, fmt.Errorf("<field %q>: priority=%q must be an integer", name, p)
		}
		fld.Priority = v
	}
	return fld, nil
}

func buildFormats(n *xnode) (map[string]*Format, error) {
	out := map[string]*Format{}
	for _, child := range n.Children() {
		if child.Name() != "format" {
			return nil, fmt.Errorf("<formats>: unexpected child element <%s>", child.Name())
		}
		if err := checkAttrs(child, "name", "input", "when"); err != nil {
			return nil, err
		}
		name := child.Attr("name")
		if name == "" {
			return nil, fmt.Errorf("<format>: name= is required")
		}
		f, err := buildFormat(child)
		if err != nil {
			return nil, err
		}
		out[name] = f
	}
	return out, nil
}

func buildFormat(n *xnode) (*Format, error) {
	f := &Format{Input: n.Attr("input"), When: n.Attr("when")}
	for _, child := range n.Children() {
		if child.Name() != "view" {
			return nil, fmt.Errorf("<format>: unexpected child element <%s>", child.Name())
		}
		if err := checkAttrs(child, "name", "when", "default"); err != nil {
			return nil, err
		}
		tmpl, err := compileContent(child)
		if err != nil {
			return nil, err
		}
		f.Views = append(f.Views, View{
			Name:     child.Attr("name"),
			When:     child.Attr("when"),
			Default:  child.Attr("default") == "true",
			Template: tmpl,
		})
	}
	return f, nil
}

func buildCommandNode(n *xnode) (*Command, error) {
	if err := checkAttrs(n, "name", "description", "passthrough", "runnable", "confirm"); err != nil {
		return nil, err
	}
	c := &Command{
		Name:        n.Attr("name"),
		Description: n.Attr("description"),
		Passthrough: n.Attr("passthrough") == "true",
		Runnable:    n.Attr("runnable") == "true",
		Confirm:     n.Attr("confirm"),
	}
	for _, child := range n.Children() {
		if err := addCommandChild(c, child); err != nil {
			return nil, err
		}
	}
	return c, nil
}

// addCommandChild dispatches one child element of a <command> into the Command.
func addCommandChild(c *Command, child *xnode) error {
	switch child.Name() {
	case "arg":
		a, err := buildArg(child)
		if err != nil {
			return err
		}
		c.Args = append(c.Args, a)
	case "flag":
		fl, err := buildFlag(child)
		if err != nil {
			return err
		}
		c.Flags = append(c.Flags, fl)
	case "vars":
		v, err := buildVars(child)
		if err != nil {
			return err
		}
		c.Vars = v
	case "run":
		cmd, req, err := buildRun(child)
		if err != nil {
			return err
		}
		c.Command, c.Request = cmd, req
	case "cwd":
		s, err := compileTextElem(child)
		if err != nil {
			return err
		}
		c.Cwd = s
	case "stdin":
		s, err := compileTextElem(child)
		if err != nil {
			return err
		}
		c.Stdin = s
	case "confirm":
		s, err := compileTextElem(child)
		if err != nil {
			return err
		}
		c.Confirm = s
	case "preconditions":
		for _, p := range child.Children() {
			if p.Name() != "precondition" {
				return fmt.Errorf("<preconditions>: unexpected child element <%s>", p.Name())
			}
			s, err := compileTextElem(p)
			if err != nil {
				return err
			}
			c.Preconditions = append(c.Preconditions, s)
		}
	case "steps":
		for _, s := range child.Children() {
			if s.Name() != "step" {
				return fmt.Errorf("<steps>: unexpected child element <%s>", s.Name())
			}
			step, err := buildStep(s)
			if err != nil {
				return err
			}
			c.Steps = append(c.Steps, step)
		}
	case "entry":
		raw, err := buildEntry(child)
		if err != nil {
			return err
		}
		c.Entry = raw
	case "fields":
		f, err := buildFields(child)
		if err != nil {
			return err
		}
		c.Fields = append(c.Fields, f)
	case "tml":
		t, err := buildTML(child)
		if err != nil {
			return err
		}
		c.TML = t
	case "format":
		ref, err := buildFormatRef(child)
		if err != nil {
			return err
		}
		c.Format = ref
	case "download":
		d, err := buildDownload(child)
		if err != nil {
			return err
		}
		c.Downloads = append(c.Downloads, d)
	case "command":
		sub, err := buildCommandNode(child)
		if err != nil {
			return err
		}
		c.Commands = append(c.Commands, *sub)
	default:
		return fmt.Errorf("<command %q>: unexpected child element <%s>", c.Name, child.Name())
	}
	return nil
}

func buildArg(n *xnode) (Arg, error) {
	if err := checkAttrs(n, "name", "type", "required", "variadic", "pattern", "description"); err != nil {
		return Arg{}, err
	}
	return Arg{
		Name:        n.Attr("name"),
		Type:        n.Attr("type"),
		Required:    n.Attr("required") == "true",
		Variadic:    n.Attr("variadic") == "true",
		Pattern:     strings.TrimSpace(n.Attr("pattern")),
		Description: n.Attr("description"),
	}, nil
}

func buildFlag(n *xnode) (Flag, error) {
	if err := checkAttrs(n, "name", "short", "type", "default", "required", "conflicts", "description"); err != nil {
		return Flag{}, err
	}
	fl := Flag{
		Name:        n.Attr("name"),
		Short:       n.Attr("short"),
		Type:        n.Attr("type"),
		Required:    n.Attr("required") == "true",
		Description: n.Attr("description"),
	}
	if c := strings.TrimSpace(n.Attr("conflicts")); c != "" {
		for _, p := range strings.Split(c, ",") {
			if p = strings.TrimSpace(p); p != "" {
				fl.Conflicts = append(fl.Conflicts, p)
			}
		}
	}
	if n.HasAttr("default") {
		def := n.Attr("default")
		switch fl.Type {
		case "bool":
			fl.Default = def == "true"
		case "int":
			v, err := strconv.Atoi(def)
			if err != nil {
				return Flag{}, fmt.Errorf("<flag %q>: default=%q must be an integer", fl.Name, def)
			}
			fl.Default = v
		case "string-slice":
			var items []any
			for _, p := range strings.Split(def, ",") {
				if p = strings.TrimSpace(p); p != "" {
					items = append(items, p)
				}
			}
			fl.Default = items
		default:
			fl.Default = def
		}
	}
	return fl, nil
}

func buildStep(n *xnode) (Step, error) {
	if err := checkAttrs(n, "name", "when", "over", "until", "interval", "attempts"); err != nil {
		return Step{}, err
	}
	s := Step{
		Name:     n.Attr("name"),
		When:     n.Attr("when"),
		Over:     strings.TrimSpace(n.Attr("over")),
		Until:    n.Attr("until"),
		Interval: strings.TrimSpace(n.Attr("interval")),
	}
	if raw := strings.TrimSpace(n.Attr("attempts")); raw != "" {
		v, err := strconv.Atoi(raw)
		if err != nil {
			return Step{}, fmt.Errorf("<step %q>: attempts=%q must be an integer", s.Name, raw)
		}
		s.Attempts = v
	}
	for _, child := range n.Children() {
		switch child.Name() {
		case "run":
			cmd, req, err := buildRun(child)
			if err != nil {
				return Step{}, err
			}
			s.Command, s.Request = cmd, req
		case "entry":
			raw, err := buildEntry(child)
			if err != nil {
				return Step{}, err
			}
			s.Entry = raw
		case "cwd":
			v, err := compileTextElem(child)
			if err != nil {
				return Step{}, err
			}
			s.Cwd = v
		case "stdin":
			v, err := compileTextElem(child)
			if err != nil {
				return Step{}, err
			}
			s.Stdin = v
		default:
			return Step{}, fmt.Errorf("<step %q>: unexpected child element <%s>", s.Name, child.Name())
		}
	}
	return s, nil
}

func buildFormatRef(n *xnode) (*FormatRef, error) {
	if err := checkAttrs(n, "ref", "input", "when"); err != nil {
		return nil, err
	}
	if ref := n.Attr("ref"); ref != "" {
		return &FormatRef{Name: ref}, nil
	}
	f, err := buildFormat(n)
	if err != nil {
		return nil, err
	}
	return &FormatRef{Inline: f}, nil
}

// buildEntry converts an <entry> element into JSON (a json.RawMessage whose
// string leaves are templates, rendered later by renderEntry).
func buildEntry(n *xnode) (json.RawMessage, error) {
	if err := checkAttrs(n); err != nil {
		return nil, err
	}
	val, err := entryObject(n)
	if err != nil {
		return nil, err
	}
	return json.Marshal(val)
}

func entryObject(n *xnode) (map[string]any, error) {
	out := map[string]any{}
	for _, child := range n.Children() {
		v, err := entryValue(child)
		if err != nil {
			return nil, err
		}
		out[child.Name()] = v
	}
	return out, nil
}

// entryValue maps one entry element to a Go value:
//   - children that are all <param>      -> a map (name -> template string)
//   - other structural child elements    -> a nested object
//   - otherwise (text / placeholders)     -> a template string
func entryValue(n *xnode) (any, error) {
	var structural []*xnode
	for _, c := range n.Children() {
		if !isPlaceholder(c.Name()) {
			structural = append(structural, c)
		}
	}
	if len(structural) == 0 {
		return compileContent(n)
	}
	allParams := true
	for _, c := range structural {
		if c.Name() != "param" {
			allParams = false
			break
		}
	}
	if allParams {
		m := map[string]any{}
		for _, c := range structural {
			if err := checkAttrs(c, "name"); err != nil {
				return nil, err
			}
			name := c.Attr("name")
			if name == "" {
				return nil, fmt.Errorf("<param>: name= is required")
			}
			s, err := compileContent(c)
			if err != nil {
				return nil, err
			}
			m[name] = s
		}
		return m, nil
	}
	return entryObject(n)
}
