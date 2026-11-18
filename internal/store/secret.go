package store

import (
	"github.com/linuxsuren/open-img-sync/internal/crypt"
	"github.com/linuxsuren/open-img-sync/internal/errs"
)

// SecretBox 把凭据字符串加密为可入库的密文字段。
// 空串加密结果仍是空串（表示“未设置”，与“留空不变更”语义配合）。
type SecretBox struct {
	box *crypt.Box
}

// NewSecretBox 构造。
func NewSecretBox(box *crypt.Box) *SecretBox { return &SecretBox{box: box} }

// Seal 加密；主密钥缺失或异常时返回稳定错误 5005。
func (s *SecretBox) Seal(plaintext string) (string, error) {
	if plaintext == "" {
		return "", nil
	}
	sealed, err := s.box.Seal(plaintext)
	if err != nil {
		return "", errs.Wrap(errs.CodeMasterKeyFail, "凭据加密失败", err)
	}
	return sealed, nil
}

// Open 解密；失败返回 5005（密文损坏或主密钥不匹配）。
func (s *SecretBox) Open(sealed string) (string, error) {
	if sealed == "" {
		return "", nil
	}
	plaintext, err := s.box.Open(sealed)
	if err != nil {
		return "", errs.Wrap(errs.CodeMasterKeyFail, "凭据解密失败", err)
	}
	return plaintext, nil
}
