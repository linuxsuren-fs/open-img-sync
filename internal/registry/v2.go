package registry

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/linuxsuren/open-img-sync/internal/errs"
)

// 同步段（4xxx）错误码（源/目标侧语义由 sync 引擎映射使用）。
const (
	CodeSourceAuth = 4001 // 源 registry 认证失败
	CodeTransfer   = 4002 // 传输中断
	CodeTargetLoad = 4004 // 目标机加载镜像失败
	CodeRelayPush  = 4007 // 推送中转 registry 失败
)

// Manifest 媒体类型。
const (
	MediaTypeDockerManifestList = "application/vnd.docker.distribution.manifest.list.v2+json"
	MediaTypeOCIImageIndex      = "application/vnd.oci.image.index.v1+json"
	MediaTypeDockerManifest     = "application/vnd.docker.distribution.manifest.v2+json"
	MediaTypeOCIImageManifest   = "application/vnd.oci.image.manifest.v1+json"

	acceptManifests = MediaTypeDockerManifestList + ", " + MediaTypeOCIImageIndex + ", " +
		MediaTypeDockerManifest + ", " + MediaTypeOCIImageManifest
)

// Descriptor blob/manifest 描述符。
type Descriptor struct {
	MediaType string    `json:"mediaType"`
	Size      int64     `json:"size"`
	Digest    string    `json:"digest"`
	Platform  *Platform `json:"platform,omitempty"`
}

// Platform 目标平台。
type Platform struct {
	Architecture string `json:"architecture"`
	OS           string `json:"os"`
	Variant      string `json:"variant,omitempty"`
}

// ImageManifest schema2 / OCI 镜像 manifest。
type ImageManifest struct {
	SchemaVersion int          `json:"schemaVersion"`
	MediaType     string       `json:"mediaType"`
	Config        Descriptor   `json:"config"`
	Layers        []Descriptor `json:"layers"`
}

// IndexList manifest list / OCI index。
type IndexList struct {
	SchemaVersion int          `json:"schemaVersion"`
	MediaType     string       `json:"mediaType"`
	Manifests     []Descriptor `json:"manifests"`
}

// IsIndex 判断媒体类型是否为多架构索引。
func IsIndex(mediaType string) bool {
	return mediaType == MediaTypeDockerManifestList || mediaType == MediaTypeOCIImageIndex
}

// IsImageManifest 判断是否为单镜像 manifest。
func IsImageManifest(mediaType string) bool {
	return mediaType == MediaTypeDockerManifest || mediaType == MediaTypeOCIImageManifest
}

// Client Registry V2 API 客户端（A2 纯 Go 直拷路径）。
type Client struct {
	Address  string // 已规范化
	Username string
	Password string
	HTTP     *http.Client
	// bearerToken 401 挑战后获取的令牌（匿名拉取 Docker Hub 等场景）
	bearerToken string
}

// NewClient 构造客户端。
func NewClient(address, username, password string) *Client {
	return &Client{
		Address:  NormalizeAddress(address),
		Username: username,
		Password: password,
		HTTP:     &http.Client{Timeout: 5 * time.Minute},
	}
}

// do 发送带认证的请求；处理一次 Bearer 挑战。
func (c *Client) do(ctx context.Context, req *http.Request) (*http.Response, error) {
	if c.bearerToken != "" {
		req.Header.Set("Authorization", "Bearer "+c.bearerToken)
	} else if c.Username != "" || c.Password != "" {
		req.SetBasicAuth(c.Username, c.Password)
	}
	resp, err := c.HTTP.Do(req.WithContext(ctx))
	if err != nil {
		return nil, errs.Wrap(CodeUnreachable, "registry 网络不可达", err)
	}
	if resp.StatusCode == http.StatusUnauthorized && c.bearerToken == "" {
		challenge := resp.Header.Get("WWW-Authenticate")
		resp.Body.Close()
		if strings.HasPrefix(challenge, "Bearer ") {
			if e := c.fetchBearerToken(ctx, challenge); e != nil {
				return nil, e
			}
			return c.do(ctx, req.Clone(ctx))
		}
		return nil, errs.New(CodeAuthFailed, "registry 认证失败")
	}
	return resp, nil
}

