package main

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/wow-look-at-my/testify/assert"
	"github.com/wow-look-at-my/testify/require"
)

// --- parseCorsLevel ---

func TestParseCorsLevel(t *testing.T) {
	tests := []struct {
		in   string
		want CorsLevel
		err  bool
	}{
		{"disabled", CorsDisabled, false},
		{"DISABLED", CorsDisabled, false},
		{"  Disabled  ", CorsDisabled, false},
		{"off", CorsDisabled, false},
		{"none", CorsDisabled, false},
		{"open", CorsDisabled, false},
		{"permissive", CorsPermissive, false},
		{"lax", CorsPermissive, false},
		{"loose", CorsPermissive, false},
		{"localhost", CorsPermissive, false},
		{"strict", CorsStrict, false},
		{"same-origin", CorsStrict, false},
		{"sameorigin", CorsStrict, false},
		{"enabled", CorsEnabled, false},
		{"on", CorsEnabled, false},
		{"locked", CorsEnabled, false},
		{"lockdown", CorsEnabled, false},
		{"block", CorsEnabled, false},
		{"", 0, true},
		{"foo", 0, true},
		{"true", 0, true},
	}
	for _, tt := range tests {
		got, err := parseCorsLevel(tt.in)
		if tt.err {
			assert.Error(t, err, tt.in)
		} else {
			require.NoError(t, err, tt.in)
			assert.Equal(t, tt.want, got, tt.in)
		}
	}
}

func TestCorsLevel_String(t *testing.T) {
	assert.Equal(t, "disabled", CorsDisabled.String())
	assert.Equal(t, "permissive", CorsPermissive.String())
	assert.Equal(t, "strict", CorsStrict.String())
	assert.Equal(t, "enabled", CorsEnabled.String())
	assert.Equal(t, "unknown", CorsLevel(99).String())
}

// --- sameOrigin / localhostOrigin ---

func TestSameOrigin(t *testing.T) {
	tests := []struct {
		origin     string
		listenAddr string
		want       bool
	}{
		{"http://127.0.0.1:8080", "127.0.0.1:8080", true},
		{"http://127.0.0.1:8080", "127.0.0.1:9090", false},
		{"http://localhost:8080", "localhost:8080", true},
		{"http://localhost:8080", "127.0.0.1:8080", false},
		{"http://localhost:8080", "0.0.0.0:8080", true},
		{"http://example.com:8080", "0.0.0.0:8080", true},
		{"http://example.com:8080", "::8080", false},
		{"http://example.com:8080", "127.0.0.1:8080", false},
		{"http://example.com", "0.0.0.0:80", true},
		{"https://example.com", "0.0.0.0:443", true},
		{"http://example.com", "0.0.0.0:443", false},
		{"http://example.com:9999", "0.0.0.0:8080", false},
		{"not a url", "127.0.0.1:8080", false},
		{"http://example.com:8080", "no-port", false},
	}
	for _, tt := range tests {
		got := sameOrigin(tt.origin, tt.listenAddr)
		assert.Equal(t, tt.want, got, "%s vs %s", tt.origin, tt.listenAddr)
	}
}

func TestLocalhostOrigin(t *testing.T) {
	assert.True(t, localhostOrigin("http://localhost:8080"))
	assert.True(t, localhostOrigin("http://127.0.0.1:9999"))
	assert.True(t, localhostOrigin("https://[::1]:8080"))
	assert.True(t, localhostOrigin("http://localhost"))
	assert.False(t, localhostOrigin("http://example.com"))
	assert.False(t, localhostOrigin("http://localhost.evil.com"))
	assert.False(t, localhostOrigin(""))
	assert.False(t, localhostOrigin("not a url"))
}

// --- withCORS ---

func newCorsRequest(method, origin string) *http.Request {
	req := httptest.NewRequest(method, "/", nil)
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	return req
}

func newPreflight(origin, reqMethod string) *http.Request {
	req := httptest.NewRequest(http.MethodOptions, "/", nil)
	req.Header.Set("Origin", origin)
	req.Header.Set("Access-Control-Request-Method", reqMethod)
	return req
}

func okHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
}

// CorsDisabled — wide open ----------------------------------------------------

func TestWithCORS_Disabled_AnyOriginEchoed(t *testing.T) {
	h := withCORS(okHandler(), CorsDisabled, "127.0.0.1:8080")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, newCorsRequest(http.MethodGet, "http://evil.example.com"))

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "http://evil.example.com", rec.Header().Get("Access-Control-Allow-Origin"))
	assert.Equal(t, "true", rec.Header().Get("Access-Control-Allow-Credentials"))
	assert.Equal(t, "*", rec.Header().Get("Access-Control-Expose-Headers"))
}

func TestWithCORS_Disabled_PreflightAlwaysSucceeds(t *testing.T) {
	called := false
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true })
	h := withCORS(inner, CorsDisabled, "127.0.0.1:8080")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, newPreflight("http://anywhere.example.com", "POST"))

	assert.False(t, called, "preflight should not reach inner")
	assert.Equal(t, http.StatusNoContent, rec.Code)
	assert.Equal(t, "http://anywhere.example.com", rec.Header().Get("Access-Control-Allow-Origin"))
	assert.Equal(t, "true", rec.Header().Get("Access-Control-Allow-Credentials"))
	assert.Equal(t, "POST", rec.Header().Get("Access-Control-Allow-Methods"))
	assert.Equal(t, "*", rec.Header().Get("Access-Control-Allow-Headers"))
}

// CorsPermissive — localhost + same-origin -----------------------------------

func TestWithCORS_Permissive_LocalhostAllowed(t *testing.T) {
	h := withCORS(okHandler(), CorsPermissive, "0.0.0.0:8080")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, newCorsRequest(http.MethodGet, "http://localhost:9999"))

	assert.Equal(t, "http://localhost:9999", rec.Header().Get("Access-Control-Allow-Origin"))
	assert.Contains(t, rec.Header().Values("Vary"), "Origin")
}

func TestWithCORS_Permissive_SameOriginAllowed(t *testing.T) {
	h := withCORS(okHandler(), CorsPermissive, "example.com:8080")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, newCorsRequest(http.MethodGet, "http://example.com:8080"))

	assert.Equal(t, "http://example.com:8080", rec.Header().Get("Access-Control-Allow-Origin"))
}

func TestWithCORS_Permissive_RemoteOriginRejected(t *testing.T) {
	h := withCORS(okHandler(), CorsPermissive, "127.0.0.1:8080")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, newCorsRequest(http.MethodGet, "http://evil.example.com"))

	assert.Equal(t, http.StatusOK, rec.Code, "request still reaches inner; browser blocks via missing header")
	assert.Empty(t, rec.Header().Get("Access-Control-Allow-Origin"))
}

func TestWithCORS_Permissive_PreflightForRemoteOrigin(t *testing.T) {
	called := false
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true })
	h := withCORS(inner, CorsPermissive, "127.0.0.1:8080")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, newPreflight("http://evil.example.com", "POST"))

	assert.False(t, called)
	assert.Equal(t, http.StatusForbidden, rec.Code)
}

// CorsStrict — same-origin only ----------------------------------------------

func TestWithCORS_Strict_SameOriginAllowed(t *testing.T) {
	h := withCORS(okHandler(), CorsStrict, "127.0.0.1:8080")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, newCorsRequest(http.MethodGet, "http://127.0.0.1:8080"))

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "http://127.0.0.1:8080", rec.Header().Get("Access-Control-Allow-Origin"))
	assert.Contains(t, rec.Header().Values("Vary"), "Origin")
}

func TestWithCORS_Strict_LocalhostNotSameOriginRejected(t *testing.T) {
	// Bound to 127.0.0.1; localhost (different hostname) is rejected.
	h := withCORS(okHandler(), CorsStrict, "127.0.0.1:8080")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, newCorsRequest(http.MethodGet, "http://localhost:8080"))

	assert.Empty(t, rec.Header().Get("Access-Control-Allow-Origin"))
}

