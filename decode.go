// decode.go holds the body decoders: the formats a run's output reads as.

package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/url"
	"strings"
)

// decodeAuto is the default decode= value: follow the Content-Type, else sniff.
const decodeAuto = "auto"

// decodeNames lists every decode= value, for an error that names the choices.
const decodeNames = "auto, json, ndjson, csv, tsv, yaml, xml, form, lines, text"

// decoders maps a decode= name to the function that reads a body in that format.
var decoders = map[string]func(string) (any, error){
	"json":   decodeJSON,
	"ndjson": decodeNDJSON,
	"csv":    decodeCSV,
	"tsv":    decodeTSV,
	"yaml":   decodeYAML,
	"xml":    decodeXML,
	"form":   decodeForm,
	"lines":  decodeLines,
	"text":   decodeText,
}

// runBody is the output of a run and the format that reads it. An empty format
// sniffs: JSON when the text parses, else the trimmed text.
type runBody struct {
	text   string
	format string
}

// decode turns the body into data. A named format fails on a body that is not in it. The sniff never fails.
func (b runBody) decode() (any, error) { return decodeBody(b.text, b.format) }

// checkDecode validates a decode= value at load time.
func checkDecode(name string) error {
	if _, ok := decoders[name]; ok || name == "" || name == decodeAuto {
		return nil
	}
	return fmt.Errorf("<run decode=%q>: unknown format (one of: %s)", name, decodeNames)
}

// declaredFormat is the format a decode= value names. auto names none.
func declaredFormat(decode string) string {
	if decode == decodeAuto {
		return ""
	}
	return decode
}

// runFormat is the declared format of whichever run a node has.
func runFormat(cmd *Cmd, req *Request) string {
	switch {
	case req.Defined():
		return declaredFormat(req.Decode)
	case cmd != nil:
		return declaredFormat(cmd.Decode)
	}
	return ""
}

func decodeBody(text, format string) (any, error) {
	if format == "" || format == decodeAuto {
		return parseResult(text), nil
	}
	dec, ok := decoders[format]
	if !ok {
		return nil, fmt.Errorf("unknown decode %q (one of: %s)", format, decodeNames)
	}
	v, err := dec(text)
	if err != nil {
		return nil, fmt.Errorf("the output is not %s: %v; it starts with %s", format, err, bodyExcerpt([]byte(text)))
	}
	return v, nil
}

// responseFormat picks the format of a response under decode="auto". A
// Content-Type that names a format wins when the body reads as that format.
// Any other body goes to the sniff, which is also what reads JSON.
func responseFormat(contentType, text string) string {
	format := contentTypeFormat(contentType)
	if format == "" {
		return ""
	}
	if _, err := decoders[format](text); err != nil {
		logVerbose("decode: Content-Type %q names %s, and the body is not %s (%v); reading it as text", contentType, format, format, err)
		return ""
	}
	logVerbose("decode: Content-Type %q reads as %s", contentType, format)
	return format
}

// contentTypeFormat names the decoder a Content-Type calls for. It returns ""
// for JSON and for a type it does not know, because the sniff reads both.
func contentTypeFormat(header string) string {
	media, _, err := mime.ParseMediaType(header)
	if err != nil {
		return ""
	}
	switch media {
	case "text/csv", "application/csv":
		return "csv"
	case "text/tab-separated-values":
		return "tsv"
	case "application/x-ndjson", "application/ndjson", "application/jsonl", "application/x-jsonlines", "application/jsonlines":
		return "ndjson"
	case "application/yaml", "application/x-yaml", "text/yaml", "text/x-yaml":
		return "yaml"
	case "application/xml", "text/xml":
		return "xml"
	case "application/x-www-form-urlencoded":
		return "form"
	}
	switch {
	case strings.HasSuffix(media, "+yaml"):
		return "yaml"
	case strings.HasSuffix(media, "+xml"):
		return "xml"
	}
	return ""
}

// decodeJSON reads one JSON value.
func decodeJSON(s string) (any, error) {
	if strings.TrimSpace(s) == "" {
		return nil, nil
	}
	dec := json.NewDecoder(strings.NewReader(s))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, errors.New("text follows the first JSON value (decode=\"ndjson\" reads a value per line)")
	}
	return normalizeNumbers(v), nil
}

// decodeNDJSON reads a stream of JSON values into a list. The values need no
// line of their own, so the output of jq reads too.
func decodeNDJSON(s string) (any, error) {
	dec := json.NewDecoder(strings.NewReader(s))
	dec.UseNumber()
	out := []any{}
	for {
		var v any
		err := dec.Decode(&v)
		if errors.Is(err, io.EOF) {
			return out, nil
		}
		if err != nil {
			line := 1 + strings.Count(s[:dec.InputOffset()], "\n")
			return nil, fmt.Errorf("value %d, near line %d: %v", len(out)+1, line, err)
		}
		out = append(out, normalizeNumbers(v))
	}
}

// decodeForm reads application/x-www-form-urlencoded pairs. A key that repeats holds a list.
func decodeForm(s string) (any, error) {
	values, err := url.ParseQuery(strings.TrimSpace(s))
	if err != nil {
		return nil, err
	}
	out := make(map[string]any, len(values))
	for k, vs := range values {
		if len(vs) == 1 {
			out[k] = vs[0]
			continue
		}
		list := make([]any, len(vs))
		for i, v := range vs {
			list[i] = v
		}
		out[k] = list
	}
	return out, nil
}

// decodeLines reads one string per line.
func decodeLines(s string) (any, error) {
	out := []any{}
	for _, line := range splitLines(s) {
		out = append(out, line)
	}
	return out, nil
}

// decodeText keeps the body as one string, without the trailing line breaks.
func decodeText(s string) (any, error) {
	return strings.TrimRight(s, "\r\n"), nil
}

// splitLines cuts s into lines. It drops the trailing line breaks and each line's carriage return.
func splitLines(s string) []string {
	s = strings.TrimRight(s, "\r\n")
	if s == "" {
		return nil
	}
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		lines[i] = strings.TrimSuffix(line, "\r")
	}
	return lines
}
