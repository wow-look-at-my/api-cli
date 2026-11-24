// decodemarkup.go reads yaml and xml bodies into the shapes JSON decodes to.

package main

import (
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"
	"golang.org/x/text/encoding/htmlindex"
)

// decodeYAML reads a YAML stream. One document is its value, and several are a list.
func decodeYAML(s string) (any, error) {
	dec := yaml.NewDecoder(strings.NewReader(s))
	var docs []any
	for {
		var v any
		err := dec.Decode(&v)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		docs = append(docs, yamlValue(v))
	}
	switch len(docs) {
	case 0:
		return nil, nil
	case 1:
		return docs[0], nil
	}
	return docs, nil
}

// yamlValue narrows a decoded YAML value to the types a JSON body decodes to.
// yamlValue also narrows which are the only ones the templates, jq and the
// formatter expect.
func yamlValue(v any) any {
	switch x := v.(type) {
	case map[string]any:
		for k, val := range x {
			x[k] = yamlValue(val)
		}
		return x
	case map[any]any:
		out := make(map[string]any, len(x))
		for k, val := range x {
			out[fmt.Sprint(k)] = yamlValue(val)
		}
		return out
	case []any:
		for i, val := range x {
			x[i] = yamlValue(val)
		}
		return x
	case int:
		return int64(x)
	case uint64:
		return float64(x)
	case time.Time:
		return x.Format(time.RFC3339Nano)
	case []byte:
		return string(x)
	default:
		return v
	}
}

var (
	xmlDeclaration = regexp.MustCompile(`^\s*<\?xml\s[^>]*\?>`)
	xmlEncoding    = regexp.MustCompile(`encoding\s*=\s*["']([^"']+)["']`)
)

// decodeXML reads an XML document as an object with one key, the root element.
func decodeXML(s string) (any, error) {
	s, err := xmlText(s)
	if err != nil {
		return nil, err
	}
	dec := xml.NewDecoder(strings.NewReader(s))
	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			return nil, errors.New("no root element")
		}
		if err != nil {
			return nil, err
		}
		if start, ok := tok.(xml.StartElement); ok {
			v, err := xmlElement(dec, start)
			if err != nil {
				return nil, err
			}
			return map[string]any{start.Name.Local: v}, nil
		}
	}
}

// xmlText removes the XML declaration and converts the body to UTF-8.
func xmlText(s string) (string, error) {
	s = strings.TrimPrefix(s, byteOrderMark)
	decl := xmlDeclaration.FindString(s)
	if decl == "" {
		return s, nil
	}
	s = s[len(decl):]
	m := xmlEncoding.FindStringSubmatch(decl)
	if m == nil || strings.EqualFold(m[1], "utf-8") {
		return s, nil
	}
	enc, err := htmlindex.Get(m[1])
	if err != nil {
		return "", fmt.Errorf("encoding %q: %v", m[1], err)
	}
	return enc.NewDecoder().String(s)
}

// xmlElement reads the rest of an element. An attribute is "@name", text
// beside attributes or children is "#text", and a child that repeats is a
// list. An element with text only is that string.
func xmlElement(dec *xml.Decoder, start xml.StartElement) (any, error) {
	node := map[string]any{}
	for _, a := range start.Attr {
		if a.Name.Space == "xmlns" || a.Name.Local == "xmlns" {
			continue
		}
		node["@"+a.Name.Local] = a.Value
	}
	var text strings.Builder
	for {
		tok, err := dec.Token()
		if err != nil {
			return nil, err
		}
		switch t := tok.(type) {
		case xml.CharData:
			text.Write(t)
		case xml.StartElement:
			child, err := xmlElement(dec, t)
			if err != nil {
				return nil, err
			}
			name := t.Name.Local
			switch prev := node[name].(type) {
			case nil:
				node[name] = child
			case xmlList:
				node[name] = append(prev, child)
			default:
				node[name] = xmlList{prev, child}
			}
		case xml.EndElement:
			return xmlNode(node, strings.TrimSpace(text.String())), nil
		}
	}
}

// xmlList marks a list this decoder built, apart from a child's own value.
type xmlList []any

// xmlNode finishes an element: its lists become plain lists, and an element
// with nothing but text is the text.
func xmlNode(node map[string]any, text string) any {
	if len(node) == 0 {
		return text
	}
	for k, v := range node {
		if l, ok := v.(xmlList); ok {
			node[k] = []any(l)
		}
	}
	if text != "" {
		node["#text"] = text
	}
	return node
}
