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

// TMLField maps one part of a list element to one property of the item
// template.
//
// Path reads a value out of the element. Expr computes one instead, against the
// element promoted to the top level and the whole context through `$`, exactly
// as a <field expr=> does.
//
// Lines turns the value into a list of strings, which is the property type a
// data template declares as string[] and walks with <For>. A log is the reason
// it exists: the value is one blob of output, and the card shows the tail of
// it. Last keeps the final N entries, and Truncate clips each one to a width
// the card can hold, because TML does no wrapping of its own.
// Last and Truncate are templates, so a flag sets them: `last="{{ .flag.lines }}"`.
type TMLField struct {
	Name     string `json:"name"`
	Path     string `json:"path,omitempty"`
	Expr     string `json:"expr,omitempty"`
	Lines    bool   `json:"lines,omitempty"`
	Last     string `json:"last,omitempty"`
	Truncate string `json:"truncate,omitempty"`
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
	p := TMLProp{
		Name: strings.TrimSpace(n.Attr("name")),
		From: strings.TrimSpace(n.Attr("from")),
		Over: strings.TrimSpace(n.Attr("over")),
	}
	// A prop holds EITHER fields or text. The two cannot be read in one pass:
	// the placeholder compiler reads content it knows, and <field> is this
	// element's own child rather than a placeholder, so it rejects one.
	if repeats(n) {
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
		return p, nil
	}
	text, err := compileContent(n)
	if err != nil {
		return TMLProp{}, err
	}
	p.Text = strings.TrimSpace(text)
	return p, nil
}

// repeats reports whether a prop names the parts of a list element rather than
// one value.
func repeats(n *xnode) bool {
	for _, child := range n.Children() {
		if child.Name() == "field" {
			return true
		}
	}
	return false
}

func buildTMLField(n *xnode) (TMLField, error) {
	if err := checkAttrs(n, "name", "expr", "lines", "last", "truncate"); err != nil {
		return TMLField{}, err
	}
	path, err := textOf(n)
	if err != nil {
		return TMLField{}, err
	}
	return TMLField{
		Name:     strings.TrimSpace(n.Attr("name")),
		Path:     strings.TrimSpace(path),
		Expr:     n.Attr("expr"),
		Lines:    n.Attr("lines") == "true",
		Last:     strings.TrimSpace(n.Attr("last")),
		Truncate: strings.TrimSpace(n.Attr("truncate")),
	}, nil
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
	for i, element := range list {
		record := make(map[string]sema.Value, len(p.Fields))
		for _, f := range p.Fields {
			value, err := tmlFieldValue(f, element, i, ctx)
			if err != nil {
				return sema.Value{}, fmt.Errorf("field %q: %w", f.Name, err)
			}
			record[f.Name] = value
		}
		records = append(records, record)
	}
	return sema.RecordListValue(records), nil
}

// tmlFieldValue reads one part of one list element.
func tmlFieldValue(f TMLField, element any, index int, ctx map[string]any) (sema.Value, error) {
	raw := ""
	if f.Expr != "" {
		out, err := renderString(f.Expr, tmlExprData(element, index, ctx))
		if err != nil {
			return sema.Value{}, err
		}
		raw = out
	} else {
		path := f.Path
		if strings.TrimSpace(path) == "" {
			path = f.Name
		}
		found := fields.LookupValue(element, path)
		if !f.Lines {
			return tmlScalarValue(found), nil
		}
		raw = tmlScalar(found)
	}
	if !f.Lines {
		return sema.StringValue(raw), nil
	}
	last, err := tmlCount(f.Last, "last", ctx)
	if err != nil {
		return sema.Value{}, err
	}
	truncate, err := tmlCount(f.Truncate, "truncate", ctx)
	if err != nil {
		return sema.Value{}, err
	}
	return tmlLines(raw, last, truncate), nil
}

// tmlCount renders a last= or truncate= template and reads the number out of
// it. An empty value means no limit.
func tmlCount(tmpl, attr string, ctx map[string]any) (int, error) {
	if tmpl == "" {
		return 0, nil
	}
	out, err := renderString(tmpl, ctx)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", attr, err)
	}
	out = strings.TrimSpace(out)
	if out == "" {
		return 0, nil
	}
	n, err := strconv.Atoi(out)
	if err != nil || n < 0 {
		return 0, fmt.Errorf("%s wants a number, got %q", attr, out)
	}
	return n, nil
}

// tmlExprData is the data a field expr sees: the whole leaf context, the
// element's own keys promoted to the top level, and the element's position. So
// `.stage` is this element and `$.var.x` is still the run.
func tmlExprData(element any, index int, ctx map[string]any) map[string]any {
	data := make(map[string]any, len(ctx)+4)
	for k, v := range ctx {
		data[k] = v
	}
	data["item"] = element
	data["index"] = index
	// The element's own keys land last, so an element that already carries an
	// `item` keeps it. A repeated step builds exactly that shape.
	if m, ok := element.(map[string]any); ok {
		for k, v := range m {
			data[k] = v
		}
	}
	return data
}

// tmlLines cuts one blob of output into the lines a card shows: the last few,
// each clipped to the width the card can hold.
func tmlLines(s string, last, truncate int) sema.Value {
	split := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(split) == 1 && strings.TrimSpace(split[0]) == "" {
		split = nil
	}
	if last > 0 && len(split) > last {
		split = split[len(split)-last:]
	}
	if truncate > 0 {
		for i, line := range split {
			split[i] = truncateCells(line, truncate)
		}
	}
	return sema.ListValue(split)
}

// truncateCells clips a line to n display cells, ellipsis included. It counts
// cells rather than bytes, because a wide rune costs two columns of the card.
func truncateCells(s string, n int) string {
	if fields.DisplayWidth(s) <= n {
		return s
	}
	if n <= 1 {
		return "…"
	}
	out := make([]rune, 0, n)
	width := 0
	for _, r := range s {
		w := fields.DisplayWidth(string(r))
		if width+w > n-1 {
			break
		}
		out = append(out, r)
		width += w
	}
	return string(out) + "…"
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
