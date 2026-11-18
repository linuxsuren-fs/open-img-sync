package server

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/linuxsuren/open-img-sync/internal/errs"
)

// withAuth 对 /api/* 启用 Bearer Token 鉴权；/healthz 豁免（容器探针不受影响）。
// 未配置 Token 时鉴权关闭（默认本机/内网单用户形态）。
func (s *Server) withAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.opt.Token == "" || !strings.HasPrefix(r.URL.Path, "/api/") || r.URL.Path == "/healthz" {
			next.ServeHTTP(w, r)
			return
		}
		header := r.Header.Get("Authorization")
		if header != "Bearer "+s.opt.Token {
			s.writeErr(w, errs.New(errs.CodeUnauthorized, "未授权的请求"))
			return
		}
		next.ServeHTTP(w, r)
	})
}

// statusRecorder 记录响应状态码用于访问日志。
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

// withAccessLog 输出访问日志（不含请求体，避免凭据进日志）。
func withAccessLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec := &statusRecorder{ResponseWriter: w, status: 200}
		start := time.Now()
		next.ServeHTTP(rec, r)
		slog.Info("http",
			"method", r.Method,
			"path", r.URL.Path,
			"status", rec.status,
			"cost_ms", time.Since(start).Milliseconds(),
		)
	})
}

// writeJSON 统一 JSON 响应。
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// writeErr 把业务错误写为 {code,message} 信封；技术根因只进日志。
func (s *Server) writeErr(w http.ResponseWriter, e *errs.E) {
	if cause := e.Cause(); cause != nil {
		slog.Error("request failed", "code", e.Code, "cause", cause.Error())
	}
	writeJSON(w, e.HTTPStatus(), e)
}
