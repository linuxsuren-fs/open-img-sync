// Package webui 嵌入 web/dist 构建产物，使单二进制可直接服务前端。
// dist 由 make web 生成并提交进仓库——纯 Go 环境（无 Node）也能 make build，
// 这是 #6/#7 的显式取舍：提交构建产物换取「拷贝即用」的发行形态。
package webui

import (
	"embed"
	"io/fs"
	"net/http"
)

//go:embed all:dist
var distFS embed.FS

// Handler 返回 SPA 静态服务：命中静态文件直接返回，未命中回退 index.html。
func Handler() http.Handler {
	sub, err := fs.Sub(distFS, "dist")
	if err != nil {
		return http.NotFoundHandler()
	}
	fileServer := http.FileServer(http.FS(sub))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		if path != "/" {
			if _, err := fs.Stat(sub, path[1:]); err != nil {
				// SPA 回退：非静态路径直接返回入口页内容（避免 FileServer 的 index 重定向环）
				index, readErr := fs.ReadFile(sub, "index.html")
				if readErr != nil {
					http.NotFound(w, r)
					return
				}
				w.Header().Set("Content-Type", "text/html; charset=utf-8")
				_, _ = w.Write(index)
				return
			}
		}
		fileServer.ServeHTTP(w, r)
	})
}