// fetchBearerToken 处理 Bearer 挑战（realm/service/scope）。
func (c *Client) fetchBearerToken(ctx context.Context, challenge string) error {
	params := parseChallenge(challenge)
	realm := params["realm"]
	if realm == "" {
		return errs.New(CodeAuthFailed, "registry 认证失败")
	}
	tokenReq, err := http.NewRequestWithContext(ctx, http.MethodGet, realm, nil)
	if err != nil {
		return errs.Wrap(CodeAuthFailed, "registry 认证失败", err)
	}
	q := tokenReq.URL.Query()
	if s := params["service"]; s != "" {
		q.Set("service", s)
	}
	if s := params["scope"]; s != "" {
		q.Set("scope", s)
	}
	tokenReq.URL.RawQuery = q.Encode()
	if c.Username != "" || c.Password != "" {
		tokenReq.SetBasicAuth(c.Username, c.Password)
	}
	resp, err := c.HTTP.Do(tokenReq)
	if err != nil {
		return errs.Wrap(CodeUnreachable, "registry 网络不可达", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return errs.New(CodeAuthFailed, "registry 认证失败")
	}
	var payload struct {
		Token       string `json:"token"`
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return errs.Wrap(CodeAuthFailed, "registry 认证失败", err)
	}
	if payload.Token != "" {
		c.bearerToken = payload.Token
	} else {
		c.bearerToken = payload.AccessToken
	}
	if c.bearerToken == "" {
		return errs.New(CodeAuthFailed, "registry 认证失败")
	}
	return nil
}

func parseChallenge(challenge string) map[string]string {
	out := map[string]string{}
	body := strings.TrimPrefix(challenge, "Bearer ")
	for _, kv := range strings.Split(body, ",") {
		idx := strings.Index(kv, "=")
		if idx < 0 {
			continue
		}
		k := strings.TrimSpace(kv[:idx])
		v := strings.Trim(strings.TrimSpace(kv[idx+1:]), `"`)
		out[k] = v
	}
	return out
}

// GetManifest 拉取 manifest（tag 或 digest）。
func (c *Client) GetManifest(ctx context.Context, repo, reference string) (body []byte, digest, mediaType string, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		c.Address+"/v2/"+repo+"/manifests/"+reference, nil)
	if err != nil {
		return nil, "", "", errs.Wrap(errs.CodeInternal, "构造请求失败", err)
	}
	req.Header.Set("Accept", acceptManifests)
	resp, err := c.do(ctx, req)
	if err != nil {
		return nil, "", "", err
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusNotFound:
		return nil, "", "", errs.New(errs.CodeNotFound, "镜像不存在")
	case http.StatusUnauthorized, http.StatusForbidden:
		return nil, "", "", errs.New(CodeAuthFailed, "registry 认证失败")
	default:
		return nil, "", "", errs.Wrap(CodeNotRegistry, "拉取 manifest 失败",
			fmt.Errorf("http %d", resp.StatusCode))
	}
	body, err = io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return nil, "", "", errs.Wrap(CodeTransfer, "传输中断", err)
	}
	return body, resp.Header.Get("Docker-Content-Digest"), resp.Header.Get("Content-Type"), nil
}

// HeadManifest 判断 manifest 是否存在并返回其 digest（幂等检查用）。
func (c *Client) HeadManifest(ctx context.Context, repo, digest string) (bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodHead,
		c.Address+"/v2/"+repo+"/manifests/"+digest, nil)
	if err != nil {
		return false, errs.Wrap(errs.CodeInternal, "构造请求失败", err)
	}
	req.Header.Set("Accept", acceptManifests)
	resp, err := c.do(ctx, req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		return true, nil
	}
	if resp.StatusCode == http.StatusNotFound {
		return false, nil
	}
	return false, errs.Wrap(CodeRelayPush, "检查 manifest 失败", fmt.Errorf("http %d", resp.StatusCode))
}

// BlobExists 判断 blob 是否存在（幂等检查 / M2 推送跳过已存在层）。
func (c *Client) BlobExists(ctx context.Context, repo, digest string) (bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodHead,
		c.Address+"/v2/"+repo+"/blobs/"+digest, nil)
	if err != nil {
		return false, errs.Wrap(errs.CodeInternal, "构造请求失败", err)
	}
	resp, err := c.do(ctx, req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		return true, nil
	}
	if resp.StatusCode == http.StatusNotFound {
		return false, nil
	}
	return false, errs.Wrap(CodeRelayPush, "检查 blob 失败", fmt.Errorf("http %d", resp.StatusCode))
}

