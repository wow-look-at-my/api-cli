package main

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"
)

// placeholderNames are the element names treated as inline template
// placeholders (as opposed to structural children).
var placeholderNames = map[string]bool{"value": true, "if": true, "for": true, "else": true}

// compileContent compiles an element's mixed content (text + <value>/<if>/<for>)
// into a single Go template string.
func compileContent(n *xnode) (string, error) {
	return compileItems(n.content)
}

func compileItems(items []xitem) (string, error) {
	var b strings.Builder
	for _, it := range items {
		if it.elem == nil {
			b.WriteString(cleanText(it.text))
			continue
		}
		var (
			s   string
			err error
		)
		switch it.elem.name {
		case "value":
			s, err = compileValue(it.elem)
		case "if":
			s, err = compileIf(it.elem)
		case "for":
			s, err = compileFor(it.elem)
		default:
			return "", fmt.Errorf("unexpected element <%s> in text content", it.elem.name)
		}
		if err != nil {
			return "", err
		}
		b.WriteString(s)
	}
	return b.String(), nil
}

// cleanText normalizes a text chunk. Whitespace-only chunks that span a line
// break are structural indentation and are dropped; inline whitespace (e.g. the
// space in "Bearer ") is preserved. Multi-line content is dedented of its
// common leading tabs (structural indentation is tabs, per the format).
func cleanText(s string) string {
	if strings.TrimSpace(s) == "" {
		if strings.ContainsAny(s, "\n\r") {
			return ""
		}
		return s
	}
	if strings.Contains(s, "\n") {
		return dedentTabs(s)
	}
	return s
}

// dedentTabs removes the common leading-tab indentation from a multi-line
// block and trims a leading/trailing blank line.
func dedentTabs(s string) string {
	lines := strings.Split(s, "\n")
	for len(lines) > 0 && strings.TrimSpace(lines[0]) == "" {
		lines = lines[1:]
	}
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	min := -1
	for _, ln := range lines {
		if strings.TrimSpace(ln) == "" {
			continue
		}
		n := 0
		for n < len(ln) && ln[n] == '\t' {
			n++
		}
		if min < 0 || n < min {
			min = n
		}
	}
	if min > 0 {
		for i, ln := range lines {
			if len(ln) >= min {
				lines[i] = ln[min:]
			}
		}
	}
	return strings.Join(lines, "\n")
}

// dotPath turns a context path ("var.base_url") into a template expression that
// reads it. All-identifier paths use the readable field form (".var.base_url",
// graceful on missing keys under missingkey=zero). A path with a non-identifier
// segment (e.g. a kebab-case flag "flag.dry-run") falls back to a parenthesized
// index expression, which accepts arbitrary keys and works on map[string]any as
// well as map[string]string (e.g. .env). Either form is safe used bare after a
// function name or inside parentheses.
func dotPath(path string) string {
	path = strings.TrimSpace(path)
	segs := strings.Split(path, ".")
	allIdent := true
	for _, s := range segs {
		if !isTemplateIdent(s) {
			allIdent = false
			break
		}
	}
	if allIdent {
		return "." + path
	}
	var b strings.Builder
	b.WriteString("(index .")
	for _, s := range segs {
		b.WriteByte(' ')
		b.WriteString(strconv.Quote(s))
	}
	b.WriteString(")")
	return b.String()
}

// isTemplateIdent reports whether s is a valid Go template field identifier.
func isTemplateIdent(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		if r == '_' || unicode.IsLetter(r) {
			continue
		}
		if i > 0 && unicode.IsDigit(r) {
			continue
		}
		return false
	}
	return true
}

func compileValue(n *xnode) (string, error) {
	if err := checkAttrs(n, "name", "expr", "default", "as"); err != nil {
		return "", err
	}
	name, expr := n.attr("name"), n.attr("expr")
	def, as := n.attr("default"), n.attr("as")
	if (name == "") == (expr == "") {
		return "", fmt.Errorf("<value>: exactly one of name= or expr= is required")
	}
	if expr != "" {
		if def != "" || as != "" {
			return "", fmt.Errorf("<value expr=>: cannot combine with default= or as=")
		}
		return expr, nil
	}
	core := dotPath(name)
	if def != "" {
		core = core + " | default " + strconv.Quote(def)
	}
	if as != "" {
		core = as + " (" + core + ")"
	}
	return "{{ " + core + " }}", nil
}

func compileIf(n *xnode) (string, error) {
	if err := checkAttrs(n, "test", "eq"); err != nil {
		return "", err
	}
	test := n.attr("test")
	if test == "" {
		return "", fmt.Errorf("<if>: test= is required")
	}
	var thenItems, elseItems []xitem
	cur := &thenItems
	seenElse := false
	for _, it := range n.content {
		if it.elem != nil && it.elem.name == "else" {
			if seenElse {
				return "", fmt.Errorf("<if>: at most one <else/> is allowed")
			}
			if err := checkAttrs(it.elem); err != nil {
				return "", err
			}
			seenElse = true
			cur = &elseItems
			continue
		}
		*cur = append(*cur, it)
	}
	thenStr, err := compileItems(thenItems)
	if err != nil {
		return "", err
	}
	elseStr, err := compileItems(elseItems)
	if err != nil {
		return "", err
	}
	var cond string
	if n.hasAttr("eq") {
		cond = fmt.Sprintf("eq (printf \"%%v\" %s) %s", dotPath(test), strconv.Quote(n.attr("eq")))
	} else {
		cond = "truthy " + dotPath(test)
	}
	if seenElse {
		return fmt.Sprintf("{{ if %s }}%s{{ else }}%s{{ end }}", cond, thenStr, elseStr), nil
	}
	return fmt.Sprintf("{{ if %s }}%s{{ end }}", cond, thenStr), nil
}

// compileFor compiles <for each="path">...</for> to a range that rebinds "."
// to each element, so a nested <value name="field"/> reads the element's field
// and <value expr="{{ . }}"/> reads a scalar element.
func compileFor(n *xnode) (string, error) {
	if err := checkAttrs(n, "each"); err != nil {
		return "", err
	}
	each := n.attr("each")
	if each == "" {
		return "", fmt.Errorf("<for>: each= is required")
	}
	body, err := compileItems(n.content)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("{{ range %s }}%s{{ end }}", dotPath(each), body), nil
}

// compileTextElem rejects attributes on a content-only element, then compiles
// its mixed content. Used for <url>/<body>/<cwd>/<stdin>/<confirm>/<argv>/
// <precondition>, none of which take attributes.
func compileTextElem(n *xnode) (string, error) {
	if err := checkAttrs(n); err != nil {
		return "", err
	}
	return compileContent(n)
}

// textOf returns the concatenated plain text of an element, rejecting child
// elements. Used where content must be literal (descriptions, field paths).
func textOf(n *xnode) (string, error) {
	var b strings.Builder
	for _, it := range n.content {
		if it.elem != nil {
			return "", fmt.Errorf("<%s>: unexpected child element <%s>", n.name, it.elem.name)
		}
		b.WriteString(cleanText(it.text))
	}
	return strings.TrimSpace(b.String()), nil
}
