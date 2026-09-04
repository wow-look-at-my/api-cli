package main

// The <request> half of the XML source: the element itself, its query and its
// headers. The rest of the config tree is built in xmlsource.go.

import (
	"fmt"
	"strconv"
	"strings"
)

// parseAllowStatus reads an allow-status attribute: a comma-separated list of
// HTTP status codes that must not fail the request. It rejects anything that is
// not a status a server can send, and anything below 400, which never fails.
func parseAllowStatus(raw string) ([]int, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	var out []int
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		n, err := strconv.Atoi(part)
		if err != nil {
			return nil, fmt.Errorf("<request allow-status=%q>: %q is not a status code", raw, part)
		}
		if n < 400 || n > 599 {
			return nil, fmt.Errorf("<request allow-status=%q>: %d is not an error status (400-599)", raw, n)
		}
		out = append(out, n)
	}
	return out, nil
}

func buildRequest(n *xnode) (*Request, error) {
	if err := checkAttrs(n, "method", "transport", "allow-status"); err != nil {
		return nil, err
	}
	allowed, err := parseAllowStatus(n.Attr("allow-status"))
	if err != nil {
		return nil, err
	}
	req := &Request{
		Method:      strings.TrimSpace(n.Attr("method")),
		Transport:   strings.TrimSpace(n.Attr("transport")),
		AllowStatus: allowed,
	}
	if req.Method == "" {
		req.Method = "GET"
	}
	for _, child := range n.Children() {
		switch child.Name() {
		case "url":
			s, err := compileTextElem(child)
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
			test := child.Attr("test")
			for _, inner := range child.Children() {
				if inner.Name() != "header" {
					return nil, fmt.Errorf("<request><if>: only <header> children are supported, got <%s>", inner.Name())
				}
				h, err := buildHeader(inner, test)
				if err != nil {
					return nil, err
				}
				req.Headers = append(req.Headers, h)
			}
		case "body":
			s, err := compileTextElem(child)
			if err != nil {
				return nil, err
			}
			req.Body = s
		case "response":
			if err := checkAttrs(child, "jq"); err != nil {
				return nil, err
			}
			req.Response = &Response{JQ: strings.TrimSpace(child.Attr("jq"))}
		default:
			return nil, fmt.Errorf("<request>: unexpected child element <%s>", child.Name())
		}
	}
	return req, nil
}

func buildHeader(n *xnode, when string) (Header, error) {
	if err := checkAttrs(n, "name"); err != nil {
		return Header{}, err
	}
	name := n.Attr("name")
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
	req.QueryFrom = strings.TrimSpace(n.Attr("from"))
	for _, child := range n.Children() {
		switch child.Name() {
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
			test := child.Attr("test")
			for _, inner := range child.Children() {
				if inner.Name() != "param" {
					return fmt.Errorf("<query><if>: only <param> children are supported, got <%s>", inner.Name())
				}
				p, err := buildParam(inner, test)
				if err != nil {
					return err
				}
				req.Query = append(req.Query, p)
			}
		default:
			return fmt.Errorf("<query>: unexpected child element <%s>", child.Name())
		}
	}
	return nil
}

func buildParam(n *xnode, when string) (Param, error) {
	if err := checkAttrs(n, "name"); err != nil {
		return Param{}, err
	}
	name := n.Attr("name")
	if name == "" {
		return Param{}, fmt.Errorf("<param>: name= is required")
	}
	val, err := compileContent(n)
	if err != nil {
		return Param{}, err
	}
	return Param{Name: name, Value: val, When: when}, nil
}
