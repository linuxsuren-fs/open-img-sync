package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/linuxsuren/open-img-sync/internal/registry"
)

func createRegistryFixture(t *testing.T, s *Server, body map[string]any) map[string]any {
	t.Helper()
	rec := doJSON(t, s, http.MethodPost, "/api/registries", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("create registry = %d body=%s", rec.Code, rec.Body.String())
	}
	var created map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	return created
}

// TestRegistryCRUDAndMask 覆盖：三种类型 CRUD + 地址规范化 + 凭据掩码。
func TestRegistryCRUDAndMask(t *testing.T) {
	s := newTestServer(t, nil)

	created := createRegistryFixture(t, s, map[string]any{
		"name": "docker-hub-mirror", "address": "registry.example.com:5443/",
		"type": "standard", "role": "source", "username": "ci", "secret": "tok-123",
	})
	if created["address"] != "https://registry.example.com:5443" {
		t.Fatalf("地址未规范化: %v", created["address"])
	}
	if created["secret_mask"] != "reg-****" {
		t.Fatalf("secret_mask = %v", created["secret_mask"])
	}
	if strings.Contains(recStr(created), "tok-123") {
		t.Fatal("创建响应泄露明文")
	}

	// anonymous
	anon := createRegistryFixture(t, s, map[string]any{
		"name": "public", "address": "https://docker.io", "type": "anonymous",
	})
	if anon["role"] != "source" {
		t.Fatalf("缺省 role 应为 source: %v", anon["role"])
	}
	if anon["secret_mask"] != "" {
		t.Fatalf("anonymous 不应有掩码: %v", anon["secret_mask"])
	}

	// aliyun-temp：AK/SK 掩码
	aliyun := createRegistryFixture(t, s, map[string]any{
		"name": "aliyun-ee", "address": "https://xxx-registry.cn-hangzhou.cr.aliyuncs.com",
		"type": "aliyun-temp", "aliyun": map[string]any{
			"region": "cn-hangzhou", "instance": "xxx-registry",
			"access_key_id": "LTAI5tAKID", "access_key_secret": "sk-plain-123",
		},
	})
	aliyunCfg, _ := aliyun["aliyun"].(map[string]any)
	if aliyunCfg["ak_mask"] != "ak-****" {
		t.Fatalf("ak_mask = %v", aliyunCfg["ak_mask"])
	}
	if strings.Contains(recStr(aliyun), "sk-plain-123") || strings.Contains(recStr(aliyun), "LTAI5tAKID") {
		t.Fatal("aliyun 凭据泄露明文")
	}

	// role 过滤
	list := doJSON(t, s, http.MethodGet, "/api/registries?role=source", nil)
	var env struct {
		Total int `json:"total"`
	}
	_ = json.Unmarshal(list.Body.Bytes(), &env)
	if env.Total != 3 {
		t.Fatalf("role=source total = %d", env.Total)
	}

	// 更新留空不变更
	id, _ := created["id"].(string)
	upd := doJSON(t, s, http.MethodPut, "/api/registries/"+id, map[string]any{
		"name": "docker-hub-mirror", "address": "registry.example.com:5443",
		"type": "standard", "username": "ci", "secret": "",
	})
	if upd.Code != http.StatusOK {
		t.Fatalf("update = %d %s", upd.Code, upd.Body.String())
	}
	var u map[string]any
	_ = json.Unmarshal(upd.Body.Bytes(), &u)
	if u["secret_mask"] != "reg-****" {
		t.Fatal("留空 secret 不应清除凭据")
	}

	// 删除
	del := doJSON(t, s, http.MethodDelete, "/api/registries/"+id, nil)
	if del.Code != http.StatusOK {
		t.Fatalf("delete = %d", del.Code)
	}
}

func TestRegistryValidation(t *testing.T) {
	s := newTestServer(t, nil)
	cases := []map[string]any{
		{"name": "", "address": "a.com", "type": "anonymous"},
		{"name": "n", "address": "", "type": "anonymous"},
		{"name": "n", "address": "a.com", "type": "weird"},
		{"name": "n", "address": "a.com", "type": "standard", "username": ""}, // standard 无用户名
		{"name": "n", "address": "a.com", "type": "standard", "username": "u", "secret": ""},
		{"name": "n", "address": "a.com", "type": "aliyun-temp", "aliyun": map[string]any{}},
		{"name": "n", "address": "a.com", "type": "anonymous", "role": "bad"},
	}
	for i, body := range cases {
		rec := doJSON(t, s, http.MethodPost, "/api/registries", body)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("case %d 应 400, got %d body=%s", i, rec.Code, rec.Body.String())
		}
	}
}

