package fields

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSinksAndKnownSink(t *testing.T) {
	names := Sinks()
	require.NotEmpty(t, names)
	for _, name := range names {
		assert.True(t, KnownSink(name), "Sinks named %q, which KnownSink rejects", name)
	}
	assert.False(t, KnownSink("nonesuch"))
}

func TestLookupValueDropsTheFoundFlag(t *testing.T) {
	data := map[string]any{"items": []any{map[string]any{"name": "alpha"}}}
	assert.Equal(t, "alpha", LookupValue(data, "items.0.name"))
	assert.Nil(t, LookupValue(data, "items.9.name"))
}
