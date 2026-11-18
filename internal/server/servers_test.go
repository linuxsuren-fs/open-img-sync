package server

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/linuxsuren/open-img-sync/internal/crypt"
	"github.com/linuxsuren/open-img-sync/internal/sshx"
	"github.com/linuxsuren/open-img-sync/internal/store"
)

// newTestServer 构造带内存依赖的测试服务；sshTester 可注入假实现。
func newTestServer(t *testing.T, tester func(ctx context.Context, opt sshx.Options) sshx.TestResult) *Server {
	t.Helper()
	key, _, err := crypt.LoadMasterKey(crypt.MasterKeyConfig{EnvVar: "OIS_TEST_KEY_UNUSED", FilePath: filepath.Join(t.TempDir(), "master.key")})
	if err != nil {
		t.Fatal(err)
	}
	return newTestServerKey(t, tester, crypt.NewBox(key))
}

// newTestServerKey 用指定主密钥构造测试服务（导入导出往返需两侧同钥）。
func newTestServerKey(t *testing.T, tester func(ctx context.Context, opt sshx.Options) sshx.TestResult, box *crypt.Box) *Server {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	s := New(Options{Version: "test"}, Deps{DB: db, Secrets: store.NewSecretBox(box)})
	if tester != nil {
		s.sshTester = tester
	}
	return s
}

func doJSON(t *testing.T, s *Server, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var reader *bytes.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		reader = bytes.NewReader(raw)
	} else {
		reader = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, reader)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	return rec
}

const testPassword = "Sup3r-Secret-pw"

func createServerFixture(t *testing.T, s *Server) map[string]any {
	t.Helper()
	rec := doJSON(t, s, http.MethodPost, "/api/servers", map[string]any{
		"name": "edge-01", "host": "192.168.1.10", "port": 22,
		"auth_type": "password", "username": "root", "secret": testPassword,
		"tags": []string{"prod", "edge"},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("create status = %d body = %s", rec.Code, rec.Body.String())
	}
	var created map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	return created
}

// TestServerCRUDAndMask 覆盖：CRUD 全流程 + 列表/详情绝不回显明文凭据。
func TestServerCRUDAndMask(t *testing.T) {
	s := newTestServer(t, nil)
	created := createServerFixture(t, s)

	if created["secret_mask"] != "ssh-****" {
		t.Fatalf("secret_mask = %v", created["secret_mask"])
	}
	fp, _ := created["secret_fingerprint"].(string)
	if !strings.HasPrefix(fp, "sha256:") || len(fp) != len("sha256:")+8 {
		t.Fatalf("fingerprint = %q", fp)
	}
	if strings.Contains(recStr(created), testPassword) {
		t.Fatal("创建响应泄露明文密码")
	}
	id, _ := created["id"].(string)
	if id == "" {
		t.Fatal("应生成 id")
	}

	// 详情不回显明文
	detail := doJSON(t, s, http.MethodGet, "/api/servers/"+id, nil)
	if detail.Code != http.StatusOK || strings.Contains(detail.Body.String(), testPassword) {
		t.Fatalf("详情响应泄露明文: %s", detail.Body.String())
	}

	// 更新：secret 留空 = 不变更
	updated := doJSON(t, s, http.MethodPut, "/api/servers/"+id, map[string]any{
		"name": "edge-01", "host": "192.168.1.11", "port": 2222,
		"auth_type": "password", "username": "ubuntu", "secret": "",
		"tags": []string{"prod"},
	})
	if updated.Code != http.StatusOK {
		t.Fatalf("update = %d %s", updated.Code, updated.Body.String())
	}
	var up map[string]any
	_ = json.Unmarshal(updated.Body.Bytes(), &up)
	if up["host"] != "192.168.1.11" || up["port"].(float64) != 2222 {
		t.Fatalf("更新未生效: %v", up)
	}
	if up["secret_mask"] != "ssh-****" {
		t.Fatalf("留空 secret 不应清除凭据, mask = %v", up["secret_mask"])
	}

	// 删除
	del := doJSON(t, s, http.MethodDelete, "/api/servers/"+id, nil)
	if del.Code != http.StatusOK {
		t.Fatalf("delete = %d", del.Code)
	}
	missing := doJSON(t, s, http.MethodGet, "/api/servers/"+id, nil)
	if missing.Code != http.StatusNotFound {
		t.Fatalf("删除后应 404, got %d", missing.Code)
	}
}

func TestServerValidation(t *testing.T) {
	s := newTestServer(t, nil)
	cases := []map[string]any{
		{"name": "", "host": "h", "auth_type": "password", "username": "u", "secret": "x"},
		{"name": "n", "host": "", "auth_type": "password", "username": "u", "secret": "x"},
		{"name": "n", "host": "h", "auth_type": "bad", "username": "u", "secret": "x"},
		{"name": "n", "host": "h", "auth_type": "password", "username": "", "secret": "x"},
		{"name": "n", "host": "h", "auth_type": "password", "username": "u", "secret": ""},
	}
	for i, body := range cases {
		rec := doJSON(t, s, http.MethodPost, "/api/servers", body)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("case %d 应 400, got %d body=%s", i, rec.Code, rec.Body.String())
		}
		var e map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &e)
		if code, _ := e["code"].(float64); int(code) != 5002 {
			t.Fatalf("case %d code = %v, want 5002", i, e["code"])
		}
	}
}

