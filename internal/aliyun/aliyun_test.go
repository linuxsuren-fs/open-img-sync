package aliyun

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/linuxsuren/open-img-sync/internal/crypt"
	"github.com/linuxsuren/open-img-sync/internal/errs"
	"github.com/linuxsuren/open-img-sync/internal/model"
	"github.com/linuxsuren/open-img-sync/internal/store"
)

// fakeACR 回放式 fake：按场景返回录制的 GetAuthorizationToken 响应，
// 并统计请求次数（CI 不依赖真实 AK，对应 issue 技术要点）。
type fakeACR struct {
	server   *httptest.Server
	calls    atomic.Int64
	scenario func(r *http.Request) (int, string)
}

func startFakeACR(t *testing.T, scenario func(r *http.Request) (int, string)) *fakeACR {
	t.Helper()
	f := &fakeACR{scenario: scenario}
	f.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.calls.Add(1)
		// 校验请求形态：POST + 表单 + ACS 签名头
		if r.Method != http.MethodPost {
			w.WriteHeader(400)
			return
		}
		if err := r.ParseForm(); err != nil {
			w.WriteHeader(400)
			return
		}
		if r.Form.Get("Action") != "GetAuthorizationToken" || r.Form.Get("Signature") == "" {
			w.WriteHeader(400)
			return
		}
		status, body := scenario(r)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(f.server.Close)
	return f
}

func okTokenBody(username, password string, expireEpoch int64) string {
	basic := base64.StdEncoding.EncodeToString([]byte(username + ":" + password))
	resp := map[string]any{
		"AuthorizationToken": map[string]any{
			"AuthorizationToken": basic,
			"TempUserName":       username,
			"ExpireTime":         expireEpoch,
		},
	}
	raw, _ := json.Marshal(resp)
	return string(raw)
}

func TestClientGetTokenSuccess(t *testing.T) {
	expire := time.Now().Add(time.Hour).Unix()
	f := startFakeACR(t, func(_ *http.Request) (int, string) {
		return 200, okTokenBody("cr_temp_user", "temp-pass-123", expire)
	})
	c := &Client{AK: "ak", SK: "sk", Region: "cn-hangzhou", Endpoint: f.server.URL}
	tok, err := c.GetAuthorizationToken(context.Background())
	if err != nil {
		t.Fatalf("GetAuthorizationToken: %v", err)
	}
	if tok.Username != "cr_temp_user" || tok.Password != "temp-pass-123" {
		t.Fatalf("token = %+v", tok)
	}
	if !tok.ExpireTime.Equal(time.Unix(expire, 0)) {
		t.Fatalf("expire = %v", tok.ExpireTime)
	}
}

func TestClientErrorClassification(t *testing.T) {
	cases := []struct {
		scenario func(_ *http.Request) (int, string)
		wantCode int
	}{
		{func(_ *http.Request) (int, string) {
			return 404, `{"RequestId":"x","Code":"InvalidAccessKeyId.NotFound","Message":"AK not found"}`
		}, CodeAKInvalid},
		{func(_ *http.Request) (int, string) {
			return 400, `{"RequestId":"x","Code":"SignatureDoesNotMatch","Message":"sig"}`
		}, CodeAKInvalid},
		{func(_ *http.Request) (int, string) {
			return 403, `{"RequestId":"x","Code":"Forbidden.RAM","Message":"no permission"}`
		}, CodeNoAuth},
		{func(_ *http.Request) (int, string) {
			return 400, `{"RequestId":"x","Code":"Throttling","Message":"too fast"}`
		}, CodeRateLimited},
		{func(_ *http.Request) (int, string) {
			return 500, `{"RequestId":"x","Code":"InternalError","Message":"boom"}`
		}, CodeFetchFailed},
		{func(_ *http.Request) (int, string) {
			return 200, `{"AuthorizationToken":{"AuthorizationToken":"not-base64!!","ExpireTime":1}}`
		}, CodeFetchFailed},
	}
	for i, tc := range cases {
		f := startFakeACR(t, tc.scenario)
		c := &Client{AK: "ak", SK: "sk", Region: "cn-hangzhou", Endpoint: f.server.URL}
		_, err := c.GetAuthorizationToken(context.Background())
		if err == nil {
			t.Fatalf("case %d 应报错", i)
		}
		var e *errs.E
		if !errors.As(err, &e) || e.Code != tc.wantCode {
			t.Fatalf("case %d code 异常（err=%v）", i, err)
		}
	}
}

func TestSignatureDeterministic(t *testing.T) {
	params := map[string]string{"Action": "GetAuthorizationToken", "AccessKeyId": "ak", "Format": "JSON"}
	_, sig1 := SignRPC("ak", "sk", params)
	_, sig2 := SignRPC("ak", "sk", params)
	if sig1 == "" || sig1 != sig2 {
		t.Fatalf("签名应非空且确定: %q vs %q", sig1, sig2)
	}
	// 换 SK 签名不同
	_, sig3 := SignRPC("ak", "other", params)
	if sig3 == sig1 {
		t.Fatal("不同 SK 应产生不同签名")
	}
	// percentEncode 空格为 %20，加号等特殊字符被编码
	if got := percentEncode("a b+c"); got != "a%20b%2Bc" {
		t.Fatalf("percentEncode = %q", got)
	}
}

