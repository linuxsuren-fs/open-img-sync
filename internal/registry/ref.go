package registry

import (
	"regexp"
	"strings"

	"github.com/linuxsuren/open-img-sync/internal/errs"
)

// Ref 解析后的镜像引用。
type Ref struct {
	Registry   string // 可选 host[:port]
	Repository string // 含命名空间路径
	Tag        string // 缺省 latest（规范化时补全）
	Digest     string // repo@sha256:... 形式时非空
}

var (
	repoRe = regexp.MustCompile(`^[a-z0-9]+(?:(?:\.|_|__|-+)[a-z0-9]+)*(?:/[a-z0-9]+(?:(?:\.|_|__|-+)[a-z0-9]+)*)*$`)
	tagRe  = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9._-]{0,127}$`)
)

// ParseRef 解析并校验镜像引用，规范化规则：
//   - 无 tag 且无 digest 时补全 :latest
//   - repository 必须小写、分量以 [a-z0-9] 开头，分隔符 . _ - __
//   - digest 仅支持 sha256:<64 hex>
//
// 非法引用返回稳定错误码 3004。
func ParseRef(raw string) (Ref, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return Ref{}, errs.New(CodeInvalidRef, "镜像引用为空")
	}
	ref := Ref{}

	// digest 部分
	if idx := strings.Index(s, "@"); idx >= 0 {
		ref.Digest = s[idx+1:]
		s = s[:idx]
		if !isSha256Digest(ref.Digest) {
			return Ref{}, errs.New(CodeInvalidRef, "digest 必须是 sha256:<64 位十六进制>")
		}
	}

	// registry host 部分：首个路径段含 "." 或 ":" 或等于 "localhost" 且后面还有内容时视为 host
	if idx := strings.Index(s, "/"); idx > 0 {
		first := s[:idx]
		if strings.Contains(first, ".") || strings.Contains(first, ":") || first == "localhost" {
			ref.Registry = first
			s = s[idx+1:]
		}
	}

	// tag 部分：最后一个冒号必须在最后一个斜杠之后才是 tag（repo:tag@digest 时 tag 丢弃）
	if idx := strings.LastIndex(s, ":"); idx >= 0 && !strings.Contains(s[idx:], "/") && idx > strings.LastIndex(s, "/") {
		ref.Tag = s[idx+1:]
		s = s[:idx]
		if !tagRe.MatchString(ref.Tag) {
			return Ref{}, errs.New(CodeInvalidRef, "tag 含非法字符")
		}
	}
	if s == "" {
		return Ref{}, errs.New(CodeInvalidRef, "镜像名称为空")
	}
	if !repoRe.MatchString(s) {
		return Ref{}, errs.New(CodeInvalidRef, "镜像名称含非法字符（须小写）")
	}
	ref.Repository = s
	if ref.Tag == "" && ref.Digest == "" {
		ref.Tag = "latest"
	}
	return ref, nil
}

func isSha256Digest(d string) bool {
	if !strings.HasPrefix(d, "sha256:") {
		return false
	}
	hex := d[len("sha256:"):]
	if len(hex) != 64 {
		return false
	}
	for _, c := range hex {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

// String 输出规范化引用。
func (r Ref) String() string {
	var b strings.Builder
	if r.Registry != "" {
		b.WriteString(r.Registry)
		b.WriteString("/")
	}
	b.WriteString(r.Repository)
	if r.Digest != "" {
		b.WriteString("@")
		b.WriteString(r.Digest)
	} else if r.Tag != "" {
		b.WriteString(":")
		b.WriteString(r.Tag)
	}
	return b.String()
}
