package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/wow-look-at-my/api-cli/fields"
	"github.com/wow-look-at-my/tml"
	"github.com/wow-look-at-my/tml/sema"
)

// TML presents a leaf through a Terminal Markup Language component. The leaf
// still runs its command or its request; this declares what the result looks
// like on screen, in place of <fields>.
type TML struct {
	Src   string    `json:"src"`
	Dark  bool      `json:"dark,omitempty"`
	Props []TMLProp `json:"props,omitempty"`
}

// TMLProp is one argument to the entry component. A component declares its
// properties and rejects an argument it never declared, so the config names
// every value it passes rather than offering the whole response and hoping.
//
// Text is a template. From reads one value out of the response. Over reads a
// list and Fields say which part of each element the item template gets: an
// element carries exactly the fields named here, because a data template
// rejects a field it did not declare.
type TMLProp struct {
	Name   string     `json:"name"`
	Text   string     `json:"text,omitempty"`
	From   string     `json:"from,omitempty"`
	Over   string     `json:"over,omitempty"`
	Fields []TMLField `json:"fields,omitempty"`
}

// TMLField maps one path inside a list element to one property of the item
// template.
type TMLField struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

// Defined reports whether a leaf presents itself through TML.
func (t *TML) Defined() bool { return t != nil && strings.TrimSpace(t.Src) != "" }

// configDir is the directory the running config was read from. It is published
// the way the transport registry is, by every path that turns a config into
// runnable commands, because a leaf resolves its src against it.
var configDir string

func installConfigDir(cfg *Config) {
	configDir = ""
	if cfg != nil {
		configDir = cfg.Dir
	}
}

// buildTML reads a <tml src="ui/app.tml"> element and its <prop> children.
func buildTML(n *xnode) (*TML, error) {
	if err := checkAttrs(n, "src", "dark"); err != nil {
		return nil, err
	}
	t := &TML{Src: strings.TrimSpace(n.Attr("src")), Dark: n.Attr("dark") == "true"}
	for _, child := range n.Children() {
		if child.Name() != "prop" {
			return nil, fmt.Errorf("<tml>: unexpected child element <%s>", child.Name())
		}
		prop, err := buildTMLProp(child)
		if err != nil {
			return nil, err
		}
		t.Props = append(t.Props, prop)
	}
	return t, nil
}

func buildTMLProp(n *xnode) (TMLProp, error) {
	if err := checkAttrs(n, "name", "from", "over"); err != nil {
		return TMLProp{}, err
	}
	text, err := compileContent(n)
	if err != nil {
		return TMLProp{}, err
	}
	p := TMLProp{
		Name: strings.TrimSpace(n.Attr("name")),
		From: strings.TrimSpace(n.Attr("from")),
		Over: strings.TrimSpace(n.Attr("over")),
	}
	for _, child := range n.Children() {
		if child.Name() != "field" {
			return TMLProp{}, fmt.Errorf("<prop %q>: unexpected child element <%s>", p.Name, child.Name())
		}
		field, err := buildTMLField(child)
		if err != nil {
			return TMLProp{}, err
		}
		p.Fields = append(p.Fields, field)
	}
	// A prop that repeats holds its fields as elements, so the element's own
	// text is theirs rather than a value of its own.
	if len(p.Fields) == 0 {
		p.Text = strings.TrimSpace(text)
	}
	return p, nil
}

func buildTMLField(n *xnode) (TMLField, error) {
	if err := checkAttrs(n, "name"); err != nil {
		return TMLField{}, err
	}
	path, err := textOf(n)
	if err != nil {
		return TMLField{}, err
	}
	return TMLField{Name: strings.TrimSpace(n.Attr("name")), Path: strings.TrimSpace(path)}, nil
}

func validateTML(t *TML, where string) error {
	if !t.Defined() {
		return fmt.Errorf("%s: <tml> needs a \"src\"", where)
	}
	for i, p := range t.Props {
		if strings.TrimSpace(p.Name) == "" {
			return fmt.Errorf("%s: <prop> %d has no \"name\"", where, i)
		}
		sources := 0
		for _, set := range []bool{p.Text != "", p.From != "", p.Over != ""} {
			if set {
				sources++
			}
		}
		if sources != 1 {
			return fmt.Errorf("%s: prop %q takes exactly one of text, \"from\" or \"over\"", where, p.Name)
		}
		if p.Over != "" && len(p.Fields) == 0 {
			return fmt.Errorf("%s: prop %q reads a list and needs at least one <field>", where, p.Name)
		}
		if p.Over == "" && len(p.Fields) > 0 {
			return fmt.Errorf("%s: prop %q has fields but no \"over\" to read them from", where, p.Name)
		}
		for j, f := range p.Fields {
			if strings.TrimSpace(f.Name) == "" {
				return fmt.Errorf("%s: prop %q field %d has no \"name\"", where, p.Name, j)
			}
		}
	}
	return nil
}