// TestRegistryTestPingClassified 连通性测试三类分类（真实 HTTP 往返）。
func TestRegistryTestPingClassified(t *testing.T) {
	s := newTestServer(t, nil)

	// 200：可达
	okSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer okSrv.Close()
	// 401：认证失败
	authSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer authSrv.Close()
	// 404：非 registry
	notRegSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer notRegSrv.Close()

	for _, tc := range []struct {
		addr     string
		wantCode int
	}{
		{okSrv.URL, 0},
		{authSrv.URL, registry.CodeAuthFailed},
		{notRegSrv.URL, registry.CodeNotRegistry},
		{"http://127.0.0.1:1", registry.CodeUnreachable}, // 端口 1 几乎必然拒绝
	} {
		reg := createRegistryFixture(t, s, map[string]any{
			"name":    "t-" + strings.ReplaceAll(strings.TrimPrefix(tc.addr, "http://"), ".", "-"),
			"address": tc.addr, "type": "anonymous",
		})
		id, _ := reg["id"].(string)
		rec := doJSON(t, s, http.MethodPost, "/api/registries/"+id+"/test", nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("test %s = %d", tc.addr, rec.Code)
		}
		var res registry.PingResult
		_ = json.Unmarshal(rec.Body.Bytes(), &res)
		if res.Code != tc.wantCode {
			t.Fatalf("test %s code = %d, want %d", tc.addr, res.Code, tc.wantCode)
		}
	}
}

// TestImageBatchAndList 覆盖验收：批量录入给出非法行的明确提示（第几行、原因）。
func TestImageBatchAndList(t *testing.T) {
	s := newTestServer(t, nil)
	reg := createRegistryFixture(t, s, map[string]any{
		"name": "src", "address": "https://registry.example.com", "type": "anonymous",
	})
	id, _ := reg["id"].(string)

	refs := "nginx\nlibrary/nginx:1.27\nAPP:latest\nbad@sha256:short\n\nreg.io:5000/team/app:v1\n"
	rec := doJSON(t, s, http.MethodPost, "/api/registries/"+id+"/images/batch", map[string]any{"refs": refs})
	if rec.Code != http.StatusOK {
		t.Fatalf("batch = %d", rec.Code)
	}
	var batch struct {
		Data []struct {
			Line   int    `json:"line"`
			OK     bool   `json:"ok"`
			Ref    string `json:"ref"`
			Reason string `json:"reason"`
		} `json:"data"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &batch)
	if len(batch.Data) != 5 { // 空行跳过
		t.Fatalf("batch 结果数 = %d: %+v", len(batch.Data), batch.Data)
	}
	byLine := map[int]int{}
	for _, item := range batch.Data {
		if item.OK {
			byLine[item.Line] = 1
		} else if item.Reason == "" {
			t.Fatalf("第 %d 行失败但无原因", item.Line)
		}
	}
	// 第 1、2、6 行合法；第 3 行大写、第 4 行 digest 非法
	for _, want := range []int{1, 2, 6} {
		if byLine[want] != 1 {
			t.Fatalf("第 %d 行应合法: %+v", want, batch.Data)
		}
	}
	for _, bad := range []int{3, 4} {
		if byLine[bad] != 0 {
			t.Fatalf("第 %d 行应非法: %+v", bad, batch.Data)
		}
	}

	// 清单查询 + 分页
	list := doJSON(t, s, http.MethodGet, "/api/registries/"+id+"/images?page=1&page_size=2", nil)
	var env struct {
		Data  []map[string]any `json:"data"`
		Total int              `json:"total"`
	}
	_ = json.Unmarshal(list.Body.Bytes(), &env)
	if env.Total != 3 || len(env.Data) != 2 {
		t.Fatalf("list = %+v", env)
	}
	if env.Data[0]["ref"] != "nginx:latest" {
		t.Fatalf("ref 未规范化: %v", env.Data[0]["ref"])
	}

	// 删除 registry 级联删除清单
	if del := doJSON(t, s, http.MethodDelete, "/api/registries/"+id, nil); del.Code != http.StatusOK {
		t.Fatal("registry 删除失败")
	}
	list = doJSON(t, s, http.MethodGet, "/api/registries/"+id+"/images", nil)
	if list.Code != http.StatusNotFound {
		t.Fatalf("registry 删除后应 404, got %d", list.Code)
	}
}
