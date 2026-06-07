package main

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// parseConfigXML maps an XML config document to the Config model.
//
// The document is first tokenized into a small order-preserving DOM (xnode, see
// xmldom.go), then walked to build the structs. Node placeholders
// (<value>/<if>/<for>) that appear in element content are compiled to Go
// text/template source (xmlcompile.go), so the rest of the runtime renders them
// exactly like any other template string. Unknown elements and attributes are
// rejected to catch typos early.
func parseConfigXML(src []byte) (*Config, error) {
	root, err := parseDOM(src)
	if err != nil {
		return nil, err
	}
	if root.name != "config" {
		return nil, fmt.Errorf("root element must be <config>, got <%s>", root.name)
	}
	return buildConfig(root)
}

func buildConfig(root *xnode) (*Config, error) {
	if err := checkAttrs(root, "name", "schema"); err != nil {
		return nil, err
	}
	cfg := &Config{Name: root.attr("name"), Schema: root.attr("schema")}
	for _, child := range root.children() {
		switch child.name {
		case "description":
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
			s, err := compileContent(child)
			if err != nil {
				return nil, err
			}
			cfg.Cwd = s
		case "stdin":
			s, err := compileContent(child)
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
		case "command":
			c, err := buildCommandNode(child)
			if err != nil {
				return nil, err
			}
			cfg.Commands = append(cfg.Commands, *c)
		default:
			return nil, fmt.Errorf("<config>: unexpected child element <%s>", child.name)
		}
	}
	return cfg, nil
}

