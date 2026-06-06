package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v5"
	"github.com/stretchr/testify/require"
)

// TestExampleConfigMatchesSchema validates every shipped *.example.json (auto-
// discovered via filepath.Glob) against api.schema.json using a draft-07 JSON
// Schema validator. Catches drift between the documented schema and any example
// we ship — adding a new example is enough; no test edit required.
func TestExampleConfigMatchesSchema(t *testing.T) {
	schemaBytes, err := os.ReadFile("api.schema.json")
	require.NoError(t, err)

	compiler := jsonschema.NewCompiler()
	require.NoError(t, compiler.AddResource("api.schema.json", bytes.NewReader(schemaBytes)))
	schema, err := compiler.Compile("api.schema.json")
	require.NoError(t, err)

	matches, err := filepath.Glob("*.example.json")
	require.NoError(t, err)
	require.NotEmpty(t, matches, "expected at least one *.example.json")

	for _, path := range matches {
		t.Run(path, func(t *testing.T) {
			exampleBytes, err := os.ReadFile(path)
			require.NoError(t, err)

			var doc any
			require.NoError(t, json.Unmarshal(exampleBytes, &doc))

			require.NoError(t, schema.Validate(doc))
		})
	}
}

// TestGithubSampleLoadsAndMatchesSchema guards the tab-YAML GitHub sample: it
// must load (parse + pass api-cli validation) and conform to the published JSON
// Schema. This replaces the well-formedness coverage it had as a *.json file
// (the CI json-validator only scans *.json).
func TestGithubSampleLoadsAndMatchesSchema(t *testing.T) {
	const path = "samples/github/github.yaml"

	_, err := Load(path)
	require.NoError(t, err)

	schemaBytes, err := os.ReadFile("api.schema.json")
	require.NoError(t, err)
	compiler := jsonschema.NewCompiler()
	require.NoError(t, compiler.AddResource("api.schema.json", bytes.NewReader(schemaBytes)))
	schema, err := compiler.Compile("api.schema.json")
	require.NoError(t, err)

	src, err := os.ReadFile(path)
	require.NoError(t, err)
	jsonSrc, err := sourceToJSON(src)
	require.NoError(t, err)

	var doc any
	require.NoError(t, json.Unmarshal(jsonSrc, &doc))
	require.NoError(t, schema.Validate(doc))
}
