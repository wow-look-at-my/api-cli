package fields

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// lookupData resolves a dotted path through decoded JSON, walking maps by key
// and lists by index. The DSL's own lookup walks maps alone, which a body that
// nests lists outgrows.
//
// found is false when a step names nothing, which a JSON null does not do: a
// caller that must fail loud needs to tell them apart.
func lookupData(data any, path string) (value any, found bool) {
	cur := data
	for _, seg := range strings.Split(path, ".") {
		seg = strings.TrimSpace(seg)
		if seg == "" {
			continue
		}
		switch c := cur.(type) {
		case map[string]any:
			v, ok := c[seg]
			if !ok {
				return nil, false
			}
			cur = v
		case map[string]string:
			v, ok := c[seg]
			if !ok {
				return nil, false
			}
			cur = v
		case []any:
			i, err := strconv.Atoi(seg)
			if err != nil || i < 0 || i >= len(c) {
				return nil, false
			}
			cur = c[i]
		case []string:
			i, err := strconv.Atoi(seg)
			if err != nil || i < 0 || i >= len(c) {
				return nil, false
			}
			cur = c[i]
		default:
			return nil, false
		}
	}
	return cur, true
}

// lookupValue is lookupData for a caller that treats a missing path and a null
// the same way, such as a field whose `default=` covers both.
func lookupValue(data any, path string) any {
	v, _ := lookupData(data, path)
	return v
}

// jsonTypeName names a decoded value the way its JSON source does, for an error
// message a config author can act on.
func jsonTypeName(v any) string {
	switch v.(type) {
	case nil:
		return "null"
	case string:
		return "a string"
	case bool:
		return "a boolean"
	case int, int64, float64, json.Number:
		return "a number"
	default:
		return fmt.Sprintf("a %T", v)
	}
}

// overSource resolves a `<fields over=>` path against the response body, then
// against the whole format context, so both spellings work.
//
// A path that names nothing, or a value that is neither a list nor a map, is an
// error. The alternative puts an empty record on the screen, which reads as an
// API that returned nothing rather than a config that points nowhere.
func overSource(over string, parsed any, ctx map[string]any) (any, error) {
	v, ok := lookupData(parsed, over)
	if !ok {
		v, ok = lookupData(ctx, over)
	}
	if !ok {
		return nil, fmt.Errorf("fields over=%q resolved to nothing", over)
	}
	switch v.(type) {
	case []any, map[string]any:
		return v, nil
	}
	return nil, fmt.Errorf("fields over=%q is %s, not a list or a map", over, jsonTypeName(v))
}

// resolveRecords selects the record set per f.Over and classifies its shape:
// "array-objects", "array-scalars", "map-entries", "single", or "scalar".
func resolveRecords(f *Fields, parsed any, ctx map[string]any) ([]record, string, error) {
	src := parsed
	if f.Over != "" {
		v, err := overSource(f.Over, parsed, ctx)
		if err != nil {
			return nil, "", err
		}
		src = v
	}
	switch v := src.(type) {
	case []any:
		hasMap := false
		for _, el := range v {
			if _, ok := el.(map[string]any); ok {
				hasMap = true
				break
			}
		}
		recs := make([]record, 0, len(v))
		for _, el := range v {
			recs = append(recs, record{obj: el})
		}
		// A declared list, or an object element, keeps the table shape.
		if !hasMap && len(f.List) == 0 {
			return recs, "array-scalars", nil
		}
		return recs, "array-objects", nil
	case map[string]any:
		if fieldsWalkMap(f) {
			keys := sortedKeys(v)
			recs := make([]record, 0, len(v))
			for _, k := range keys {
				recs = append(recs, record{obj: v[k], key: k, isEntry: true})
			}
			return recs, "map-entries", nil
		}
		return []record{{obj: v}}, "single", nil
	default:
		return []record{{obj: src}}, "scalar", nil
	}
}

// fieldsWalkMap reports whether a field reads @key or @value, which asks for a
// map to be walked entry by entry instead of read as a lone record.
func fieldsWalkMap(f *Fields) bool {
	for _, fld := range f.List {
		if fld.Path == "@key" || fld.Path == "@value" {
			return true
		}
	}
	return false
}
