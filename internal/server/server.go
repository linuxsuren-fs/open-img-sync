// Package server 提供 Web + API 服务。
// 已接入：/healthz、/api/version、/api/system/info、服务器资源（#2）与鉴权中间件（#10）；
// registry / 任务路由随 #3/#5 接入。
package server

import (
	"context"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/linuxsuren/open-img-sync/internal/aliyun"
	"github.com/linuxsuren/open-img-sync/internal/sshx"
	"github.com/linuxsuren/open-img-sync/internal/store"
	"github.com/linuxsuren/open-img-sync/internal/sync"
	webui "github.com/linuxsuren/open-img-sync/web"
)

// Options 聚合服务启动所需的注入项。
type Options struct {
	Version   string
	Commit    string
	BuildTime string
	// Token 非空时对 /api/* 启用 Bearer 鉴权（/healthz 豁免）
	Token string
	// DataDir 数据目录（系统信息展示用）
	DataDir string
	// MasterKeySource 主密钥来源：env / file
	MasterKeySource string
	// TestTimeout 连通性测试超时（默认 10s，#2）
	TestTimeout time.Duration
}

// Deps 服务器的持久化与加密依赖。
type Deps struct {
	DB      *store.DB
	Secrets *store.SecretBox
	// Aliyun 临时凭据提供者（aliyun-temp 类型 registry 用，可为 nil）
	Aliyun *aliyun.Provider
	// Sync 同步引擎（任务接口用，可为 nil）
	Sync *sync.Engine
}

// Server 持有路由与依赖。
type Server struct {
	opt       Options
	deps      Deps
	mux       *http.ServeMux
	http      *http.Server
	sshTester func(ctx context.Context, opt sshx.Options) sshx.TestResult
	now       func() time.Time
}

// New 构造服务实例。
func New(opt Options, deps Deps) *Server {
	if opt.TestTimeout <= 0 {
		opt.TestTimeout = sshx.DefaultTimeout
	}
	s := &Server{
		opt:       opt,
		deps:      deps,
		mux:       http.NewServeMux(),
		sshTester: sshx.TestDocker,
		now:       time.Now,
	}
	s.routes()
	return s
}

func (s *Server) routes() {
	s.mux.HandleFunc("GET /healthz", s.handleHealthz)
	s.mux.HandleFunc("GET /api/version", s.handleVersion)
	s.mux.HandleFunc("GET /api/system/info", s.handleSystemInfo)

	if s.deps.DB != nil {
		_ = s.deps.DB.EnsureBuckets(bucketServers, bucketRegistries, bucketImages)
		s.mux.HandleFunc("GET /api/servers", s.handleServerList)
		s.mux.HandleFunc("POST /api/servers", s.handleServerCreate)
		s.mux.HandleFunc("GET /api/servers/export", s.handleServerExport)
		s.mux.HandleFunc("POST /api/servers/import", s.handleServerImport)
		s.mux.HandleFunc("GET /api/servers/{id}", s.handleServerGet)
		s.mux.HandleFunc("PUT /api/servers/{id}", s.handleServerUpdate)
		s.mux.HandleFunc("DELETE /api/servers/{id}", s.handleServerDelete)
		s.mux.HandleFunc("POST /api/servers/{id}/test", s.handleServerTest)

		s.mux.HandleFunc("GET /api/registries", s.handleRegistryList)
		s.mux.HandleFunc("POST /api/registries", s.handleRegistryCreate)
		s.mux.HandleFunc("GET /api/registries/{id}", s.handleRegistryGet)
		s.mux.HandleFunc("PUT /api/registries/{id}", s.handleRegistryUpdate)
		s.mux.HandleFunc("DELETE /api/registries/{id}", s.handleRegistryDelete)
		s.mux.HandleFunc("POST /api/registries/{id}/test", s.handleRegistryTest)
		s.mux.HandleFunc("POST /api/registries/{id}/images/batch", s.handleImageBatch)
		s.mux.HandleFunc("GET /api/registries/{id}/images", s.handleImageList)
		s.mux.HandleFunc("DELETE /api/images/{id}", s.handleImageDelete)
	}

	if s.deps.Sync != nil {
		_ = s.deps.DB.EnsureBuckets(bucketTasksAPI)
		s.mux.HandleFunc("GET /api/tasks", s.handleTaskList)
		s.mux.HandleFunc("POST /api/tasks", s.handleTaskCreate)
		s.mux.HandleFunc("GET /api/tasks/{id}", s.handleTaskGet)
		s.mux.HandleFunc("POST /api/tasks/{id}/cancel", s.handleTaskCancel)
		s.mux.HandleFunc("GET /api/tasks/{id}/logs", s.handleTaskLogs)
	}

	// 前端静态资源（嵌入产物）兜底挂载：具体 /api 路由优先匹配
	s.mux.Handle("/", webui.Handler())
}

// Handler 暴露路由（含中间件）供测试与上层复用。
func (s *Server) Handler() http.Handler {
	return s.withAuth(withAccessLog(s.mux))
}

// ListenAndServe 阻塞监听。
func (s *Server) ListenAndServe(addr string) error {
	s.http = &http.Server{
		Addr:              addr,
		Handler:           s.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	slog.Info("listening", "addr", addr, "version", s.opt.Version)
	return s.http.Serve(ln)
}

func (s *Server) handleHealthz(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleVersion(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{
		"version":    s.opt.Version,
		"commit":     s.opt.Commit,
		"build_time": s.opt.BuildTime,
	})
}

func (s *Server) handleSystemInfo(w http.ResponseWriter, _ *http.Request) {
	authEnabled := s.opt.Token != ""
	writeJSON(w, http.StatusOK, map[string]any{
		"version":           s.opt.Version,
		"data_dir":          s.opt.DataDir,
		"master_key_source": s.opt.MasterKeySource,
		"auth_enabled":      authEnabled,
	})
}
