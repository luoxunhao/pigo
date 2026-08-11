package httpapi

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net"
	"net/http"
	"strings"
)

type contextKey string

const requestIDKey contextKey = "request-id"

// RequestID adds a server-generated request id to every request.
func RequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := newRequestID()
		w.Header().Set("X-Request-Id", id)
		next.ServeHTTP(w, r.WithContext(withRequestID(r.Context(), id)))
	})
}

func withRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, requestIDKey, id)
}

// Recoverer converts panics into a 500 error envelope.
func Recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				WriteError(w, r, Internal("internal server error"))
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// BasicAuth enforces HTTP Basic authentication when a password is configured.
func BasicAuth(password string) func(http.Handler) http.Handler {
	if password == "" {
		return func(next http.Handler) http.Handler { return next }
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			user, pass, ok := r.BasicAuth()
			if !ok || user != "pigo" || pass != password {
				w.Header().Set("WWW-Authenticate", `Basic realm="pigo"`)
				WriteError(w, r, &APIError{Status: http.StatusUnauthorized, Code: CodeUnauthorized, Message: "authentication required"})
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// CORS allows configured origins. Loopback and same-origin requests pass through.
func CORS(allowed []string) func(http.Handler) http.Handler {
	allowedMap := make(map[string]bool, len(allowed))
	for _, origin := range allowed {
		allowedMap[strings.TrimSpace(origin)] = true
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")
			if origin != "" && (allowedMap[origin] || sameOriginAllowed(r)) {
				w.Header().Set("Access-Control-Allow-Origin", origin)
				w.Header().Set("Vary", "Origin")
				w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
				w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, X-Request-Id")
				if r.Method == http.MethodOptions {
					w.WriteHeader(http.StatusNoContent)
					return
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}

func newRequestID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "req-unknown"
	}
	return "req-" + hex.EncodeToString(b[:])
}

func sameOriginAllowed(r *http.Request) bool {
	host := r.Host
	hostname, _, err := net.SplitHostPort(host)
	if err != nil {
		hostname = host
	}
	return hostname == "127.0.0.1" || hostname == "localhost" || hostname == "::1"
}

// responseJSON writes a JSON response with a status code.
func responseJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
