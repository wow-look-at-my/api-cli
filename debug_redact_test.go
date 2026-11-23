package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestDebug_DataContextHidesSecrets(t *testing.T) {
	t.Setenv("APICLI_TEST_ENV_SECRET", "env-value-must-not-print")
	cfg := &Config{
		Name:    "t",
		Command: &Cmd{Shell: true, Template: `true`},
		Vars:    map[string]any{"api_token": "var-value-must-not-print", "region": "eu"},
		Commands: []Command{{
			Name:  "say",
			Flags: []Flag{{Name: "password", Type: "string"}},
		}},
	}
	code, _, stderr := execCmdFull(t, cfg, "--debug", "say", "--password", "flag-value-must-not-print")
	assert.Equal(t, 0, code, stderr)
	assert.Contains(t, stderr, "APICLI_TEST_ENV_SECRET", "env names stay visible")
	assert.NotContains(t, stderr, "env-value-must-not-print")
	assert.NotContains(t, stderr, "var-value-must-not-print")
	assert.NotContains(t, stderr, "flag-value-must-not-print")
	assert.Contains(t, stderr, `"region":"eu"`)
	assert.Contains(t, stderr, "[redacted]")
}
