package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func authTarget(t *testing.T, token string) *Server {
	t.Helper()
	return New(Options{Version: "t", Token: token}, Deps{})
}

func TestAuthDisabledByDefault(t *testing.T) {
	srv := authTarget(t, "")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/version", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("未配置 Token 时 /api/version 应放行, got %d", rec.Code)
	}
}

func TestAuthRejectsMissingOrWrongToken(t *testing.T) {
	srv := authTarget(t, "s3cret-token")
	for _, header := range []string{"", "Bearer wrong", "Basic s3cret-token", "s3cret-token"} {
		req := httptest.NewRequest(http.MethodGet, "/api/version", nil)
		if header != "" {
			req.Header.Set("Authorization", header)
		}
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("Authorization=%q 应 401, got %d", header, rec.Code)
		}
		var body map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("401 响应应为 JSON: %v", err)
		}
		if code, _ := body["code"].(float64); int(code) != 5004 {
			t.Fatalf("401 响应 code = %v, want 5004", body["code"])
		}
		// 错误信封不得回显任何 token 片段
		if strings.Contains(rec.Body.String(), "s3cret-token") {
			t.Fatalf("401 响应泄露了 token: %s", rec.Body.String())
		}
	}
}

func TestAuthAcceptsCorrectToken(t *testing.T) {
	srv := authTarget(t, "s3cret-token")
	req := httptest.NewRequest(http.MethodGet, "/api/version", nil)
	req.Header.Set("Authorization", "Bearer s3cret-token")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("正确 Token 应放行, got %d", rec.Code)
	}
}

// TestHealthzExemptFromAuth 锁定 #10 验收项：开启 Token 鉴权后 healthz 仍 200。
func TestHealthzExemptFromAuth(t *testing.T) {
	srv := authTarget(t, "s3cret-token")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("healthz 应豁免鉴权, got %d", rec.Code)
	}
}

func TestSystemInfoMasksNothing(t *testing.T) {
	srv := New(Options{Version: "v1", DataDir: "/data", MasterKeySource: "file", Token: "tk"}, Deps{})
	req := httptest.NewRequest(http.MethodGet, "/api/system/info", nil)
	req.Header.Set("Authorization", "Bearer tk")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("system info = %d", rec.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["master_key_source"] != "file" || body["auth_enabled"] != true {
		t.Fatalf("system info = %v", body)
	}
}
