package aliyun

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"log/slog"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/linuxsuren/open-img-sync/internal/errs"
	"github.com/linuxsuren/open-img-sync/internal/model"
	"github.com/linuxsuren/open-img-sync/internal/store"
)

// RefreshMargin 到期前提前刷新的时间窗（提前 5 分钟）。
const RefreshMargin = 5 * time.Minute

func defaultNonce() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return base64.StdEncoding.EncodeToString(b)
}

func base64Decode(s string) ([]byte, error) { return base64.StdEncoding.DecodeString(s) }

// Provider 临时凭据提供者：缓存 + 到期前刷新 + 并发共享单次换取。
// 缓存仅存内存，进程内共享；key 为 registryID + AK 指纹（换 AK 自动失效）。
type Provider struct {
	Secrets *store.SecretBox
	// EndpointFor 覆盖区域端点（测试注入 fake 服务），为空用默认
	EndpointFor func(region string) string
	Now         func() time.Time

	mu    sync.Mutex
	cache map[string]Token
}

// NewProvider 构造提供者。
func NewProvider(secrets *store.SecretBox) *Provider {
	return &Provider{Secrets: secrets, cache: map[string]Token{}}
}

// DeriveRegion 从 ACR 实例域名推导地域：<实例>.<地域>.cr.aliyuncs.com → <地域>。
// 非该形式返回空串。
func DeriveRegion(address string) string {
	lower := strings.ToLower(address)
	m := regionFromHost.FindStringSubmatch(lower)
	if len(m) == 2 {
		return m[1]
	}
	return ""
}

var regionFromHost = regexp.MustCompile(`\.([a-z0-9]+(?:-[a-z0-9]+)+)\.cr\.aliyuncs\.com`)

// Fetch 返回该 registry 的有效临时凭据：缓存命中且未进入刷新窗口则直接复用，
// 否则换取新凭据（并发调用串行化，同一 key 只换取一次）。
func (p *Provider) Fetch(ctx context.Context, reg model.RegistryRecord) (Token, error) {
	ak, err := p.Secrets.Open(reg.Aliyun.AKSealed)
	if err != nil {
		return Token{}, errs.From(err)
	}
	sk, err := p.Secrets.Open(reg.Aliyun.SKSealed)
	if err != nil {
		return Token{}, errs.From(err)
	}
	region := strings.TrimSpace(reg.Aliyun.Region)
	if region == "" {
		region = DeriveRegion(reg.Address)
	}
	if region == "" {
		return Token{}, errs.New(CodeFetchFailed,
			"临时凭据获取失败：未配置地域，且无法从地址识别（地址应为 <实例>.<地域>.cr.aliyuncs.com 形式，或在配置中填写地域，如 cn-beijing）")
	}
	key := reg.ID + "/" + reg.Aliyun.AKFingerprint

	p.mu.Lock()
	defer p.mu.Unlock()
	now := p.Now
	if now == nil {
		now = time.Now
	}
	if cached, ok := p.cache[key]; ok && now().Add(RefreshMargin).Before(cached.ExpireTime) {
		return cached, nil // 缓存命中，不重复换取
	}

	client := &Client{AK: ak, SK: sk, Region: region, Instance: strings.TrimSpace(reg.Aliyun.Instance)}
	if p.EndpointFor != nil {
		client.Endpoint = p.EndpointFor(region)
	}
	tok, err := client.GetAuthorizationToken(ctx)
	if err != nil {
		// 日志只落 registry 与错误码，绝不落 AK/SK/临时密码
		slog.Warn("aliyun temp credential fetch failed", "registry_id", reg.ID, "err_code", errs.From(err).Code)
		return Token{}, err
	}
	p.cache[key] = tok
	slog.Info("aliyun temp credential fetched",
		"registry_id", reg.ID, "expire_at", tok.ExpireTime.Format(time.RFC3339))
	return tok, nil
}

// Invalidate 清除指定 registry 的缓存（凭据更新后调用）。
func (p *Provider) Invalidate(registryID string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for k := range p.cache {
		if len(k) > len(registryID) && k[:len(registryID)] == registryID {
			delete(p.cache, k)
		}
	}
}