func newTestProvider(t *testing.T, endpoint string, now func() time.Time) (*Provider, model.RegistryRecord) {
	t.Helper()
	key, _, err := crypt.LoadMasterKey(crypt.MasterKeyConfig{EnvVar: "OIS_TEST_KEY_UNUSED", FilePath: t.TempDir() + "/master.key"})
	if err != nil {
		t.Fatal(err)
	}
	secrets := store.NewSecretBox(crypt.NewBox(key))
	akSealed, err := secrets.Seal("LTAI5tFakeAK")
	if err != nil {
		t.Fatal(err)
	}
	skSealed, err := secrets.Seal("fake-sk")
	if err != nil {
		t.Fatal(err)
	}
	reg := model.RegistryRecord{
		ID: "reg-1", Type: model.RegistryTypeAliyunTemp,
		Aliyun: model.AliyunConfig{
			Region: "cn-hangzhou", Instance: "xxx-registry",
			AKSealed: akSealed, SKSealed: skSealed,
			AKFingerprint: crypt.Fingerprint("LTAI5tFakeAK"),
		},
	}
	p := NewProvider(secrets)
	p.EndpointFor = func(string) string { return endpoint }
	p.Now = now
	return p, reg
}

// TestProviderCacheHitNoRefetch 覆盖验收：缓存命中不再重复换取。
func TestProviderCacheHitNoRefetch(t *testing.T) {
	expire := time.Now().Add(time.Hour)
	f := startFakeACR(t, func(_ *http.Request) (int, string) {
		return 200, okTokenBody("u", "p", expire.Unix())
	})
	p, reg := newTestProvider(t, f.server.URL, nil)

	ctx := context.Background()
	for i := 0; i < 3; i++ {
		tok, err := p.Fetch(ctx, reg)
		if err != nil {
			t.Fatalf("Fetch #%d: %v", i, err)
		}
		if tok.Username != "u" {
			t.Fatalf("tok = %+v", tok)
		}
	}
	if got := f.calls.Load(); got != 1 {
		t.Fatalf("应只换取 1 次, got %d", got)
	}
}

// TestProviderRefreshBeforeExpiry 覆盖验收：到期前（提前 5 分钟）自动刷新，用 fake 时钟。
func TestProviderRefreshBeforeExpiry(t *testing.T) {
	base := time.Now()
	expire := base.Add(10 * time.Minute)
	f := startFakeACR(t, func(_ *http.Request) (int, string) {
		return 200, okTokenBody("u", "p", expire.Unix())
	})
	current := base
	p, reg := newTestProvider(t, f.server.URL, func() time.Time { return current })

	if _, err := p.Fetch(context.Background(), reg); err != nil {
		t.Fatal(err)
	}
	// 前进 5 分 01 秒：进入刷新窗口（expire-5m 已过），应重新换取
	current = base.Add(5*time.Minute + time.Second)
	if _, err := p.Fetch(context.Background(), reg); err != nil {
		t.Fatal(err)
	}
	if got := f.calls.Load(); got != 2 {
		t.Fatalf("进入刷新窗口应重新换取, got %d 次", got)
	}
	// 只前进 4 分钟：仍在窗口外，缓存复用
	current = base.Add(4 * time.Minute)
	if _, err := p.Fetch(context.Background(), reg); err != nil {
		t.Fatal(err)
	}
	if got := f.calls.Load(); got != 2 {
		t.Fatalf("窗口外不应重复换取, got %d 次", got)
	}
}

// TestProviderConcurrentSingleFetch 覆盖验收：并发任务共享同一份缓存，不重复换取。
func TestProviderConcurrentSingleFetch(t *testing.T) {
	expire := time.Now().Add(time.Hour)
	f := startFakeACR(t, func(_ *http.Request) (int, string) {
		time.Sleep(50 * time.Millisecond) // 模拟网络延迟放大竞争窗口
		return 200, okTokenBody("u", "p", expire.Unix())
	})
	p, reg := newTestProvider(t, f.server.URL, nil)
	p.EndpointFor = func(string) string { return f.server.URL }

	const n = 8
	errCh := make(chan error, n)
	for i := 0; i < n; i++ {
		go func() {
			_, err := p.Fetch(context.Background(), reg)
			errCh <- err
		}()
	}
	for i := 0; i < n; i++ {
		if err := <-errCh; err != nil {
			t.Fatalf("并发 Fetch: %v", err)
		}
	}
	if got := f.calls.Load(); got != 1 {
		t.Fatalf("并发应只换取 1 次, got %d", got)
	}
}

func TestProviderNoSecretInError(t *testing.T) {
	f := startFakeACR(t, func(_ *http.Request) (int, string) {
		return 403, `{"RequestId":"x","Code":"Forbidden.RAM","Message":"denied"}`
	})
	p, reg := newTestProvider(t, f.server.URL, nil)
	_, err := p.Fetch(context.Background(), reg)
	if err == nil {
		t.Fatal("应报错")
	}
	if msg := err.Error(); strings.Contains(msg, "fake-sk") || strings.Contains(msg, "LTAI5tFakeAK") {
		t.Fatalf("错误信息泄露凭据: %s", msg)
	}
	var e *errs.E
	if !errors.As(err, &e) || e.Code != CodeNoAuth {
		t.Fatalf("code 异常: %v", err)
	}
}
