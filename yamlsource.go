package main

import (
	"encoding/json"

	"github.com/wow-look-at-my/yaml-fixed/yaml"
)

// sourceToJSON converts a config source into canonical JSON bytes for the
// strict encoding/json decoder in Load.
//
// Two on-disk forms are accepted:
//
//   - JSON. If the bytes are already valid JSON they are handed straight to the
//     decoder unchanged. Every existing JSON config keeps loading exactly as
//     before, multi-line pretty-printing included.
//   - tab-YAML. Otherwise the source is parsed with yaml-fixed, which uses
//     tabs -- and only tabs -- for indentation, then
//     re-encoded as JSON. Routing back through JSON keeps a single decode path:
//     the strict decoder's unknown-field rejection and the Cmd/FormatRef
//     UnmarshalJSON methods (which are json-, not yaml-, aware) all keep
//     working untouched.
//
// Space-indented YAML is intentionally unsupported: yaml-fixed rejects spaces
// used as indentation, so such a document fails to parse with a clear error.
func sourceToJSON(src []byte) ([]byte, error) {
	if json.Valid(src) {
		return src, nil
	}
	doc, err := yaml.Parse(src)
	if err != nil {
		return nil, err
	}
	return json.Marshal(doc)
}
