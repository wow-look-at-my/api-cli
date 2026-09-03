package fields

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// MarshalJSON encodes v indented, without HTML escaping, so a URL stays legible.
func MarshalJSON(v any) (string, error) { return marshalJSON(v) }

// SortedKeys returns m's keys in a stable order, so output does not reshuffle.
func SortedKeys(m map[string]any) []string { return sortedKeys(m) }

// JSONTypeName names a decoded JSON value's type for an error message.
func JSONTypeName(v any) string { return jsonTypeName(v) }

func marshalJSON(v any) (string, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return "", fmt.Errorf("encode json: %w", err)
	}
	return strings.TrimRight(buf.String(), "\n"), nil
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
