package cli

import (
	"flag"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"
)

// selfCheck 内置健康自检（容器 HEALTHCHECK 用，不依赖 wget/curl）：
// 探测本机服务的 /healthz，2xx 退出码 0，否则非零退出。
func selfCheck(args []string) error {
	fs := flag.NewFlagSet("self-check", flag.ContinueOnError)
	addr := fs.String("addr", os.Getenv("OIS_SELF_CHECK_ADDR"), "服务地址；缺省取 OIS_ADDR（仅替换为 127.0.0.1）")
	if err := fs.Parse(args); err != nil {
		return err
	}
	target := *addr
	if target == "" {
		target = envOr("OIS_ADDR", ":8080")
		// 容器内自检走回环：":8080" → "127.0.0.1:8080"
		if strings.HasPrefix(target, ":") {
			target = "127.0.0.1" + target
		}
	}
	if !strings.HasPrefix(target, "http") {
		target = "http://" + target
	}
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(strings.TrimRight(target, "/") + "/healthz")
	if err != nil {
		return fmt.Errorf("健康检查失败：服务未响应")
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		fmt.Println("健康检查通过")
		return nil
	}
	return fmt.Errorf("健康检查失败：HTTP %d", resp.StatusCode)
}
