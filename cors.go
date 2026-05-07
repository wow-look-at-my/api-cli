package main

import (
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
)

// CorsLevel controls how the MCP HTTP/SSE server handles cross-origin
// requests. Levels go from most-open (CorsDisabled) to most-locked
// (CorsEnabled); the default is CorsStrict.
type CorsLevel int

const (
	// CorsDisabled is wide open: Access-Control-Allow-Origin: *, all
	// methods, all headers, preflight always succeeds. No protection.
	CorsDisabled CorsLevel = iota
	// CorsPermissive allows localhost-style origins (localhost, 127.0.0.1,
	// ::1, any port) plus same-origin. Useful for browser dev tools.
	CorsPermissive
	// CorsStrict only allows requests whose Origin matches the server's
	// bound host:port. When the server binds to 0.0.0.0/::, any host with
	// the matching port is accepted.
	CorsStrict
	// CorsEnabled fully locks down: no Access-Control-Allow-Origin is ever
	// emitted, and preflight (OPTIONS) requests are answered with 403.
	CorsEnabled
)

// String returns the canonical flag value.
func (l CorsLevel) String() string {
	switch l {
	case CorsDisabled:
		return "disabled"
	case CorsPermissive:
		return "permissive"
	case CorsStrict:
		return "strict"
	case CorsEnabled:
		return "enabled"
	}
	return "unknown"
}

// parseCorsLevel parses a flag value into a CorsLevel. Aliases are
// accepted for each level.
func parseCorsLevel(s string) (CorsLevel, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "disabled", "off", "none", "open":
		return CorsDisabled, nil
	case "permissive", "lax", "loose", "localhost":
		return CorsPermissive, nil
	case "strict", "same-origin", "sameorigin":
		return CorsStrict, nil
	case "enabled", "on", "lockdown", "locked", "block":
		return CorsEnabled, nil
	}
	return 0, fmt.Errorf("invalid --cors value %q; want one of: disabled, permissive, strict, enabled", s)
}

// withCORS wraps inner with CORS handling at the given level. listenAddr
// is the server's bound TCP address (host:port); used to compute
// same-origin matches for CorsStrict and CorsPermissive.
func withCORS(inner http.Handler, level CorsLevel, listenAddr string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		allowed := corsAllowOrigin(level, origin, listenAddr)
		if r.Method == http.MethodOptions && r.Header.Get("Access-Control-Request-Method") != "" {
			handleCorsPreflight(w, r, level, origin, allowed)
			return
		}
		if origin != "" && allowed {
			setCorsAllowOrigin(w, level, origin)
			if level == CorsDisabled {
				w.Header().Set("Access-Control-Expose-Headers", "*")
			} else {
				w.Header().Set("Access-Control-Expose-Headers", "Mcp-Session-Id")
			}
		}
		inner.ServeHTTP(w, r)
	})
}

// handleCorsPreflight answers an OPTIONS preflight based on the level.
func handleCorsPreflight(w http.ResponseWriter, r *http.Request, level CorsLevel, origin string, allowed bool) {
	if level == CorsEnabled || !allowed {
		w.WriteHeader(http.StatusForbidden)
		return
	}
	setCorsAllowOrigin(w, level, origin)

	if reqMethod := r.Header.Get("Access-Control-Request-Method"); reqMethod != "" {
		w.Header().Set("Access-Control-Allow-Methods", reqMethod)
	} else {
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS, DELETE")
	}

	if level == CorsDisabled {
		w.Header().Set("Access-Control-Allow-Headers", "*")
	} else if reqHeaders := r.Header.Get("Access-Control-Request-Headers"); reqHeaders != "" {
		w.Header().Set("Access-Control-Allow-Headers", reqHeaders)
	} else {
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, Mcp-Session-Id, Last-Event-ID")
	}

	w.Header().Set("Access-Control-Max-Age", "600")
	w.WriteHeader(http.StatusNoContent)
}

// setCorsAllowOrigin writes Access-Control-Allow-Origin and the matching
// Vary header when the value is origin-specific.
func setCorsAllowOrigin(w http.ResponseWriter, level CorsLevel, origin string) {
	if level == CorsDisabled {
		if origin != "" {
			w.Header().Set("Access-Control-Allow-Origin", origin)
		} else {
			w.Header().Set("Access-Control-Allow-Origin", "*")
		}
		w.Header().Set("Access-Control-Allow-Credentials", "true")
		w.Header().Add("Vary", "Origin")
		return
	}
	if origin == "" {
		return
	}
	w.Header().Set("Access-Control-Allow-Origin", origin)
	w.Header().Add("Vary", "Origin")
}

// corsAllowOrigin reports whether origin is allowed under level. An empty
// origin always passes through (non-browser clients don't send Origin).
func corsAllowOrigin(level CorsLevel, origin, listenAddr string) bool {
	if origin == "" {
		return true
	}
	switch level {
	case CorsDisabled:
		return true
	case CorsEnabled:
		return false
	case CorsPermissive:
		return localhostOrigin(origin) || sameOrigin(origin, listenAddr)
	case CorsStrict:
		return sameOrigin(origin, listenAddr)
	}
	return false
}

// localhostOrigin reports whether origin's host is a localhost-style name.
func localhostOrigin(origin string) bool {
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" {
		return false
	}
	switch u.Hostname() {
	case "localhost", "127.0.0.1", "::1":
		return true
	}
	return false
}

// sameOrigin reports whether origin matches listenAddr's host:port. When
// the listen host is unspecified (0.0.0.0/::/empty), any host with the
// matching port is accepted, since the server is reachable on any
// interface.
func sameOrigin(origin, listenAddr string) bool {
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" {
		return false
	}
	listenHost, listenPort, err := net.SplitHostPort(listenAddr)
	if err != nil {
		return false
	}
	originPort := u.Port()
	if originPort == "" {
		switch u.Scheme {
		case "https":
			originPort = "443"
		default:
			originPort = "80"
		}
	}
	if originPort != listenPort {
		return false
	}
	if listenHost == "" || listenHost == "0.0.0.0" || listenHost == "::" {
		return true
	}
	return u.Hostname() == listenHost
}