func TestServerListFilterAndPaging(t *testing.T) {
	s := newTestServer(t, nil)
	for i, spec := range []struct{ name, tag string }{
		{"edge-a", "prod"}, {"edge-b", "dev"}, {"db-a", "prod"},
	} {
		rec := doJSON(t, s, http.MethodPost, "/api/servers", map[string]any{
			"name": spec.name, "host": "10.0.0.1", "auth_type": "password",
			"username": "root", "secret": "pw", "tags": []string{spec.tag},
		})
		if rec.Code != http.StatusOK {
			t.Fatalf("create %d: %d", i, rec.Code)
		}
		time.Sleep(2 * time.Millisecond) // 保证 created_at 排序稳定
	}
	// 名称模糊
	list := doJSON(t, s, http.MethodGet, "/api/servers?name=edge", nil)
	var env struct {
		Data     []map[string]any `json:"data"`
		Total    int              `json:"total"`
		Page     int              `json:"page"`
		PageSize int              `json:"page_size"`
	}
	_ = json.Unmarshal(list.Body.Bytes(), &env)
	if env.Total != 2 {
		t.Fatalf("name 过滤 total = %d, want 2", env.Total)
	}
	// 标签
	list = doJSON(t, s, http.MethodGet, "/api/servers?tag=prod", nil)
	_ = json.Unmarshal(list.Body.Bytes(), &env)
	if env.Total != 2 {
		t.Fatalf("tag 过滤 total = %d, want 2", env.Total)
	}
	// 分页
	list = doJSON(t, s, http.MethodGet, "/api/servers?page=2&page_size=2", nil)
	_ = json.Unmarshal(list.Body.Bytes(), &env)
	if len(env.Data) != 1 || env.Total != 3 || env.Page != 2 {
		t.Fatalf("分页异常: %+v", env)
	}
}

