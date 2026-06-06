package main

import (
	"bytes"
	"encoding/json"
	"fmt"

	"gopkg.in/yaml.v3"
)

// configTabWidth is how many spaces each leading tab expands to before the
// document reaches the YAML parser. The exact value is irrelevant to a
// tab-only file (every nesting level is the same multiple of it), but a small
// even number keeps space-authored and tab-authored files visually comparable.
const configTabWidth = 2

// sourceToJSON converts a config source into canonical JSON bytes for the
// strict encoding/json decoder in Load.
//
// Configs are YAML. YAML is a superset of JSON, so existing pure-JSON configs
// pass through unchanged. We deliberately do NOT decode YAML straight into the
// Config structs: those carry `json` tags and custom UnmarshalJSON methods
// (Cmd, FormatRef) that a YAML decoder would ignore. Routing through JSON keeps
// a single source of truth for decoding — unknown-field rejection and the
// string-or-array/string-or-object unmarshalers all keep working.
//
// Leading tabs are expanded to spaces first. The YAML spec forbids tab
// characters in indentation (they render at different widths across systems),
// so yaml.v3 rejects tab-indented documents outright. Expanding the leading
// run of tabs on every line lets configs be authored with tabs anyway: a
// tab-only file is just a space-indented file scaled by configTabWidth, which
// preserves the relative nesting the parser cares about. The trade-off, chosen
// deliberately, is that the on-disk file is no longer something other YAML
// tools (editors, linters, $schema validators) will accept.
//
// Only the leading run of tabs is touched. A literal tab inside a value is
// untouched in the source; author it with the \t escape in a double-quoted
// scalar (YAML's double quotes honor C-style escapes; single quotes and block
// scalars do not).
func sourceToJSON(src []byte) ([]byte, error) {
	var doc any
	if err := yaml.Unmarshal(expandLeadingTabs(src, configTabWidth), &doc); err != nil {
		return nil, err
	}
	return json.Marshal(jsonifyValue(doc))
}

// expandLeadingTabs replaces the leading run of tab characters on every line
// with width spaces per tab. Tabs anywhere other than the start of a line are
// left alone.
func expandLeadingTabs(src []byte, width int) []byte {
	if !bytes.ContainsRune(src, '\t') {
		return src
	}
	var out bytes.Buffer
	out.Grow(len(src) + len(src)/4)
	pad := bytes.Repeat([]byte{' '}, width)
	atLineStart := true
	for _, b := range src {
		switch {
		case atLineStart && b == '\t':
			out.Write(pad)
		case b == '\n':
			out.WriteByte(b)
			atLineStart = true
		default:
			out.WriteByte(b)
			atLineStart = false
		}
	}
	return out.Bytes()
}

// jsonifyValue rewrites the generic value yaml.v3 produces into something
// encoding/json can marshal. yaml.v3 already decodes string-keyed mappings into
// map[string]any, but a mapping with non-string keys decodes into map[any]any,
// which json.Marshal cannot handle; coerce those keys to strings. Recurses
// through slices and maps so the whole tree is safe to marshal.
func jsonifyValue(v any) any {
	switch x := v.(type) {
	case map[string]any:
		for k, val := range x {
			x[k] = jsonifyValue(val)
		}
		return x
	case map[any]any:
		m := make(map[string]any, len(x))
		for k, val := range x {
			m[fmt.Sprint(k)] = jsonifyValue(val)
		}
		return m
	case []any:
		for i, val := range x {
			x[i] = jsonifyValue(val)
		}
		return x
	default:
		return x
	}
}
