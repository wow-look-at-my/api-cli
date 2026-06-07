package main

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
)

// xnode is a parsed XML element. content preserves the original order of text
// chunks and child elements so mixed content (text interleaved with
// placeholders) can be compiled faithfully.
type xnode struct {
	name    string
	attrs   []xattr
	attrMap map[string]string
	content []xitem
}

type xattr struct{ name, value string }

// xitem is one piece of an element's content: either a text chunk (elem == nil)
// or a child element.
type xitem struct {
	text string
	elem *xnode
}

func (n *xnode) attr(name string) string { return n.attrMap[name] }

func (n *xnode) hasAttr(name string) bool { _, ok := n.attrMap[name]; return ok }

// children returns the child elements (text chunks dropped).
func (n *xnode) children() []*xnode {
	var out []*xnode
	for _, it := range n.content {
		if it.elem != nil {
			out = append(out, it.elem)
		}
	}
	return out
}

// parseDOM tokenizes an XML document into an order-preserving DOM. Comments,
// processing instructions, and directives are dropped; CDATA arrives as ordinary
// character data.
func parseDOM(src []byte) (*xnode, error) {
	dec := xml.NewDecoder(bytes.NewReader(stripXMLDecl(src)))
	dec.Strict = true
	var root *xnode
	var stack []*xnode
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("xml: %w", err)
		}
		switch t := tok.(type) {
		case xml.StartElement:
			n := &xnode{name: t.Name.Local, attrMap: map[string]string{}}
			for _, a := range t.Attr {
				// Skip namespace declarations (xmlns / xmlns:*).
				if a.Name.Local == "xmlns" || a.Name.Space == "xmlns" {
					continue
				}
				if _, dup := n.attrMap[a.Name.Local]; dup {
					return nil, fmt.Errorf("<%s>: duplicate attribute %q", n.name, a.Name.Local)
				}
				n.attrs = append(n.attrs, xattr{a.Name.Local, a.Value})
				n.attrMap[a.Name.Local] = a.Value
			}
			if len(stack) > 0 {
				p := stack[len(stack)-1]
				p.content = append(p.content, xitem{elem: n})
			}
			stack = append(stack, n)
		case xml.EndElement:
			top := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			if len(stack) == 0 {
				if root != nil {
					return nil, fmt.Errorf("multiple top-level elements; expected a single <config> root")
				}
				root = top
			}
		case xml.CharData:
			if len(stack) > 0 {
				p := stack[len(stack)-1]
				p.content = append(p.content, xitem{text: string(t)})
			}
		}
	}
	if root == nil {
		return nil, fmt.Errorf("no root element")
	}
	return root, nil
}

// stripXMLDecl removes a leading XML declaration (<?xml ... ?>). Go's
// encoding/xml supports only XML 1.0 and errors on a version="1.1" declaration,
// but the shipped configs declare 1.1 for the stricter xml-validator. The
// declaration carries nothing the loader needs, so we drop it before decoding.
func stripXMLDecl(src []byte) []byte {
	src = bytes.TrimPrefix(src, []byte("\xef\xbb\xbf")) // UTF-8 BOM
	trimmed := bytes.TrimLeft(src, " \t\r\n")
	if !bytes.HasPrefix(trimmed, []byte("<?xml")) {
		return src
	}
	rest := trimmed[len("<?xml"):]
	// Only treat it as the declaration (not another <?xml...?> PI) when the
	// next byte ends the target or is whitespace.
	if len(rest) > 0 && rest[0] != ' ' && rest[0] != '\t' && rest[0] != '\r' && rest[0] != '\n' && rest[0] != '?' {
		return src
	}
	i := bytes.Index(trimmed, []byte("?>"))
	if i < 0 {
		return src
	}
	return trimmed[i+2:]
}

// checkAttrs rejects any attribute on n not in the allowed set.
func checkAttrs(n *xnode, allowed ...string) error {
	allow := make(map[string]bool, len(allowed))
	for _, a := range allowed {
		allow[a] = true
	}
	for _, a := range n.attrs {
		if !allow[a.name] {
			return fmt.Errorf("<%s>: unknown attribute %q", n.name, a.name)
		}
	}
	return nil
}
