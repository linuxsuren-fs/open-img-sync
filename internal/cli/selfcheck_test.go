package cli

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestSelfCheck 覆盖 #8 验收：self-check 子命令按 /healthz 状态给出退出语义。
func TestSelfCheck(t *testing.T) {
	ok := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer ok.Close()
	if err := selfCheck([]string{"-addr", ok.URL}); err != nil {
		t.Fatalf("健康服务应通过: %v", err)
	}

	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer bad.Close()
	if err := selfCheck([]string{"-addr", bad.URL}); err == nil {
		t.Fatal("500 应报错")
	}

	if err := selfCheck([]string{"-addr", "http://127.0.0.1:1"}); err == nil {
		t.Fatal("不可达应报错")
	}
}

// TestSelfCheckDefaultAddrFromEnv 缺省地址取 OIS_ADDR 并回环化。
func TestSelfCheckDefaultAddrFromEnv(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	t.Setenv("OIS_ADDR", srv.URL)
	if err := selfCheck(nil); err != nil {
		t.Fatalf("OIS_ADDR 服务应通过: %v", err)
	}
}
