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
	HTTP     *http.Client
	Now      func() time.Time
	Nonce    func() string
}

type tokenResponse struct {
	AuthorizationToken struct {
		AuthorizationToken string `json:"AuthorizationToken"`
		TempUserName       string `json:"TempUserName"`
		ExpireTime         int64  `json:"ExpireTime"` // epoch 秒
	} `json:"AuthorizationToken"`
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
		return Token{}, errs.Wrap(CodeFetchFailed, "临时凭据获取失败", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return Token{}, errs.Wrap(CodeFetchFailed, "临时凭据获取失败", err)
	}

	if resp.StatusCode != http.StatusOK {
		var apiErr apiError
		if json.Unmarshal(body, &apiErr) == nil && apiErr.Code != "" {
			return Token{}, classifyAPIError(apiErr.Code)
		}
		return Token{}, errs.Wrap(CodeFetchFailed, "临时凭据获取失败",
			fmt.Errorf("http %d: %s", resp.StatusCode, truncate(string(body), 200)))
	}
	var parsed tokenResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return Token{}, errs.Wrap(CodeFetchFailed, "临时凭据获取失败", err)
	}
	raw := parsed.AuthorizationToken.AuthorizationToken
	user, pass, ok := decodeBasicToken(raw)
	if !ok || user == "" || pass == "" {
		return Token{}, errs.New(CodeFetchFailed, "临时凭据获取失败")
	}
	return Token{
		Username:   user,
		Password:   pass,
		ExpireTime: time.Unix(parsed.AuthorizationToken.ExpireTime, 0),
	}, nil
}

// classifyAPIError 把阿里云错误码映射为稳定错误码（1001/1002/1003/1004）。
func classifyAPIError(code string) error {
	switch {
	case strings.Contains(code, "InvalidAccessKeyId"), strings.Contains(code, "SignatureDoesNotMatch"):
		return errs.New(CodeAKInvalid, "阿里云 AccessKey 无效")
	case strings.Contains(code, "Forbidden"), strings.Contains(code, "Unauthorized"),
		strings.Contains(code, "NoPermission"), strings.Contains(code, "AccessDenied"):
		return errs.New(CodeNoAuth, "AccessKey 未授权该 ACR 实例")
	case strings.Contains(code, "Throttling"), strings.Contains(code, "RequestLimitExceeded"):
		return errs.New(CodeRateLimited, "阿里云接口限流，请稍后重试")
	default:
		return errs.New(CodeFetchFailed, "临时凭据获取失败")
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
