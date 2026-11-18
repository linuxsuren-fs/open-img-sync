// Package model 定义持久化资源模型。JSON 标签即对外契约（snake_case），
// 见 docs/api-contract.md；改动需走 PR 并说明兼容性。
package model

import (
	"crypto/rand"
	"encoding/hex"
	"time"
)

// 认证方式枚举（对外契约，不得改名）。
const (
	AuthPassword   = "password"
	AuthPrivateKey = "private_key"
)

// ServerRecord 是服务器的持久化形态：凭据以密文存储，绝不含明文。
type ServerRecord struct {
	ID                string    `json:"id"`
	Name              string    `json:"name"`
	Host              string    `json:"host"`
	Port              int       `json:"port"`
	AuthType          string    `json:"auth_type"`
	Username          string    `json:"username"`
	SecretSealed      string    `json:"secret_sealed"`      // AES-256-GCM 密文（base64）
	SecretFingerprint string    `json:"secret_fingerprint"` // 指纹（sha256 前 8 hex），可持久化
	Tags              []string  `json:"tags"`
	CreatedAt         time.Time `json:"created_at"`
	UpdatedAt         time.Time `json:"updated_at"`
}

// ServerView 是服务器的 API 回显形态：凭据只含掩码与指纹。
type ServerView struct {
	ID                string    `json:"id"`
	Name              string    `json:"name"`
	Host              string    `json:"host"`
	Port              int       `json:"port"`
	AuthType          string    `json:"auth_type"`
	Username          string    `json:"username"`
	SecretMask        string    `json:"secret_mask"`
	SecretFingerprint string    `json:"secret_fingerprint"`
	Tags              []string  `json:"tags"`
	CreatedAt         time.Time `json:"created_at"`
	UpdatedAt         time.Time `json:"updated_at"`
}

// ServerInput 是创建/更新的请求体；Secret 留空表示不变更（更新语义）。
type ServerInput struct {
	Name     string   `json:"name"`
	Host     string   `json:"host"`
	Port     int      `json:"port"`
	AuthType string   `json:"auth_type"`
	Username string   `json:"username"`
	Secret   string   `json:"secret"`
	Tags     []string `json:"tags"`
}

// ServerExportItem 导入导出格式：凭据以加密块呈现，绝无明文。
type ServerExportItem struct {
	Name         string   `json:"name"`
	Host         string   `json:"host"`
	Port         int      `json:"port"`
	AuthType     string   `json:"auth_type"`
	Username     string   `json:"username"`
	SecretSealed string   `json:"secret_sealed,omitempty"` // 加密块，同主密钥环境下可还原
	SecretMask   string   `json:"secret_mask,omitempty"`   // 无密文时的掩码占位
	Tags         []string `json:"tags"`
}

// NewID 生成资源 ID：前缀 + 8 字节随机十六进制。
func NewID(prefix string) string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		// crypto/rand 失败属于系统级异常，直接 panic 由上层恢复为 5001
		panic("crypto/rand 不可用: " + err.Error())
	}
	return prefix + "-" + hex.EncodeToString(b)
}

// 分页解析结果。
type PageQuery struct {
	Page     int
	PageSize int
}

// NormalizePage 归一化分页参数：page 从 1 起，page_size 默认 20、上限 100。
func NormalizePage(page, pageSize int) PageQuery {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 20
	}
	if pageSize > 100 {
		pageSize = 100
	}
	return PageQuery{Page: page, PageSize: pageSize}
}

// PageEnvelope 列表响应统一信封。
type PageEnvelope[T any] struct {
	Data     []T `json:"data"`
	Total    int `json:"total"`
	Page     int `json:"page"`
	PageSize int `json:"page_size"`
}
