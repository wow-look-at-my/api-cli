package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDecodeBody_EachFormat(t *testing.T) {
	cases := []struct {
		name, format, body string
		want               any
	}{
		{"json object", "json", `{"a":1,"b":[1.5,"x"]}`, map[string]any{"a": int64(1), "b": []any{1.5, "x"}}},
		{"json empty body is null", "json", " \n", nil},
		{"ndjson skips blank lines", "ndjson", "{\"a\":1}\n\n{\"a\":2}\n", []any{map[string]any{"a": int64(1)}, map[string]any{"a": int64(2)}}},
		{"ndjson reads values with no line of their own", "ndjson", "{\n  \"a\": 1\n}\n[2]", []any{map[string]any{"a": int64(1)}, []any{int64(2)}}},
		{"ndjson empty body is an empty list", "ndjson", "", []any{}},
		{"csv header keys every record", "csv", "id,name\n1,ada\n2,\"b, c\"\n", []any{
			map[string]any{"id": "1", "name": "ada"},
			map[string]any{"id": "2", "name": "b, c"},
		}},
		{"csv keeps a number as text", "csv", "zip\n02134\n", []any{map[string]any{"zip": "02134"}}},
		{"csv drops a byte order mark", "csv", "\xef\xbb\xbfid\n7\n", []any{map[string]any{"id": "7"}}},
		{"csv names an empty and a repeated column", "csv", "a,,a\n1,2,3\n", []any{map[string]any{"a": "1", "column_2": "2", "a_2": "3"}}},
		{"csv header alone is an empty list", "csv", "id,name\n", []any{}},
		{"csv empty body is an empty list", "csv", "", []any{}},
		{"tsv treats a quote as data", "tsv", "id\tsaid\r\n1\t\"hi\" there\r\n", []any{map[string]any{"id": "1", "said": `"hi" there`}}},
		{"yaml mapping", "yaml", "a: 1\nb:\n  - x\n  - 2.5\nc: true\n", map[string]any{"a": int64(1), "b": []any{"x", 2.5}, "c": true}},
		{"yaml key that is not a string", "yaml", "1: one\n", map[string]any{"1": "one"}},
		{"yaml stream is a list", "yaml", "a: 1\n---\na: 2\n", []any{map[string]any{"a": int64(1)}, map[string]any{"a": int64(2)}}},
		{"yaml empty body is null", "yaml", "", nil},
		{"form", "form", "access_token=abc&scope=a%20b&id=1&id=2\n", map[string]any{"access_token": "abc", "scope": "a b", "id": []any{"1", "2"}}},
		{"lines", "lines", "a\r\nb\n\n", []any{"a", "b"}},
		{"lines empty body is an empty list", "lines", "\n", []any{}},
		{"text keeps the leading space and drops the trailing breaks", "text", "  {\"a\":1}\r\n\n", `  {"a":1}`},
		{"auto reads JSON", "auto", ` {"a":1} `, map[string]any{"a": int64(1)}},
		{"no format reads JSON", "", `[1]`, []any{int64(1)}},
		{"auto reads anything else as trimmed text", "auto", " id,name\n1,ada\n", "id,name\n1,ada"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := decodeBody(tc.body, tc.format)
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestDecodeBody_XML(t *testing.T) {
	body := `<?xml version="1.1" encoding="UTF-8"?>
<rss version="2.0" xmlns:atom="http://www.w3.org/2005/Atom">
	<channel>
		<title>Feed</title>
		<atom:link href="https://example.invalid/feed" rel="self"/>
		<item><title>one</title><guid isPermaLink="false">a1</guid></item>
		<item><title>two</title><guid isPermaLink="true">a2</guid></item>
		<empty/>
	</channel>
</rss>`
	got, err := decodeBody(body, "xml")
	require.NoError(t, err)
	assert.Equal(t, map[string]any{
		"rss": map[string]any{
			"@version": "2.0",
			"channel": map[string]any{
				"title": "Feed",
				"link":  map[string]any{"@href": "https://example.invalid/feed", "@rel": "self"},
				"item": []any{
					map[string]any{"title": "one", "guid": map[string]any{"@isPermaLink": "false", "#text": "a1"}},
					map[string]any{"title": "two", "guid": map[string]any{"@isPermaLink": "true", "#text": "a2"}},
				},
				"empty": "",
			},
		},
	}, got)
}

func TestDecodeBody_XMLInAnotherEncoding(t *testing.T) {
	got, err := decodeBody("<?xml version=\"1.0\" encoding=\"ISO-8859-1\"?><name>Ren\xe9e</name>", "xml")
	require.NoError(t, err)
	assert.Equal(t, map[string]any{"name": "Renée"}, got)
}

// A named format is a claim, so a body that breaks it fails and shows itself.
func TestDecodeBody_NamedFormatRejectsAnotherBody(t *testing.T) {
	cases := []struct{ name, format, body, want string }{
		{"json", "json", "<!DOCTYPE html><title>Sign in</title>", "the output is not json"},
		{"json with a second value", "json", "{}\n{}", `decode="ndjson"`},
		{"ndjson", "ndjson", "{\"a\":1}\n{oops", "value 2, near line 2"},
		{"csv with a short row", "csv", "a,b\n1\n", "wrong number of fields"},
		{"tsv with a long row", "tsv", "a\tb\n1\t2\t3\n", "line 2 has 3 fields, and the first row has 2"},
		{"yaml", "yaml", "a: [1, 2", "the output is not yaml"},
		{"xml that never closes", "xml", "<a><b></a>", "the output is not xml"},
		{"xml with no element", "xml", "plain words", "no root element"},
		{"xml in an encoding nobody knows", "xml", `<?xml version="1.0" encoding="klingon"?><a/>`, `encoding "klingon"`},
		{"form", "form", "a=%zz", "the output is not form"},
		{"a format that does not exist", "toml", "a = 1", `unknown decode "toml"`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := decodeBody(tc.body, tc.format)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
		})
	}
}

func TestDecodeBody_ErrorShowsTheStartOfTheBody(t *testing.T) {
	_, err := decodeBody("<!DOCTYPE html><title>Sign in</title>", "json")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "<title>Sign in</title>")
}

func TestContentTypeFormat(t *testing.T) {
	cases := map[string]string{
		"text/csv":                           "csv",
		"text/csv; charset=utf-8":            "csv",
		"Text/CSV":                           "csv",
		"text/tab-separated-values":          "tsv",
		"application/x-ndjson":               "ndjson",
		"application/jsonl":                  "ndjson",
		"application/yaml":                   "yaml",
		"text/x-yaml":                        "yaml",
		"application/vnd.oai.openapi+yaml":   "yaml",
		"application/xml":                    "xml",
		"text/xml; charset=ISO-8859-1":       "xml",
		"application/atom+xml":               "xml",
		"application/x-www-form-urlencoded":  "form",
		"application/json":                   "",
		"application/vnd.github+json":        "",
		"text/plain; charset=utf-8":          "",
		"text/html":                          "",
		"application/octet-stream":           "",
		"":                                   "",
		"not a media type at all; ; ;":       "",
		"application/x-yaml; charset=\"utf8": "",
	}
	for header, want := range cases {
		assert.Equal(t, want, contentTypeFormat(header), "Content-Type %q", header)
	}
}

// auto is a guess. A Content-Type that the body does not keep is not a failure.
func TestResponseFormat_FallsBackWhenTheBodyDoesNotMatch(t *testing.T) {
	assert.Equal(t, "csv", responseFormat("text/csv", "a,b\n1,2\n"))
	assert.Equal(t, "", responseFormat("text/csv", "a,b\n1\n"), "a ragged body is read as text")
	assert.Equal(t, "", responseFormat("application/json", `{"a":1}`), "the sniff reads JSON")
}

func TestCheckDecode(t *testing.T) {
	for _, name := range []string{"", "auto", "json", "ndjson", "csv", "tsv", "yaml", "xml", "form", "lines", "text"} {
		assert.NoError(t, checkDecode(name), name)
	}
	err := checkDecode("jsonl")
	require.Error(t, err)
	assert.Contains(t, err.Error(), `<run decode="jsonl">: unknown format`)
	assert.Contains(t, err.Error(), "ndjson", "the error names the choices")
}

func TestRunFormat(t *testing.T) {
	assert.Equal(t, "csv", runFormat(&Cmd{Shell: true, Template: "x", Decode: "csv"}, nil))
	assert.Equal(t, "", runFormat(&Cmd{Shell: true, Template: "x", Decode: "auto"}, nil), "auto names no format")
	assert.Equal(t, "yaml", runFormat(nil, &Request{URL: "u", Decode: "yaml"}))
	assert.Equal(t, "", runFormat(nil, nil))
}

func TestTableColumns_KeepsTheHeaderOrder(t *testing.T) {
	assert.Equal(t, []string{"zeta", "alpha", "column_3"}, tableColumns(runBody{text: "zeta,alpha,\n1,2,3\n", format: "csv"}))
	assert.Equal(t, []string{"zeta", "alpha"}, tableColumns(runBody{text: "\nzeta\talpha\n1\t2\n", format: "tsv"}))
	assert.Nil(t, tableColumns(runBody{text: `[{"zeta":1}]`}), "only a delimited body has a header")
	assert.Nil(t, tableColumns(runBody{text: "", format: "csv"}))
}

func TestOrderDerived(t *testing.T) {
	body := runBody{text: "zeta,a.b\n1,2\n", format: "csv"}
	declared := &Fields{List: []Field{{Name: "Z", Path: "zeta"}}}
	scoped := &Fields{Over: "data"}
	blocks := orderDerived([]FieldsBlock{{Fields: &Fields{}}, {Fields: declared}, {Fields: scoped}}, body)

	assert.Equal(t, []Field{{Name: "zeta", Path: "zeta"}, {Name: "a.b", Expr: `{{ index . "a.b" }}`}}, blocks[0].Fields.List,
		"a block with no fields takes the header, and a dotted column reads by index")
	assert.Same(t, declared, blocks[1].Fields, "declared fields stay as the author wrote them")
	assert.Same(t, scoped, blocks[2].Fields, "over= reads somewhere other than the rows")

	same := []FieldsBlock{{Fields: &Fields{}}}
	assert.Empty(t, orderDerived(same, runBody{text: `[{"b":1,"a":2}]`})[0].Fields.List, "a JSON body has no header to follow")
}