// TestServerExportImportRoundtrip 覆盖验收：导入导出往返还原非凭据字段（且凭据不落明文）。
func TestServerExportImportRoundtrip(t *testing.T) {
	// 导出方与导入方共享同一主密钥（同一部署或迁移场景）
	sharedKey, _, err := crypt.LoadMasterKey(crypt.MasterKeyConfig{EnvVar: "OIS_TEST_KEY_UNUSED", FilePath: filepath.Join(t.TempDir(), "master.key")})
	if err != nil {
		t.Fatal(err)
	}
	box := crypt.NewBox(sharedKey)
	s := newTestServerKey(t, nil, box)
	createServerFixture(t, s)

	exported := doJSON(t, s, http.MethodGet, "/api/servers/export", nil)
	if strings.Contains(exported.Body.String(), testPassword) {
		t.Fatal("导出内容泄露明文密码")
	}

	// 导入到全新 DB
	s2 := newTestServerKey(t, nil, box)
	imp := doJSON(t, s2, http.MethodPost, "/api/servers/import", json.RawMessage(exported.Body.String()))
	if imp.Code != http.StatusOK {
		t.Fatalf("import = %d %s", imp.Code, imp.Body.String())
	}
	var impResult struct {
		Data []map[string]any `json:"data"`
	}
	_ = json.Unmarshal(imp.Body.Bytes(), &impResult)
	if len(impResult.Data) != 1 || impResult.Data[0]["ok"] != true {
		t.Fatalf("导入结果异常: %s", imp.Body.String())
	}

	list := doJSON(t, s2, http.MethodGet, "/api/servers", nil)
	var env struct {
		Data []map[string]any `json:"data"`
	}
	_ = json.Unmarshal(list.Body.Bytes(), &env)
	if len(env.Data) != 1 {
		t.Fatalf("导入后应有一条记录")
	}
	got := env.Data[0]
	if got["name"] != "edge-01" || got["host"] != "192.168.1.10" || got["secret_mask"] != "ssh-****" {
		t.Fatalf("往返后字段不一致: %v", got)
	}
	// 同一主密钥环境下凭据加密块可还原（fingerprint 一致）
	orig := createServerFixture(t, s)
	if got["secret_fingerprint"] != orig["secret_fingerprint"] {
		t.Fatalf("指纹不一致: %v vs %v", got["secret_fingerprint"], orig["secret_fingerprint"])
	}
}

// TestServerTestEndpointClassified 覆盖验收：连通性测试三类错误分类稳定。
func TestServerTestEndpointClassified(t *testing.T) {
	for _, tc := range []struct {
		name   string
		tester func(ctx context.Context, opt sshx.Options) sshx.TestResult
		want   int
	}{
		{"auth", func(_ context.Context, _ sshx.Options) sshx.TestResult {
			return sshx.TestResult{Code: sshx.CodeAuthFailed, Message: "SSH 认证失败"}
		}, 2002},
		{"timeout", func(_ context.Context, _ sshx.Options) sshx.TestResult {
			return sshx.TestResult{Code: sshx.CodeTimeout, Message: "连接超时"}
		}, 2004},
		{"docker", func(_ context.Context, _ sshx.Options) sshx.TestResult {
			return sshx.TestResult{Reachable: true, Code: sshx.CodeDockerMissing, Message: "目标机 Docker 不可用"}
		}, 2003},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newTestServer(t, tc.tester)
			fixture := createServerFixture(t, s)
			id, _ := fixture["id"].(string)
			rec := doJSON(t, s, http.MethodPost, "/api/servers/"+id+"/test", nil)
			if rec.Code != http.StatusOK {
				t.Fatalf("test endpoint = %d", rec.Code)
			}
			var res sshx.TestResult
			_ = json.Unmarshal(rec.Body.Bytes(), &res)
			if res.Code != tc.want {
				t.Fatalf("code = %d, want %d", res.Code, tc.want)
			}
		})
	}
}

// TestNoPlaintextInLogsAndResponses 锁定 #10 验收项：API 响应与日志两条出口均无明文凭据。
func TestNoPlaintextInLogsAndResponses(t *testing.T) {
	var logBuf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logBuf, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })

	s := newTestServer(t, func(_ context.Context, _ sshx.Options) sshx.TestResult {
		return sshx.TestResult{Code: sshx.CodeUnreachable, Message: "服务器不可达"}
	})
	fixture := createServerFixture(t, s)
	id, _ := fixture["id"].(string)

	list := doJSON(t, s, http.MethodGet, "/api/servers", nil)
	tested := doJSON(t, s, http.MethodPost, "/api/servers/"+id+"/test", nil)

	for name, output := range map[string]string{
		"list 响应": list.Body.String(),
		"test 响应": tested.Body.String(),
		"日志":      logBuf.String(),
	} {
		if strings.Contains(output, testPassword) {
			t.Fatalf("%s 出现明文密码:\n%s", name, output)
		}
	}
}

func recStr(m map[string]any) string {
	raw, _ := json.Marshal(m)
	return string(raw)
}
