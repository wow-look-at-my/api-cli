package main

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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