// tmlEntry resolves a src against the directory the config was loaded from, so
// a relative path means what the author sees next to the config file.
func tmlEntry(src, dir string) string {
	if filepath.IsAbs(src) || dir == "" {
		return src
	}
	return filepath.Join(dir, src)
}

// loaded caches views by entry path, because a watch renders the same component
// many times and every Load re-reads and re-checks the whole import graph. The
// entry file's size and modification time key the cache, so editing the file
// mid-watch reloads it. An imported file is NOT part of the key: change one of
// those and restart.
var loaded sync.Map

type loadKey struct {
	path string
	dark bool
	mod  int64
	size int64
}

func loadTMLView(path string, dark bool) (*tml.View, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("open view %q: %w", path, err)
	}
	key := loadKey{path: path, dark: dark, mod: info.ModTime().UnixNano(), size: info.Size()}
	if cached, ok := loaded.Load(key); ok {
		return cached.(*tml.View), nil
	}
	view, err := tml.Load(os.DirFS(filepath.Dir(path)), filepath.Base(path), tml.Options{Dark: dark})
	if err != nil {
		return nil, fmt.Errorf("load view %q: %w", path, err)
	}
	loaded.Store(key, view)
	return view, nil
}

// tmlProps turns the response and the leaf context into the component's
// arguments. Every value crosses as a string and the component re-reads it as
// the type it declared, so an int property takes 3 and a color property takes
// #d97706 without this side naming types of its own.
func tmlProps(t *TML, parsed any, ctx map[string]any) (tml.Props, error) {
	props := make(tml.Props, len(t.Props))
	for _, p := range t.Props {
		value, err := tmlPropValue(p, parsed, ctx)
		if err != nil {
			return nil, fmt.Errorf("prop %q: %w", p.Name, err)
		}
		props[p.Name] = value
	}
	return props, nil
}

func tmlPropValue(p TMLProp, parsed any, ctx map[string]any) (sema.Value, error) {
	switch {
	case p.Text != "":
		out, err := renderString(p.Text, ctx)
		if err != nil {
			return sema.Value{}, err
		}
		return sema.StringValue(out), nil
	case p.From != "":
		found, ok := tmlLookup(p.From, parsed, ctx)
		if !ok {
			return sema.Value{}, fmt.Errorf("%q is not in the response or the context", p.From)
		}
		return tmlScalarValue(found), nil
	default:
		return tmlListValue(p, parsed, ctx)
	}
}

// tmlLookup reads a path out of the response body first and the whole leaf
// context second, which is the order <fields> resolves an over= in.
func tmlLookup(path string, parsed any, ctx map[string]any) (any, bool) {
	if value, ok := fields.Lookup(parsed, path); ok {
		return value, true
	}
	return fields.Lookup(ctx, path)
}

// tmlListValue reads a list and projects each element down to the named fields.
func tmlListValue(p TMLProp, parsed any, ctx map[string]any) (sema.Value, error) {
	found, ok := tmlLookup(p.Over, parsed, ctx)
	if !ok {
		return sema.Value{}, fmt.Errorf("%q is not in the response or the context", p.Over)
	}
	list, ok := found.([]any)
	if !ok {
		return sema.Value{}, fmt.Errorf("%q is %T, and a repeated property needs a list", p.Over, found)
	}
	records := make([]map[string]sema.Value, 0, len(list))
	for _, element := range list {
		record := make(map[string]sema.Value, len(p.Fields))
		for _, f := range p.Fields {
			path := f.Path
			if strings.TrimSpace(path) == "" {
				path = f.Name
			}
			record[f.Name] = tmlScalarValue(fields.LookupValue(element, path))
		}
		records = append(records, record)
	}
	return sema.RecordListValue(records), nil
}

// tmlScalarValue renders one decoded JSON value as the string a component
// re-reads. A missing value is the empty string rather than an error, because a
// row with a null column is data, not a broken config.
func tmlScalarValue(v any) sema.Value { return sema.StringValue(tmlScalar(v)) }

func tmlScalar(v any) string {
	switch value := v.(type) {
	case nil:
		return ""
	case string:
		return value
	case bool:
		return strconv.FormatBool(value)
	case int64:
		return strconv.FormatInt(value, 10)
	case float64:
		return strconv.FormatFloat(value, 'f', -1, 64)
	default:
		return fmt.Sprint(value)
	}
}

// renderTMLFrame lays the component out at one size and returns the frame.
func renderTMLFrame(t *TML, dir string, parsed any, ctx map[string]any, width, height int) (string, error) {
	view, err := loadTMLView(tmlEntry(t.Src, dir), t.Dark)
	if err != nil {
		return "", err
	}
	props, err := tmlProps(t, parsed, ctx)
	if err != nil {
		return "", err
	}
	return view.Render(props, width, height)
}