func TestWithCORS_Strict_DifferentOriginRejected(t *testing.T) {
	h := withCORS(okHandler(), CorsStrict, "127.0.0.1:8080")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, newCorsRequest(http.MethodGet, "http://example.com"))

	assert.Empty(t, rec.Header().Get("Access-Control-Allow-Origin"))
}

func TestWithCORS_Strict_PreflightSameOrigin(t *testing.T) {
	h := withCORS(okHandler(), CorsStrict, "127.0.0.1:8080")
	rec := httptest.NewRecorder()
	req := newPreflight("http://127.0.0.1:8080", "POST")
	req.Header.Set("Access-Control-Request-Headers", "Mcp-Session-Id, Content-Type")
	h.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusNoContent, rec.Code)
	assert.Equal(t, "http://127.0.0.1:8080", rec.Header().Get("Access-Control-Allow-Origin"))
	assert.Equal(t, "POST", rec.Header().Get("Access-Control-Allow-Methods"))
	assert.Equal(t, "Mcp-Session-Id, Content-Type", rec.Header().Get("Access-Control-Allow-Headers"))
}

func TestWithCORS_Strict_PreflightDifferentOriginForbidden(t *testing.T) {
	h := withCORS(okHandler(), CorsStrict, "127.0.0.1:8080")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, newPreflight("http://example.com", "POST"))

	assert.Equal(t, http.StatusForbidden, rec.Code)
	assert.Empty(t, rec.Header().Get("Access-Control-Allow-Origin"))
}

// CorsEnabled — locked down --------------------------------------------------

func TestWithCORS_Enabled_NoAllowOriginEver(t *testing.T) {
	h := withCORS(okHandler(), CorsEnabled, "127.0.0.1:8080")
	rec := httptest.NewRecorder()
	// Even same-origin is denied an ACL-Allow-Origin header.
	h.ServeHTTP(rec, newCorsRequest(http.MethodGet, "http://127.0.0.1:8080"))

	assert.Equal(t, http.StatusOK, rec.Code, "non-preflight still reaches inner")
	assert.Empty(t, rec.Header().Get("Access-Control-Allow-Origin"))
}

func TestWithCORS_Enabled_PreflightForbidden(t *testing.T) {
	called := false
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true })
	h := withCORS(inner, CorsEnabled, "127.0.0.1:8080")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, newPreflight("http://127.0.0.1:8080", "POST"))

	assert.False(t, called)
	assert.Equal(t, http.StatusForbidden, rec.Code)
	assert.Empty(t, rec.Header().Get("Access-Control-Allow-Origin"))
}

// No-Origin requests (server-to-server) --------------------------------------

func TestWithCORS_NoOrigin_AlwaysPassesThrough(t *testing.T) {
	// Requests with no Origin header (server-to-server, curl, MCP SDKs)
	// pass through unconditionally and get no CORS headers — they don't
	// need any.
	for _, level := range []CorsLevel{CorsDisabled, CorsPermissive, CorsStrict, CorsEnabled} {
		h := withCORS(okHandler(), level, "127.0.0.1:8080")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, newCorsRequest(http.MethodGet, ""))

		assert.Equal(t, http.StatusOK, rec.Code, level.String())
		assert.Empty(t, rec.Header().Get("Access-Control-Allow-Origin"), level.String())
	}
}

// OPTIONS without Access-Control-Request-Method is not a preflight ----------

func TestWithCORS_NonPreflightOptions_PassesThrough(t *testing.T) {
	called := false
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusMethodNotAllowed)
	})
	h := withCORS(inner, CorsStrict, "127.0.0.1:8080")

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodOptions, "/", nil)
	req.Header.Set("Origin", "http://127.0.0.1:8080")
	h.ServeHTTP(rec, req)

	assert.True(t, called)
	assert.Equal(t, http.StatusMethodNotAllowed, rec.Code)
}
