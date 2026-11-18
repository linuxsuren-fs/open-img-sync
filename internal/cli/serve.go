package cli

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/linuxsuren/open-img-sync/internal/aliyun"
	"github.com/linuxsuren/open-img-sync/internal/crypt"
	"github.com/linuxsuren/open-img-sync/internal/errs"
	"github.com/linuxsuren/open-img-sync/internal/server"
	"github.com/linuxsuren/open-img-sync/internal/store"
	synceng "github.com/linuxsuren/open-img-sync/internal/sync"
)

// serve 启动 Web + API 服务。骨架阶段提供 /healthz、/api/version、/api/system/info，
// 资源接口随 #2/#3/#5 逐步接入。
func serve(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	addr := fs.String("addr", envOr("OIS_ADDR", ":8080"), "监听地址，也可用环境变量 OIS_ADDR")
	dataDir := fs.String("data-dir", envOr("OIS_DATA_DIR", "data"), "数据目录，也可用环境变量 OIS_DATA_DIR")
	if err := fs.Parse(args); err != nil {
		return err
	}

	// 主密钥：环境变量 OIS_MASTER_KEY 优先，否则首启自动生成 0600 密钥文件（#10）
	keyPath := *dataDir + string(os.PathSeparator) + "master.key"
	key, keySource, err := crypt.LoadMasterKey(crypt.MasterKeyConfig{
		EnvVar:   "OIS_MASTER_KEY",
		FilePath: keyPath,
	})
	if err != nil {
		return errs.Wrap(errs.CodeMasterKeyFail, "主密钥不可用", err)
	}

	secrets := store.NewSecretBox(crypt.NewBox(key))
	db, err := store.Open(*dataDir + string(os.PathSeparator) + "open-img-sync.db")
	if err != nil {
		return err
	}
	defer db.Close()

	syncEngine := synceng.New(db, secrets, aliyun.NewProvider(secrets),
		*dataDir+string(os.PathSeparator)+"spool")

	srv := server.New(server.Options{
		Version:         version,
		Commit:          commit,
		BuildTime:       buildTime,
		Token:           os.Getenv("OIS_TOKEN"),
		DataDir:         *dataDir,
		MasterKeySource: keySource,
	}, server.Deps{
		DB:      db,
		Secrets: secrets,
		Aliyun:  aliyun.NewProvider(secrets),
		Sync:    syncEngine,
	})

	httpSrv := &http.Server{
		Addr:              *addr,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	ln, err := net.Listen("tcp", *addr)
	if err != nil {
		return fmt.Errorf("监听 %s: %w", *addr, err)
	}

	// 优雅退出：SIGINT/SIGTERM 后停止接收新连接、等待在途请求完成（同步任务级排空随 #8 扩展）
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	errCh := make(chan error, 1)
	go func() { errCh <- httpSrv.Serve(ln) }()
	slog.Info("open-img-sync serving",
		"version", version, "addr", *addr, "data_dir", *dataDir, "master_key_source", keySource)

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		// 优雅退出：停止接收新连接 → 等待在途请求 → 排空运行中的同步任务（有超时上限，#8）
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := httpSrv.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("优雅退出失败: %w", err)
		}
		drainTimeout := 30 * time.Second
		if raw := os.Getenv("OIS_SHUTDOWN_DRAIN_TIMEOUT"); raw != "" {
			if d, err := time.ParseDuration(raw); err == nil && d > 0 {
				drainTimeout = d
			}
		}
		if syncEngine.Drain(drainTimeout) {
			slog.Info("server stopped gracefully")
		} else {
			slog.Warn("server stopped with running tasks aborted", "drain_timeout", drainTimeout.String())
		}
		return nil
	}
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
