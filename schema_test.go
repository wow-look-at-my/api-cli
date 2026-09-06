package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	spec "github.com/wow-look-at-my/api-cli-spec"
	"github.com/wow-look-at-my/xml-validator/validator"
)

// Every config this repo ships, against the grammar. The schema comes from the
// specification module, so nothing here can drift from it.
func TestShippedConfigsValidateAgainstTheGrammar(t *testing.T) {
	paths, err := filepath.Glob("*.example.xml")
	require.NoError(t, err)
	paths = append(paths, "samples/github/github.xml", "samples/ci/ci.xml")

	for _, path := range paths {
		t.Run(path, func(t *testing.T) {
			file, err := os.Open(path)
			require.NoError(t, err)
			defer file.Close()

			assert.NoError(t, validator.ValidateWithSchema(file, strings.NewReader(spec.Schema)))
		})
	}
}

// TestExampleConfigsLoad ensures every shipped *.example.xml parses and passes
// api-cli validation. Adding a new example is enough; no test edit required.
func TestExampleConfigsLoad(t *testing.T) {
	matches, err := filepath.Glob("*.example.xml")
	require.NoError(t, err)
	require.NotEmpty(t, matches, "expected at least one *.example.xml")

	for _, path := range matches {
		t.Run(path, func(t *testing.T) {
			cfg, err := Load(path)
			require.NoError(t, err)
			assert.NotEmpty(t, cfg.Name)
		})
	}
}

// TestGithubSampleLoads guards the XML GitHub sample: it must parse and pass
// api-cli validation.
func TestGithubSampleLoads(t *testing.T) {
	cfg, err := Load("samples/github/github.xml")
	require.NoError(t, err)
	assert.Equal(t, "github", cfg.Name)
}