// OpenBlob 打开 blob 流；rangeStart > 0 时发起 Range 断点请求。
func (c *Client) OpenBlob(ctx context.Context, repo, digest string, rangeStart int64) (io.ReadCloser, int64, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		c.Address+"/v2/"+repo+"/blobs/"+digest, nil)
	if err != nil {
		return nil, 0, errs.Wrap(errs.CodeInternal, "构造请求失败", err)
	}
	if rangeStart > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", rangeStart))
	}
	resp, err := c.do(ctx, req)
	if err != nil {
		return nil, 0, err
	}
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusPartialContent {
		resp.Body.Close()
		if resp.StatusCode == http.StatusNotFound {
			return nil, 0, errs.New(errs.CodeNotFound, "镜像层不存在")
		}
		return nil, 0, errs.Wrap(CodeTransfer, "拉取镜像层失败", fmt.Errorf("http %d", resp.StatusCode))
	}
	size := resp.ContentLength
	if size < 0 {
		size = 0
	}
	return resp.Body, size, nil
}

// InitiateUpload 发起 blob 上传会话，返回上传位置。
func (c *Client) InitiateUpload(ctx context.Context, repo string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.Address+"/v2/"+repo+"/blobs/uploads/", nil)
	if err != nil {
		return "", errs.Wrap(errs.CodeInternal, "构造请求失败", err)
	}
	resp, err := c.do(ctx, req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		return "", errs.Wrap(CodeRelayPush, "发起上传失败", fmt.Errorf("http %d", resp.StatusCode))
	}
	location := resp.Header.Get("Location")
	if location == "" {
		return "", errs.New(CodeRelayPush, "推送中转 registry 失败")
	}
	if !strings.Contains(location, "://") {
		location = c.Address + location
	}
	return location, nil
}

// PutBlob 流式上传 blob 到指定会话位置（monolithic PUT）。
func (c *Client) PutBlob(ctx context.Context, location, digest string, contentLength int64, r io.Reader) error {
	sep := "?"
	if strings.Contains(location, "?") {
		sep = "&"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut,
		location+sep+"digest="+digest, r)
	if err != nil {
		return errs.Wrap(errs.CodeInternal, "构造请求失败", err)
	}
	req.ContentLength = contentLength
	req.Header.Set("Content-Type", "application/octet-stream")
	resp, err := c.do(ctx, req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		return errs.Wrap(CodeRelayPush, "推送镜像层失败", fmt.Errorf("http %d", resp.StatusCode))
	}
	return nil
}

// PutManifest 推送 manifest（tag 或 digest 引用）。
func (c *Client) PutManifest(ctx context.Context, repo, reference, mediaType string, body []byte) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPut,
		c.Address+"/v2/"+repo+"/manifests/"+reference, strings.NewReader(string(body)))
	if err != nil {
		return errs.Wrap(errs.CodeInternal, "构造请求失败", err)
	}
	req.Header.Set("Content-Type", mediaType)
	resp, err := c.do(ctx, req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		return errs.Wrap(CodeRelayPush, "推送 manifest 失败", fmt.Errorf("http %d", resp.StatusCode))
	}
	return nil
}

// ParseImageManifest 解析单镜像 manifest。
func ParseImageManifest(body []byte) (*ImageManifest, error) {
	var m ImageManifest
	if err := json.Unmarshal(body, &m); err != nil {
		return nil, errs.Wrap(errs.CodeInternal, "manifest 解析失败", err)
	}
	if len(m.Layers) == 0 || m.Config.Digest == "" {
		return nil, errs.New(errs.CodeInternal, "manifest 内容不完整")
	}
	return &m, nil
}

// ParseIndex 解析多架构索引。
func ParseIndex(body []byte) (*IndexList, error) {
	var idx IndexList
	if err := json.Unmarshal(body, &idx); err != nil {
		return nil, errs.Wrap(errs.CodeInternal, "index 解析失败", err)
	}
	if len(idx.Manifests) == 0 {
		return nil, errs.New(errs.CodeNotFound, "manifest list 为空")
	}
	return &idx, nil
}

// SelectPlatform 在索引中选择目标平台的 manifest digest（os/arch 匹配，缺省 linux/amd64；
// amd64 匹配 variant 可选；找不到匹配时回退第一个 linux 项，再回退首项）。
func SelectPlatform(idx *IndexList, goos, goarch string) string {
	var firstLinux string
	for _, m := range idx.Manifests {
		if m.Platform == nil {
			continue
		}
		if m.Platform.OS == goos && m.Platform.Architecture == goarch {
			if goarch == "arm64" || m.Platform.Variant == "" || goarch != "arm" {
				return m.Digest
			}
		}
		if m.Platform.OS == goos && firstLinux == "" {
			firstLinux = m.Digest
		}
	}
	if firstLinux != "" {
		return firstLinux
	}
	return idx.Manifests[0].Digest
}
