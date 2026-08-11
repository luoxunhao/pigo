package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func testRouter(t *testing.T, cfg Config) http.Handler {
	t.Helper()
	handler, err := NewRouter(cfg)
	if err != nil {
		t.Fatalf("NewRouter: %v", err)
	}
	return handler
}

func TestHealth(t *testing.T) {
	handler := testRouter(t, Config{Version: "test"})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var body struct {
		Healthy bool   `json:"healthy"`
		Version string `json:"version"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if !body.Healthy || body.Version != "test" {
		t.Fatalf("body = %+v", body)
	}
}

func TestOpenAPISpec(t *testing.T) {
	handler := testRouter(t, Config{})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/openapi.json", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "/api/v1/health") {
		t.Fatalf("spec does not contain health path: %s", rec.Body.String())
	}
}

func TestAPIDoc(t *testing.T) {
	handler := testRouter(t, Config{})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/doc", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Header().Get("Content-Type"), "text/html") {
		t.Fatalf("content type = %q", rec.Header().Get("Content-Type"))
	}
}

func TestBasicAuth(t *testing.T) {
	handler := testRouter(t, Config{Password: "secret"})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
	req.SetBasicAuth("pigo", "secret")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
}

func TestRequestID(t *testing.T) {
	handler := testRouter(t, Config{})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if got := rec.Header().Get("X-Request-Id"); got == "" {
		t.Fatal("missing X-Request-Id")
	}
}

func TestCORSAllowedOrigin(t *testing.T) {
	handler := testRouter(t, Config{AllowedOrigins: []string{"http://localhost:5173"}})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
	req.Header.Set("Origin", "http://localhost:5173")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "http://localhost:5173" {
		t.Fatalf("ACAO = %q", got)
	}
}

func TestCORSDisallowedOrigin(t *testing.T) {
	handler := testRouter(t, Config{AllowedOrigins: []string{"http://localhost:5173"}})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
	req.Header.Set("Origin", "http://evil.example")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("ACAO = %q, want empty", got)
	}
}

func TestCORSPreflight(t *testing.T) {
	handler := testRouter(t, Config{AllowedOrigins: []string{"http://localhost:5173"}})
	req := httptest.NewRequest(http.MethodOptions, "/api/v1/session", nil)
	req.Header.Set("Origin", "http://localhost:5173")
	req.Header.Set("Access-Control-Request-Method", http.MethodPost)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", rec.Code)
	}
}

func TestNotFoundUsesErrorEnvelope(t *testing.T) {
	handler := testRouter(t, Config{})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/not-found", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `"code"`) {
		t.Fatalf("body is not an error envelope: %s", rec.Body.String())
	}
}
