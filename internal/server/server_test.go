package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHealthz(t *testing.T) {
	srv := New(Options{Version: "test"}, Deps{})
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("healthz status = %d, want 200", rec.Code)
	}
	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("healthz body 不是合法 JSON: %v", err)
	}
	if body["status"] != "ok" {
		t.Fatalf("healthz status field = %q, want ok", body["status"])
	}
}

func TestVersion(t *testing.T) {
	srv := New(Options{Version: "v0.0.1", Commit: "abc1234", BuildTime: "2026-01-01T00:00:00Z"}, Deps{})
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/version", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("version status = %d, want 200", rec.Code)
	}
	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("version body 不是合法 JSON: %v", err)
	}
	if body["version"] != "v0.0.1" || body["commit"] != "abc1234" {
		t.Fatalf("version body = %v", body)
	}
}
