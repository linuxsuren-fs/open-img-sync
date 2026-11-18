package aliyun

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/linuxsuren/open-img-sync/internal/errs"
)

// 凭据服务段（1xxx）错误码。
const (
	CodeAKInvalid   = 1001 // 阿里云 AccessKey 无效
	CodeNoAuth      = 1002 // AccessKey 未授权该 ACR 实例
	CodeRateLimited = 1003 // 阿里云接口限流
	CodeFetchFailed = 1004 // 临时凭据获取失败
)

// APIVersion ACR（容器镜像服务）RPC API 版本。
const APIVersion = "2018-12-01"

// Token 临时登录凭据。
type Token struct {
	Username   string
	Password   string
	ExpireTime time.Time
}

// Client 调用 GetAuthorizationToken 的最小客户端。
type Client struct {
	AK string
	SK string
	// Region 例如 cn-hangzhou；Endpoint 非空时优先（测试注入 fake 服务）
	Region   string
	Endpoint string
	// Instance ACR 企业版实例 ID（如 cri-xxxx）；GetAuthorizationToken 必填
	Instance string
	HTTP     *http.Client
	Now      func() time.Time
	Nonce    func() string
}

// tokenResponse 对应 ACR EE GetAuthorizationToken 的真实扁平结构：
// {"IsSuccess":true,"RequestId":"...","AuthorizationToken":"<base64 user:pass>",
//
//	"TempUserName":"...","ExpireTime":<epoch>}
type tokenResponse struct {
	IsSuccess          bool        `json:"IsSuccess"`
	Code               string      `json:"Code"`
	AuthorizationToken string      `json:"AuthorizationToken"`
	TempUserName       string      `json:"TempUserName"`
	ExpireTime         json.Number `json:"ExpireTime"`
}

type apiError struct {
	RequestID string `json:"RequestId"`
	Message   string `json:"Message"`
	Code      string `json:"Code"`
}

// EndpointURL 默认区域端点：https://cr.{region}.aliyuncs.com/。
func (c *Client) EndpointURL() string {
	if c.Endpoint != "" {
		return strings.TrimRight(c.Endpoint, "/") + "/"
	}
	return fmt.Sprintf("https://cr.%s.aliyuncs.com/", c.Region)
}

// endpointHost 返回端点主机名（错误提示用）。
func (c *Client) endpointHost() string {
	raw := c.EndpointURL()
	if u, err := url.Parse(raw); err == nil && u.Host != "" {
		return u.Host
	}
	return raw
}

