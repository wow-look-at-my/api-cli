package main

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zalando/go-keyring"
)

func TestKeyring_HeaderFromKeyring(t *testing.T) {
	t.Serial("keyring.MockInit swaps the process-wide keyring provider")
	keyring.MockInit()
	require.NoError(t, keyring.Set("api-cli-test", "me", "s3cret"))

	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(srv.Close)
	swapHTTPClient(t, srv)

	cfg := &Config{Name: "t", Commands: []Command{{
		Name: "me",
		Request: &Request{Method: "GET", URL: srv.URL, Headers: []Header{
			{Name: "Authorization", Value: `Bearer {{ keyring "api-cli-test" "me" }}`},
		}},
	}}}
	code, _, stderr := execCmdFull(t, cfg, "--debug", "me")
	require.Equal(t, 0, code, stderr)
	assert.Equal(t, "Bearer s3cret", gotAuth)
	assert.NotContains(t, stderr, "s3cret")
}

func TestKeyring_MissingItemFails(t *testing.T) {
	t.Serial("keyring.MockInit swaps the process-wide keyring provider")
	keyring.MockInit()
	_, err := renderString(`{{ keyring "api-cli-test" "nobody" }}`, map[string]any{})
	require.ErrorContains(t, err, `keyring "api-cli-test" "nobody"`)
	assert.ErrorIs(t, err, keyring.ErrNotFound)
}
