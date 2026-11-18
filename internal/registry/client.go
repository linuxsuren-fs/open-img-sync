// Package registry 提供 Docker Registry V2 客户端与镜像引用解析（#3）。
// 同步引擎（#5）在此之上扩展 manifest / blob 操作。
package registry

import (
	"net/http"
	"strings"
	"time"
)

// registry 段（3xxx）错误码。
const (
	CodeUnreachable = 3001 // registry 网络不可达
	CodeAuthFailed  = 3002 // registry 认证失败
	CodeNotRegistry = 3003 // 目标不是 registry 服务
	CodeInvalidRef  = 3004 // 镜像引用不合法
)

// NormalizeAddress 地址规范化：自动补全 https://、剥离末尾斜杠。
func NormalizeAddress(address string) string {
	address = strings.TrimSpace(address)
	if address == "" {
		return address
	}
	if !strings.Contains(address, "://") {
		address = "https://" + address
	}
	return strings.TrimRight(address, "/")
}

// PingResult /v2/ 探测结果。
type PingResult struct {
	OK        bool   `json:"ok"`
	LatencyMS int64  `json:"latency_ms"`
	Code      int    `json:"code"`
	Message   string `json:"message"`
}

// httpClient 可在测试中替换的 HTTP 客户端构造。
var httpClient = func() *http.Client { return &http.Client{Timeout: 10 * time.Second} }

// Ping 探测 GET {address}/v2/，区分网络不可达 / 认证失败 / 非 registry 服务。
// username/secret 为空表示匿名探测。
func Ping(address, username, secret string) PingResult {
	address = NormalizeAddress(address)
	client := httpClient()
	start := time.Now()

	req, err := http.NewRequest(http.MethodGet, address+"/v2/", nil)
	if err != nil {
		return PingResult{OK: false, Code: CodeNotRegistry, Message: "registry 地址不合法"}
	}
	if username != "" || secret != "" {
		req.SetBasicAuth(username, secret)
	}
	resp, err := client.Do(req)
	if err != nil {
		return PingResult{OK: false, LatencyMS: sinceMS(start),
			Code: CodeUnreachable, Message: "registry 网络不可达"}
	}
	defer resp.Body.Close()

	switch {
	case resp.StatusCode == http.StatusOK:
		return PingResult{OK: true, LatencyMS: sinceMS(start), Code: 0, Message: "registry 可达"}
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return PingResult{OK: false, LatencyMS: sinceMS(start),
			Code: CodeAuthFailed, Message: "registry 认证失败"}
	default:
		return PingResult{OK: false, LatencyMS: sinceMS(start),
			Code: CodeNotRegistry, Message: "目标不是 registry 服务"}
	}
}

func sinceMS(start time.Time) int64 { return time.Since(start).Milliseconds() }
