package main

import (
	"bytes"
	"encoding/json"
	"os"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v5"
	"github.com/wow-look-at-my/testify/require"
)

// TestExampleConfigMatchesSchema validates the shipped api.example.json against
// api.schema.json using a draft-07 JSON Schema validator. Catches drift between
// the documented schema and the example we ship.
func TestExampleConfigMatchesSchema(t *testing.T) {
	schemaBytes, err := os.ReadFile("api.schema.json")
	require.NoError(t, err)

	compiler := jsonschema.NewCompiler()
	require.NoError(t, compiler.AddResource("api.schema.json", bytes.NewReader(schemaBytes)))
	schema, err := compiler.Compile("api.schema.json")
	require.NoError(t, err)

	exampleBytes, err := os.ReadFile("api.example.json")
	require.NoError(t, err)

	var doc any
	require.NoError(t, json.Unmarshal(exampleBytes, &doc))

	require.NoError(t, schema.Validate(doc))
}
