package main

import _ "embed"

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The spec's tour of element forms: every template attribute written as a child element.
//
//go:embed testdata/template-elements.xml
var templateElementsConfig string

func TestElementFormsReadAsAttributes(t *testing.T) {
	cfg, err := parseConfigXML([]byte(templateElementsConfig))
	require.NoError(t, err)

	assert.Equal(t, "[.[]| select(.active)]", cfg.Request.Response.JQ, "lines lose their surrounding whitespace and join with nothing")

	items := cfg.Commands[0]
	assert.Equal(t, "^[a-z]+$", items.Args[0].Pattern)
	assert.Equal(t, "{{ .env.USER }}", items.Flags[0].Default)
	assert.Equal(t, "{{ .arg.kind }}", items.Steps[0].When)
	assert.Equal(t, "{{or .result.page.done .result.page.empty}}", items.Steps[0].Until)
	assert.Equal(t, "result.page.items", items.Steps[1].Over)
	require.Len(t, items.Fields, 1)
	assert.Equal(t, "{{ not .flag.raw }}", items.Fields[0].When)
	assert.Equal(t, "{{ len .data }} items", items.Fields[0].Fields.Footer, "a CDATA section keeps its spaces")
	assert.Equal(t, "{{ .title| upper }}", items.Fields[0].Fields.List[1].Expr)

	report := cfg.Commands[1]
	require.NotNil(t, report.Format)
	assert.Equal(t, "{{ .tty }}", report.Format.Inline.When)
	assert.Equal(t, "{{ lt (len .data) 10 }}", report.Format.Inline.Views[0].When)

	field := cfg.Commands[2].TML.Props[0].Fields[1]
	assert.Equal(t, "{{ .log }}", field.Expr)
	assert.Equal(t, "{{ .flag.lines }}", field.Last)
	assert.Equal(t, "80", field.Truncate)

	d := cfg.Commands[3].Downloads[0]
	assert.Equal(t, "{{ toJson .data.parts }}", d.Over)
	assert.Equal(t, "{{ .item.url }}", d.When)
	assert.Equal(t, "{{ .item.stream }}", d.Group)
	assert.Equal(t, "{{ .index }}", d.Order)
	assert.Equal(t, "out/{{ .stream }}.ts", d.Join.To)

	m := cfg.Commands[4].Mock
	assert.Equal(t, "{{ if .mock.src }}0{{ else }}1{{ end }}", m.Exit)
	assert.Equal(t, "main.c", m.Inputs[0].Default)
	assert.Equal(t, "{{ stem .mock.src }}.o", m.Outputs[0].Path)
	assert.Equal(t, "0644", m.Outputs[0].Mode)
	assert.Equal(t, "build/commands.jsonl", m.Records[0].Path)
}

func TestElementFormAndAttributeTogetherFail(t *testing.T) {
	_, err := parseConfigXML([]byte(`<config name="x"><command name="a"><arg name="q" pattern="a"><pattern>b</pattern></arg><run>true</run></command></config>`))
	require.ErrorContains(t, err, "pattern= and <pattern> both set the same template")
}
