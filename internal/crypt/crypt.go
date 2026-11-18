// Package crypt 提供主密钥装载与 AES-256-GCM 加解密（#10 安全基线）。
// 仅使用标准库 crypto/aes + cipher.NewGCM，不引第三方加密库。
package crypt

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// KeyLen 主密钥长度：AES-256 需要 32 字节。
const KeyLen = 32

// Box 持有主密钥，提供加解密。
type Box struct {
	key [KeyLen]byte
}

// ErrInvalidKey 表示主密钥不是合法的 base64(32 字节)。
var ErrInvalidKey = errors.New("主密钥必须是 base64 编码的 32 字节")

// NewBox 用已有密钥构造。
func NewBox(key [KeyLen]byte) *Box { return &Box{key: key} }

// MasterKeyConfig 主密钥装载配置。
type MasterKeyConfig struct {
	// EnvVar 环境变量名（如 OIS_MASTER_KEY），base64 的 32 字节，优先级最高
	EnvVar string
	// FilePath 密钥文件路径；不存在则自动生成并写入（0600）
	FilePath string
}

// LoadMasterKey 按优先级装载主密钥：环境变量 > 密钥文件（缺失时自动生成）。
// 返回密钥与实际来源（"env" / "file"），用于 /api/system/info 展示。
func LoadMasterKey(cfg MasterKeyConfig) (key [KeyLen]byte, source string, err error) {
	if cfg.EnvVar != "" {
		if raw := os.Getenv(cfg.EnvVar); raw != "" {
			k, e := decodeKey(raw)
			if e != nil {
				return key, "", fmt.Errorf("环境变量 %s: %w", cfg.EnvVar, e)
			}
			return k, "env", nil
		}
	}
	if cfg.FilePath == "" {
		return key, "", errors.New("主密钥文件路径未配置")
	}
	raw, readErr := os.ReadFile(cfg.FilePath)
	if readErr == nil {
		k, e := decodeKey(string(raw))
		if e != nil {
			return key, "", fmt.Errorf("密钥文件 %s: %w", cfg.FilePath, e)
		}
		return k, "file", nil
	}
	if !os.IsNotExist(readErr) {
		return key, "", fmt.Errorf("读取密钥文件 %s: %w", cfg.FilePath, readErr)
	}
	// 自动生成并落盘
	if _, e := io.ReadFull(rand.Reader, key[:]); e != nil {
		return key, "", fmt.Errorf("生成主密钥: %w", e)
	}
	encoded := base64.StdEncoding.EncodeToString(key[:])
	if e := writeKeyFile(cfg.FilePath, encoded); e != nil {
		return key, "", e
	}
	return key, "file", nil
}

func decodeKey(raw string) ([KeyLen]byte, error) {
	var key [KeyLen]byte
	decoded, err := base64.StdEncoding.DecodeString(trimSpace(raw))
	if err != nil {
		return key, ErrInvalidKey
	}
	if len(decoded) != KeyLen {
		return key, ErrInvalidKey
	}
	copy(key[:], decoded)
	return key, nil
}

func trimSpace(s string) string {
	start, end := 0, len(s)
	for start < end && (s[start] == ' ' || s[start] == '\n' || s[start] == '\r' || s[start] == '\t') {
		start++
	}
	for end > start && (s[end-1] == ' ' || s[end-1] == '\n' || s[end-1] == '\r' || s[end-1] == '\t') {
		end--
	}
	return s[start:end]
}

// writeKeyFile 以 0600 权限原子写入密钥文件（先写临时文件再 rename）。
func writeKeyFile(path, content string) error {
	if e := os.MkdirAll(filepath.Dir(path), 0o700); e != nil {
		return fmt.Errorf("创建密钥目录: %w", e)
	}
	tmp, e := os.CreateTemp(filepath.Dir(path), ".master-key-*")
	if e != nil {
		return fmt.Errorf("创建临时密钥文件: %w", e)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) //nolint:errcheck // rename 成功时临时文件已不存在
	if _, e = tmp.WriteString(content); e != nil {
		tmp.Close()
		return fmt.Errorf("写入临时密钥文件: %w", e)
	}
	if e = tmp.Close(); e != nil {
		return fmt.Errorf("关闭临时密钥文件: %w", e)
	}
	if e = os.Chmod(tmpName, 0o600); e != nil {
		return fmt.Errorf("设置密钥文件权限: %w", e)
	}
	if e = os.Rename(tmpName, path); e != nil {
		return fmt.Errorf("落盘密钥文件: %w", e)
	}
	return nil
}

// Seal 用 AES-256-GCM 加密，输出 base64(nonce + ciphertext)。
func (b *Box) Seal(plaintext string) (string, error) {
	if plaintext == "" {
		return "", nil
	}
	block, err := aes.NewCipher(b.key[:])
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err = io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	sealed := gcm.Seal(nonce, nonce, []byte(plaintext), nil)
	return base64.StdEncoding.EncodeToString(sealed), nil
}

// Open 解密 Seal 的输出；空串原样返回（表示未设置）。
func (b *Box) Open(sealed string) (string, error) {
	if sealed == "" {
		return "", nil
	}
	raw, err := base64.StdEncoding.DecodeString(sealed)
	if err != nil {
		return "", fmt.Errorf("密文不是合法 base64: %w", err)
	}
	block, err := aes.NewCipher(b.key[:])
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	if len(raw) < gcm.NonceSize() {
		return "", errors.New("密文长度不合法")
	}
	nonce, ciphertext := raw[:gcm.NonceSize()], raw[gcm.NonceSize():]
	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", errors.New("解密失败：密文损坏或主密钥不匹配")
	}
	return string(plaintext), nil
}
