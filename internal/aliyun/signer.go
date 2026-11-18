// Package aliyun 实现阿里云 ACR 企业版临时凭据的自动获取与刷新（#4）。
//
// 实现方式：REST RPC 签名直调（HMAC-SHA1），不引官方 SDK——
// 官方 SDK 依赖体积大（整条 darabonba 依赖树），而本包只用到一个
// GetAuthorizationToken 接口；签名实现约 60 行标准库代码即可覆盖。
package aliyun

import (
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base64"
	"sort"
	"strings"
)

// percentEncode RFC 3986 百分号编码（Aliyun RPC 签名要求，空格编码为 %20）。
func percentEncode(s string) string {
	const hexDigits = "0123456789ABCDEF"
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9'),
			c == '-', c == '_', c == '.', c == '~':
			b.WriteByte(c)
		default:
			b.WriteByte('%')
			b.WriteByte(hexDigits[c>>4])
			b.WriteByte(hexDigits[c&0xf])
		}
	}
	return b.String()
}

// canonicalizedQuery 按 key 排序拼接 k=v（均 percentEncode）。
func canonicalizedQuery(params map[string]string) string {
	keys := make([]string, 0, len(params))
	for k := range params {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, percentEncode(k)+"="+percentEncode(params[k]))
	}
	return strings.Join(parts, "&")
}

// SignRPC 生成 Aliyun RPC V1 签名（POST 请求）。
// 返回去掉 Signature 的参数集合 + 计算出的签名值。
func SignRPC(ak, sk string, params map[string]string) (map[string]string, string) {
	canonical := canonicalizedQuery(params)
	stringToSign := "POST&" + percentEncode("/") + "&" + percentEncode(canonical)
	mac := hmac.New(sha1.New, []byte(sk+"&"))
	mac.Write([]byte(stringToSign))
	signature := base64.StdEncoding.EncodeToString(mac.Sum(nil))
	return params, signature
}
