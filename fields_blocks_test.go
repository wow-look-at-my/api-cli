package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fieldsBlocks wraps one declaration as the single block a leaf carries. Most
// tests declare one shape, and this keeps them reading that way.
func fieldsBlocks(f *Fields) []FieldsBlock {
	return []FieldsBlock{{Fields: f}}
}

// jsonServer answers every path with the given JSON body.
func jsonServer(t *testing.T, body string) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	swapHTTPClient(t, srv)
	return srv
}

func TestFieldsBlocks_WhenPicksTheShape(t *testing.T) {
	srv := jsonServer(t, `{"items":[{"id":1,"name":"a"},{"id":2,"name":"b"}],"id":9,"name":"detail","body":"long"}`)
	cfg := &Config{
		Name: "t",
		Commands: []Command{{
			Name: "thing",
			Args: []Arg{{Name: "id"}},
			Request: &Request{
				Method: "GET",
				URL:    srv.URL + "/thing/{{ .arg.id }}",
			},
			Fields: []FieldsBlock{
				{When: "{{ not .arg.id }}", Fields: &Fields{Over: "items", List: []Field{
					{Name: "id", Path: "id"},
					{Name: "name", Path: "name"},
				}}},
				{When: "{{ .arg.id }}", Fields: &Fields{List: []Field{
					{Name: "name", Path: "name"},
					{Name: "body", Path: "body"},
				}}},
			},
		}},
	}

	code, out := execCmd(t, cfg, "thing")
	require.Equal(t, 0, code)
	assert.Contains(t, out, "id  name")
	assert.NotContains(t, out, "body")

	code, out = execCmd(t, cfg, "thing", "9")
	require.Equal(t, 0, code)
	assert.Contains(t, out, "name: detail")
	assert.Contains(t, out, "body: long")
}

func TestFieldsBlocks_TwoTablesOnOneScreen(t *testing.T) {
	srv := jsonServer(t, `{"items":[{"name":"a"}],"extras":[{"name":"x"},{"name":"y"}]}`)
	cfg := &Config{
		Name: "t",
		Commands: []Command{{
			Name:    "dash",
			Request: &Request{Method: "GET", URL: srv.URL + "/dash"},
			Fields: []FieldsBlock{
				{Fields: &Fields{Over: "items", List: []Field{{Name: "primary", Path: "name"}}}},
				{Fields: &Fields{Over: "extras", List: []Field{{Name: "extra", Path: "name"}}}},
			},
		}},
	}

	code, out := execCmd(t, cfg, "dash")
	require.Equal(t, 0, code)
	assert.Contains(t, out, "primary")
	assert.Contains(t, out, "extra")
	assert.Contains(t, out, "\n\n") // the two tables are separated
	assert.Less(t, strings.Index(out, "primary"), strings.Index(out, "extra"))
}

func TestFieldsBlocks_NoBlockMatchesPrintsRawBody(t *testing.T) {
	srv := jsonServer(t, `{"name":"a"}`)
	cfg := &Config{
		Name: "t",
		Commands: []Command{{
			Name:    "thing",
			Request: &Request{Method: "GET", URL: srv.URL + "/thing"},
			Fields: []FieldsBlock{
				{When: "false", Fields: &Fields{List: []Field{{Name: "name", Path: "name"}}}},
			},
		}},
	}

	code, out := execCmd(t, cfg, "thing")
	require.Equal(t, 0, code)
	assert.Contains(t, out, `{"name":"a"}`)
}

func TestFieldsBlocks_OverAStepResult(t *testing.T) {
	srv := jsonServer(t, `{"rows":[{"n":"one"}]}`)
	cfg := &Config{
		Name: "t",
		Commands: []Command{{
			Name:    "dash",
			Request: &Request{Method: "GET", URL: srv.URL + "/dash"},
			Steps: []Step{{
				Name:    "side",
				Request: &Request{Method: "GET", URL: srv.URL + "/side"},
			}},
			Fields: []FieldsBlock{
				{Fields: &Fields{Over: "rows", List: []Field{{Name: "main", Path: "n"}}}},
				{Fields: &Fields{Over: "result.side.rows", List: []Field{{Name: "side", Path: "n"}}}},
			},
		}},
	}

	code, out := execCmd(t, cfg, "dash")
	require.Equal(t, 0, code)
	assert.Contains(t, out, "main")
	assert.Contains(t, out, "side")
}

func TestParseXML_FieldsWhenAndSeveralBlocks(t *testing.T) {
	cfg := mustParse(t, `<config name="x"><command name="c"><run>x</run>
		<fields when="{{ not .arg.id }}" over="items"><field name="id">id</field></fields>
		<fields when="{{ .arg.id }}"><field name="name">name</field></fields>
		</command></config>`)
	blocks := cfg.Commands[0].Fields
	require.Len(t, blocks, 2)
	assert.Equal(t, "{{ not .arg.id }}", blocks[0].When)
	assert.Equal(t, "items", blocks[0].Fields.Over)
	assert.Equal(t, "{{ .arg.id }}", blocks[1].When)
	assert.Equal(t, "name", blocks[1].Fields.List[0].Path)
}