// GetAuthorizationToken 换取临时登录凭据（base64 token 解为 username:password）。
func (c *Client) GetAuthorizationToken(ctx context.Context) (Token, error) {
	now := time.Now
	if c.Now != nil {
		now = c.Now
	}
	nonce := c.Nonce
	if nonce == nil {
		nonce = defaultNonce
	}
	params := map[string]string{
		"Action":           "GetAuthorizationToken",
		"Version":          APIVersion,
		"Format":           "JSON",
		"AccessKeyId":      c.AK,
		"SignatureMethod":  "HMAC-SHA1",
		"SignatureVersion": "1.0",
		"SignatureNonce":   nonce(),
		"Timestamp":        now().UTC().Format("2006-01-02T15:04:05Z"),
	}
	// ACR 企业版必填实例 ID（cri-xxxx）
	if c.Instance != "" {
		params["InstanceId"] = c.Instance
	}
	_, signature := SignRPC(c.AK, c.SK, params)
	params["Signature"] = signature

	form := url.Values{}
	for k, v := range params {
		form.Set(k, v)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.EndpointURL(), strings.NewReader(form.Encode()))
	if err != nil {
		return Token{}, errs.Wrap(CodeFetchFailed, "临时凭据获取失败", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Authorization", "ACS "+c.AK+":"+signature)

	client := c.HTTP
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return Token{}, errs.Wrap(CodeFetchFailed,
			"临时凭据获取失败：无法连接阿里云接口 "+c.endpointHost()+"，请检查网络与地域配置", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return Token{}, errs.Wrap(CodeFetchFailed, "临时凭据获取失败：响应读取中断", err)
	}

	if resp.StatusCode != http.StatusOK {
		var apiErr apiError
		if json.Unmarshal(body, &apiErr) == nil && apiErr.Code != "" {
			return Token{}, classifyAPIError(apiErr.Code)
		}
		return Token{}, errs.Wrap(CodeFetchFailed,
			fmt.Sprintf("临时凭据获取失败：接口返回异常（HTTP %d），详情见服务日志", resp.StatusCode),
			fmt.Errorf("http %d: %s", resp.StatusCode, truncate(string(body), 200)))
	}
	var parsed tokenResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return Token{}, errs.Wrap(CodeFetchFailed, "临时凭据获取失败：响应无法解析，详情见服务日志", err)
	}
	if !parsed.IsSuccess {
		if parsed.Code != "" {
			return Token{}, classifyAPIError(parsed.Code)
		}
		return Token{}, errs.Wrap(CodeFetchFailed, "临时凭据获取失败：接口返回未成功",
			fmt.Errorf("body: %s", truncate(string(body), 200)))
	}
	user, pass, ok := decodeBasicToken(parsed.AuthorizationToken)
	if !ok {
		// 个别实例不回 base64 token：退回 TempUserName + token 作为密码
		if parsed.TempUserName != "" && parsed.AuthorizationToken != "" {
			user, pass = parsed.TempUserName, parsed.AuthorizationToken
		} else {
			return Token{}, errs.New(CodeFetchFailed, "临时凭据获取失败：响应缺少凭据字段")
		}
	}
	expire := parseExpire(parsed.ExpireTime)
	return Token{
		Username:   user,
		Password:   pass,
		ExpireTime: expire,
	}, nil
}

// parseExpire 兼容秒/毫秒两种 epoch 口径（>1e12 视为毫秒）。
func parseExpire(n json.Number) time.Time {
	v, err := n.Int64()
	if err != nil || v <= 0 {
		return time.Now().Add(50 * time.Minute) // 无法解析时按保守 50 分钟计
	}
	if v > 1e12 {
		return time.UnixMilli(v)
	}
	return time.Unix(v, 0)
}

// classifyAPIError 把阿里云错误码映射为稳定错误码（1001/1002/1003/1004）。
// 未识别的错误码在提示中带上阿里云错误码，便于排查（技术细节仍只进日志）。
func classifyAPIError(code string) error {
	switch {
	case strings.Contains(code, "InvalidAccessKeyId"), strings.Contains(code, "SignatureDoesNotMatch"):
		return errs.New(CodeAKInvalid, "阿里云 AccessKey 无效")
	case strings.Contains(code, "Forbidden"), strings.Contains(code, "Unauthorized"),
		strings.Contains(code, "NoPermission"), strings.Contains(code, "AccessDenied"):
		return errs.New(CodeNoAuth, "AccessKey 未授权该 ACR 实例")
	case strings.Contains(code, "Throttling"), strings.Contains(code, "RequestLimitExceeded"):
		return errs.New(CodeRateLimited, "阿里云接口限流，请稍后重试")
	case strings.Contains(code, "MissingInstanceId"):
		return errs.New(CodeFetchFailed, "临时凭据获取失败：未携带实例 ID，请补全阿里云实例名（如 cri-xxxx）后重试")
	case strings.Contains(code, "InvalidTimeStamp"), strings.Contains(code, "InvalidTimeRange"):
		return errs.New(CodeFetchFailed, "临时凭据获取失败：请求时间戳无效，请检查服务器系统时间")
	case strings.Contains(code, "SignatureNonceUsed"):
		return errs.New(CodeFetchFailed, "临时凭据获取失败：请求被判定重放，请重试")
	case strings.Contains(code, "MissingParameter"), strings.Contains(code, "InvalidParameter"):
		return errs.New(CodeFetchFailed, "临时凭据获取失败：请求参数不合法（错误码 "+code+"），详情见服务日志")
	case strings.Contains(code, "InternalError"), strings.Contains(code, "ServiceUnavailable"):
		return errs.New(CodeFetchFailed, "临时凭据获取失败：阿里云服务暂时不可用，请稍后重试")
	default:
		return errs.New(CodeFetchFailed, "临时凭据获取失败（阿里云错误码 "+code+"），详情见服务日志")
	}
}

func decodeBasicToken(raw string) (user, pass string, ok bool) {
	decoded, err := base64Decode(raw)
	if err != nil {
		return "", "", false
	}
	idx := strings.Index(string(decoded), ":")
	if idx < 0 {
		return "", "", false
	}
	return string(decoded)[:idx], string(decoded)[idx+1:], true
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
