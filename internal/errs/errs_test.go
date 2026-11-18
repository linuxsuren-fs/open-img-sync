package errs

import (
	"errors"
	"net/http"
	"testing"
)

func TestHTTPStatusMapping(t *testing.T) {
	cases := []struct {
		code   int
		status int
	}{
		{CodeUnauthorized, http.StatusUnauthorized},
		{CodeNotFound, http.StatusNotFound},
		{CodeBadRequest, http.StatusBadRequest},
		{CodeInternal, http.StatusInternalServerError},
		{2001, http.StatusOK}, // 业务判定类：连通性失败等，200 + code 表达
	}
	for _, c := range cases {
		e := New(c.code, "x")
		if got := e.HTTPStatus(); got != c.status {
			t.Errorf("code %d HTTPStatus = %d, want %d", c.code, got, c.status)
		}
	}
}

func TestWrapKeepsCause(t *testing.T) {
	root := errors.New("dial tcp 10.0.0.1:22: connection refused")
	e := Wrap(2001, "服务器不可达", root)
	if !errors.Is(e, root) {
		t.Fatal("errors.Is 应穿透到根因")
	}
	// Error() 会带根因（面向日志）；API 层只输出 Code/Message 字段
	if e.Cause() != root {
		t.Fatal("Cause 应为原始根因")
	}
}

func TestFromWrapsUnknown(t *testing.T) {
	raw := errors.New("boom")
	e := From(raw)
	if e.Code != CodeInternal {
		t.Fatalf("未知错误应映射为 5001, got %d", e.Code)
	}
	if From(nil) != nil {
		t.Fatal("nil 应返回 nil")
	}
	var target *E
	if !errors.As(From(raw), &target) || target.Code != CodeInternal {
		t.Fatal("From 结果应可用 errors.As 提取")
	}
}
