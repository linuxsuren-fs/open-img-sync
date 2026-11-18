package crypt

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
)

// Mask 生成凭据掩码：固定前缀 + 星号，不泄露长度信息。
// 例：Mask("ssh", "...") => "ssh-****"。
func Mask(prefix, secret string) string {
	if secret == "" {
		return ""
	}
	return prefix + "-****"
}

// Fingerprint 生成凭据指纹：sha256 前 8 个十六进制字符。
// 用于 UI 上确认“是哪一个凭据”而不暴露内容。
func Fingerprint(secret string) string {
	if secret == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(secret))
	return fmt.Sprintf("sha256:%s", hex.EncodeToString(sum[:4]))
}
