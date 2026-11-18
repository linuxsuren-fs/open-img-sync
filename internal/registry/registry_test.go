package registry

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/linuxsuren/open-img-sync/internal/errs"
)

func TestNormalizeAddress(t *testing.T) {
	cases := map[string]string{
		"reg.example.com":              "https://reg.example.com",
		"reg.example.com/":             "https://reg.example.com",
		"reg.example.com///":           "https://reg.example.com",
		"https://reg.example.com":      "https://reg.example.com",
		"http://reg.example.com:5000/": "http://reg.example.com:5000",
		"  reg.example.com  ":          "https://reg.example.com",
	}
	for in, want := range cases {
		if got := NormalizeAddress(in); got != want {
			t.Errorf("NormalizeAddress(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestPingOK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Docker-Distribution-API-Version", "registry/2.0")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	res := Ping(srv.URL, "", "")
	if !res.OK || res.Code != 0 {
		t.Fatalf("res = %+v", res)
	}
}

func TestPingAuthFailed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("WWW-Authenticate", `Bearer realm="..."`)
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()
	// 匿名探测被拒
	if res := Ping(srv.URL, "", ""); res.OK || res.Code != CodeAuthFailed {
		t.Fatalf("anonymous res = %+v", res)
	}
	// 带错误凭据同样 401
	if res := Ping(srv.URL, "user", "bad"); res.OK || res.Code != CodeAuthFailed {
		t.Fatalf("bad creds res = %+v", res)
	}
	// 带正确凭据 200
	srv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok := r.BasicAuth()
		if !ok || user != "ci" || pass != "tok" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv2.Close()
	if res := Ping(srv2.URL, "ci", "tok"); !res.OK {
		t.Fatalf("good creds res = %+v", res)
	}
}

func TestPingNotRegistry(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound) // 普通 web 服务
	}))
	defer srv.Close()
	if res := Ping(srv.URL, "", ""); res.OK || res.Code != CodeNotRegistry {
		t.Fatalf("res = %+v", res)
	}
}

func TestPingUnreachable(t *testing.T) {
	// 已关闭的本机端口必然连接拒绝
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	url := srv.URL
	srv.Close()
	if res := Ping(url, "", ""); res.OK || res.Code != CodeUnreachable {
		t.Fatalf("res = %+v", res)
	}
}

func TestParseRef(t *testing.T) {
	ok := []struct{ in, normalized string }{
		{"nginx", "nginx:latest"},
		{"library/nginx:1.27", "library/nginx:1.27"},
		{"docker.io/library/nginx", "docker.io/library/nginx:latest"},
		{"reg.example.com:5000/team/app:v1.0", "reg.example.com:5000/team/app:v1.0"},
		{"app@sha256:" + hex64('a'), "app@sha256:" + hex64('a')},
		{"Reg.example.com/app:latest", "Reg.example.com/app:latest"},
		// digest 优先于 tag，与 docker 语义一致
		{"app:latest@sha256:" + hex64('b'), "app@sha256:" + hex64('b')},
	}
	for _, c := range ok {
		ref, err := ParseRef(c.in)
		if err != nil {
			t.Errorf("ParseRef(%q) 不应报错: %v", c.in, err)
			continue
		}
		if got := ref.String(); got != c.normalized {
			t.Errorf("ParseRef(%q).String() = %q, want %q", c.in, got, c.normalized)
		}
	}
	bad := []string{
		"",
		"   ",
		"APP:latest",           // 仓库必须小写
		"app:",                 // 空 tag
		"app:-bad",             // tag 首字符非法
		"app@sha256:short",     // digest 长度错误
		"app@md5:xxxx",         // 算法不支持
		"app@sha256:zzzz",      // 非 hex
		"reg:5000/app:bad:tag", // 多个冒号
		"-leading/app",         // 仓库首字符非法
	}
	for _, in := range bad {
		_, err := ParseRef(in)
		if err == nil {
			t.Errorf("ParseRef(%q) 应报错", in)
			continue
		}
		var e *errs.E
		if !errors.As(err, &e) || e.Code != CodeInvalidRef {
			t.Errorf("ParseRef(%q) 错误码异常: %v", in, err)
		}
	}
}

func hex64(c byte) string {
	b := make([]byte, 64)
	for i := range b {
		b[i] = c
	}
	return string(b)
}