func buildVars(n *xnode) (map[string]any, error) {
	out := map[string]any{}
	for _, child := range n.children() {
		if child.name != "var" {
			return nil, fmt.Errorf("<vars>: unexpected child element <%s>", child.name)
		}
		if err := checkAttrs(child, "name"); err != nil {
			return nil, err
		}
		name := child.attr("name")
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
	elems := n.children()
	for _, e := range elems {
		if e.name == "request" {
			if len(elems) != 1 {
				return nil, nil, fmt.Errorf("<run>: <request> must be the only child")
			}
			req, err := buildRequest(e)
			return nil, req, err
		}
	}
	hasArgv := false
	for _, e := range elems {
		if e.name == "argv" {
			hasArgv = true
			break
		}
	}
	if hasArgv {
		var argv []string
		for _, e := range elems {
			if e.name != "argv" {
				return nil, nil, fmt.Errorf("<run>: cannot mix <argv> with <%s>", e.name)
			}
			s, err := compileContent(e)
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

func buildRequest(n *xnode) (*Request, error) {
	if err := checkAttrs(n, "method"); err != nil {
		return nil, err
	}
	req := &Request{Method: strings.TrimSpace(n.attr("method"))}
	if req.Method == "" {
		req.Method = "GET"
	}
	for _, child := range n.children() {
		switch child.name {
		case "url":
			s, err := compileContent(child)
			if err != nil {
				return nil, err
			}
			req.URL = strings.TrimSpace(s)
		case "query":
			if err := buildQuery(child, req); err != nil {
				return nil, err
			}
		case "header":
			h, err := buildHeader(child, "")
			if err != nil {
				return nil, err
			}
			req.Headers = append(req.Headers, h)
		case "if":
			if err := checkAttrs(child, "test"); err != nil {
				return nil, err
			}
			test := child.attr("test")
			for _, inner := range child.children() {
				if inner.name != "header" {
					return nil, fmt.Errorf("<request><if>: only <header> children are supported, got <%s>", inner.name)
				}
				h, err := buildHeader(inner, test)
				if err != nil {
					return nil, err
				}
				req.Headers = append(req.Headers, h)
			}
		case "body":
			s, err := compileContent(child)
			if err != nil {
				return nil, err
			}
			req.Body = s
		case "response":
			if err := checkAttrs(child, "jq"); err != nil {
				return nil, err
			}
			req.Response = &Response{JQ: strings.TrimSpace(child.attr("jq"))}
		default:
			return nil, fmt.Errorf("<request>: unexpected child element <%s>", child.name)
		}
	}
	return req, nil
}

func buildHeader(n *xnode, when string) (Header, error) {
	if err := checkAttrs(n, "name"); err != nil {
		return Header{}, err
	}
	name := n.attr("name")
	if name == "" {
		return Header{}, fmt.Errorf("<header>: name= is required")
	}
	val, err := compileContent(n)
	if err != nil {
		return Header{}, err
	}
	return Header{Name: name, Value: val, When: when}, nil
}

func buildQuery(n *xnode, req *Request) error {
	if err := checkAttrs(n, "from"); err != nil {
		return err
	}
	req.QueryFrom = strings.TrimSpace(n.attr("from"))
	for _, child := range n.children() {
		switch child.name {
		case "param":
			p, err := buildParam(child, "")
			if err != nil {
				return err
			}
			req.Query = append(req.Query, p)
		case "if":
			if err := checkAttrs(child, "test"); err != nil {
				return err
			}
			test := child.attr("test")
			for _, inner := range child.children() {
				if inner.name != "param" {
					return fmt.Errorf("<query><if>: only <param> children are supported, got <%s>", inner.name)
				}
				p, err := buildParam(inner, test)
				if err != nil {
					return err
				}
				req.Query = append(req.Query, p)
			}
		default:
			return fmt.Errorf("<query>: unexpected child element <%s>", child.name)
		}
	}
	return nil
}

func buildParam(n *xnode, when string) (Param, error) {
	if err := checkAttrs(n, "name"); err != nil {
		return Param{}, err
	}
	name := n.attr("name")
	if name == "" {
		return Param{}, fmt.Errorf("<param>: name= is required")
	}
	val, err := compileContent(n)
	if err != nil {
		return Param{}, err
	}
	return Param{Name: name, Value: val, When: when}, nil
}

func buildFields(n *xnode) (*Fields, error) {
	if err := checkAttrs(n, "over", "footer"); err != nil {
		return nil, err
	}
	f := &Fields{Over: strings.TrimSpace(n.attr("over")), Footer: n.attr("footer")}
	for _, child := range n.children() {
		if child.name != "field" {
			return nil, fmt.Errorf("<fields>: unexpected child element <%s>", child.name)
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
	name := n.attr("name")
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
		Expr:      n.attr("expr"),
		Default:   n.attr("default"),
		FirstLine: n.attr("firstline") == "true",
		ShowIn:    strings.TrimSpace(n.attr("show_in")),
	}
	if fld.Expr != "" && fld.Path != "" {
		return Field{}, fmt.Errorf("<field %q>: cannot set both a source path and expr=", name)
	}
	if fld.Expr == "" && fld.Path == "" {
		return Field{}, fmt.Errorf("<field %q>: needs a source path or expr=", name)
	}
	if t := n.attr("truncate"); t != "" {
		v, err := strconv.Atoi(t)
		if err != nil {
			return Field{}, fmt.Errorf("<field %q>: truncate=%q must be an integer", name, t)
		}
		fld.Truncate = v
	}
	if p := n.attr("priority"); p != "" {
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
	for _, child := range n.children() {
		if child.name != "format" {
			return nil, fmt.Errorf("<formats>: unexpected child element <%s>", child.name)
		}
		if err := checkAttrs(child, "name", "input", "when"); err != nil {
			return nil, err
		}
		name := child.attr("name")
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
	f := &Format{Input: n.attr("input"), When: n.attr("when")}
	for _, child := range n.children() {
		if child.name != "view" {
			return nil, fmt.Errorf("<format>: unexpected child element <%s>", child.name)
		}
		if err := checkAttrs(child, "name", "when", "default"); err != nil {
			return nil, err
		}
		tmpl, err := compileContent(child)
		if err != nil {
			return nil, err
		}
		f.Views = append(f.Views, View{
			Name:     child.attr("name"),
			When:     child.attr("when"),
			Default:  child.attr("default") == "true",
			Template: tmpl,
		})
	}
	return f, nil
}

func buildCommandNode(n *xnode) (*Command, error) {
	if err := checkAttrs(n, "name", "description", "passthrough", "confirm"); err != nil {
		return nil, err
	}
	c := &Command{
		Name:        n.attr("name"),
		Description: n.attr("description"),
		Passthrough: n.attr("passthrough") == "true",
		Confirm:     n.attr("confirm"),
	}
	for _, child := range n.children() {
		if err := addCommandChild(c, child); err != nil {
			return nil, err
		}
	}
	return c, nil
}

// addCommandChild dispatches one child element of a <command> into the Command.
func addCommandChild(c *Command, child *xnode) error {
	switch child.name {
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
		s, err := compileContent(child)
		if err != nil {
			return err
		}
		c.Cwd = s
	case "stdin":
		s, err := compileContent(child)
		if err != nil {
			return err
		}
		c.Stdin = s
	case "confirm":
		s, err := compileContent(child)
		if err != nil {
			return err
		}
		c.Confirm = s
	case "preconditions":
		for _, p := range child.children() {
			if p.name != "precondition" {
				return fmt.Errorf("<preconditions>: unexpected child element <%s>", p.name)
			}
			s, err := compileContent(p)
			if err != nil {
				return err
			}
			c.Preconditions = append(c.Preconditions, s)
		}
	case "steps":
		for _, s := range child.children() {
			if s.name != "step" {
				return fmt.Errorf("<steps>: unexpected child element <%s>", s.name)
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
		c.Fields = f
	case "format":
		ref, err := buildFormatRef(child)
		if err != nil {
			return err
		}
		c.Format = ref
	case "command":
		sub, err := buildCommandNode(child)
		if err != nil {
			return err
		}
		c.Commands = append(c.Commands, *sub)
	default:
		return fmt.Errorf("<command %q>: unexpected child element <%s>", c.Name, child.name)
	}
	return nil
}

func buildArg(n *xnode) (Arg, error) {
	if err := checkAttrs(n, "name", "type", "required", "variadic", "description"); err != nil {
		return Arg{}, err
	}
	return Arg{
		Name:        n.attr("name"),
		Type:        n.attr("type"),
		Required:    n.attr("required") == "true",
		Variadic:    n.attr("variadic") == "true",
		Description: n.attr("description"),
	}, nil
}

func buildFlag(n *xnode) (Flag, error) {
	if err := checkAttrs(n, "name", "short", "type", "default", "required", "conflicts", "description"); err != nil {
		return Flag{}, err
	}
	fl := Flag{
		Name:        n.attr("name"),
		Short:       n.attr("short"),
		Type:        n.attr("type"),
		Required:    n.attr("required") == "true",
		Description: n.attr("description"),
	}
	if c := strings.TrimSpace(n.attr("conflicts")); c != "" {
		for _, p := range strings.Split(c, ",") {
			if p = strings.TrimSpace(p); p != "" {
				fl.Conflicts = append(fl.Conflicts, p)
			}
		}
	}
	if n.hasAttr("default") {
		def := n.attr("default")
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
	if err := checkAttrs(n, "name", "when"); err != nil {
		return Step{}, err
	}
	s := Step{Name: n.attr("name"), When: n.attr("when")}
	for _, child := range n.children() {
		switch child.name {
		case "run":
			cmd, req, err := buildRun(child)
			if err != nil {
				return Step{}, err
			}
			if req != nil {
				return Step{}, fmt.Errorf("<step %q>: <request> is not supported in steps; use a command", s.Name)
			}
			s.Command = cmd
		case "entry":
			raw, err := buildEntry(child)
			if err != nil {
				return Step{}, err
			}
			s.Entry = raw
		case "cwd":
			v, err := compileContent(child)
			if err != nil {
				return Step{}, err
			}
			s.Cwd = v
		case "stdin":
			v, err := compileContent(child)
			if err != nil {
				return Step{}, err
			}
			s.Stdin = v
		default:
			return Step{}, fmt.Errorf("<step %q>: unexpected child element <%s>", s.Name, child.name)
		}
	}
	return s, nil
}

func buildFormatRef(n *xnode) (*FormatRef, error) {
	if err := checkAttrs(n, "ref", "input", "when"); err != nil {
		return nil, err
	}
	if ref := n.attr("ref"); ref != "" {
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
	val, err := entryObject(n)
	if err != nil {
		return nil, err
	}
	return json.Marshal(val)
}

func entryObject(n *xnode) (map[string]any, error) {
	out := map[string]any{}
	for _, child := range n.children() {
		v, err := entryValue(child)
		if err != nil {
			return nil, err
		}
		out[child.name] = v
	}
	return out, nil
}

// entryValue maps one entry element to a Go value:
//   - children that are all <param>      -> a map (name -> template string)
//   - other structural child elements    -> a nested object
//   - otherwise (text / placeholders)     -> a template string
func entryValue(n *xnode) (any, error) {
	var structural []*xnode
	for _, c := range n.children() {
		if !placeholderNames[c.name] {
			structural = append(structural, c)
		}
	}
	if len(structural) == 0 {
		return compileContent(n)
	}
	allParams := true
	for _, c := range structural {
		if c.name != "param" {
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
			name := c.attr("name")
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
