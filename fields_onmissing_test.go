package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseXML_FieldsOnMissing(t *testing.T) {
	cfg := mustParse(t, `<config name="x"><command name="c"><run>x</run>
		<fields over="children" on-missing="empty"><field name="name">name</field></fields>
		<fields over="items" on-missing="fail"><field name="id">id</field></fields>
	</command></config>`)
	assert.True(t, cfg.Commands[0].Fields[0].Fields.MissingEmpty)
	assert.False(t, cfg.Commands[0].Fields[1].Fields.MissingEmpty)
}

func TestParseXML_FieldsOnMissingErrors(t *testing.T) {
	_, err := parseConfigXML([]byte(`<config name="x"><command name="c"><run>x</run>
		<fields on-missing="empty"><field name="name">name</field></fields></command></config>`))
	require.ErrorContains(t, err, "needs over=")

	_, err = parseConfigXML([]byte(`<config name="x"><command name="c"><run>x</run>
		<fields over="a" on-missing="skip"><field name="name">name</field></fields></command></config>`))
	require.ErrorContains(t, err, `want "fail" or "empty"`)
}

func onMissingConfig(url string, missingEmpty bool) *Config {
	return &Config{
		Name: "t",
		Commands: []Command{{
			Name:    "ls",
			Request: &Request{Method: "GET", URL: url},
			Fields: []FieldsBlock{{Fields: &Fields{
				Over:         "children",
				MissingEmpty: missingEmpty,
				List:         []Field{{Name: "name", Path: "name"}},
			}}},
		}},
	}
}

func TestFields_OnMissingEmpty(t *testing.T) {
	for _, body := range []string{`{"path":"/empty"}`, `{"children":null}`} {
		t.Run(body, func(t *testing.T) {
			srv := jsonServer(t, body)
			code, out := execCmd(t, onMissingConfig(srv.URL, true), "ls", "--format=always")
			require.Equal(t, 0, code, out)
			assert.NotContains(t, out, "resolved to nothing")
			assert.NotContains(t, out, "/empty", "an empty list prints no records, not the body")
		})
	}
}

func TestFields_OnMissingEmptyKeepsRecords(t *testing.T) {
	srv := jsonServer(t, `{"children":[{"name":"a.txt"},{"name":"b.txt"}]}`)
	code, out := execCmd(t, onMissingConfig(srv.URL, true), "ls", "--format=always")
	require.Equal(t, 0, code, out)
	assert.Contains(t, out, "a.txt")
	assert.Contains(t, out, "b.txt")
}

func TestFields_OnMissingDefaultFails(t *testing.T) {
	srv := jsonServer(t, `{"path":"/empty"}`)
	code, out := execCmdFull(t, onMissingConfig(srv.URL, false), "ls", "--format=always")
	assert.NotEqual(t, 0, code)
	assert.Contains(t, out, "resolved to nothing")
}

// A scalar is a shape error, not an absence, so on-missing does not cover it.
func TestFields_OnMissingScalarStillFails(t *testing.T) {
	srv := jsonServer(t, `{"children":"none"}`)
	code, out := execCmdFull(t, onMissingConfig(srv.URL, true), "ls", "--format=always")
	assert.NotEqual(t, 0, code)
	assert.Contains(t, out, "not a list or a map")
}
