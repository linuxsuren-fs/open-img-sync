package model

import "time"

// Registry 类型与角色枚举（对外契约，不得改名）。
const (
	RegistryTypeStandard   = "standard"    // 固定凭据
	RegistryTypeAliyunTemp = "aliyun-temp" // 阿里云企业版临时凭据（#4）
	RegistryTypeAnonymous  = "anonymous"   // 匿名
	RegistryRoleSource     = "source"      // 镜像来源
	RegistryRoleRelay      = "relay"       // 中转 registry（M2 模式目标端）
)

// AliyunConfig 阿里云 ACR EE 配置（type=aliyun-temp 时生效）。
// AK/SK 只存密文与指纹，绝无明文。
type AliyunConfig struct {
	Region        string `json:"region"`
	Instance      string `json:"instance"`
	AKSealed      string `json:"ak_sealed"`
	SKSealed      string `json:"sk_sealed"`
	AKFingerprint string `json:"ak_fingerprint"`
}

// RegistryRecord 是 registry 的持久化形态。
type RegistryRecord struct {
	ID                string       `json:"id"`
	Name              string       `json:"name"`
	Address           string       `json:"address"` // 已规范化：含 scheme、无尾斜杠
	Type              string       `json:"type"`
	Role              string       `json:"role"`
	Username          string       `json:"username"`
	SecretSealed      string       `json:"secret_sealed"` // standard 的密码/Token
	SecretFingerprint string       `json:"secret_fingerprint"`
	Aliyun            AliyunConfig `json:"aliyun"`
	CreatedAt         time.Time    `json:"created_at"`
	UpdatedAt         time.Time    `json:"updated_at"`
}

// AliyunView 阿里云配置的回显形态：只含掩码与指纹。
type AliyunView struct {
	Region        string `json:"region"`
	Instance      string `json:"instance"`
	AKMask        string `json:"ak_mask"`
	AKFingerprint string `json:"ak_fingerprint"`
}

// RegistryView 是 registry 的 API 回显形态。
type RegistryView struct {
	ID                string     `json:"id"`
	Name              string     `json:"name"`
	Address           string     `json:"address"`
	Type              string     `json:"type"`
	Role              string     `json:"role"`
	Username          string     `json:"username"`
	SecretMask        string     `json:"secret_mask"`
	SecretFingerprint string     `json:"secret_fingerprint"`
	Aliyun            AliyunView `json:"aliyun"`
	CreatedAt         time.Time  `json:"created_at"`
	UpdatedAt         time.Time  `json:"updated_at"`
}

// RegistryInput 创建/更新请求体；凭据留空 = 不变更。
type RegistryInput struct {
	Name     string      `json:"name"`
	Address  string      `json:"address"`
	Type     string      `json:"type"`
	Role     string      `json:"role"`
	Username string      `json:"username"`
	Secret   string      `json:"secret"` // standard：密码/Token
	Aliyun   AliyunInput `json:"aliyun"`
}

// AliyunInput 阿里云配置输入；AccessKeySecret 留空 = 不变更。
type AliyunInput struct {
	Region          string `json:"region"`
	Instance        string `json:"instance"`
	AccessKeyID     string `json:"access_key_id"`
	AccessKeySecret string `json:"access_key_secret"`
}

// ImageRecord 待同步镜像清单项。
type ImageRecord struct {
	ID         string    `json:"id"`
	RegistryID string    `json:"registry_id"`
	Ref        string    `json:"ref"` // 规范化后的完整引用
	CreatedAt  time.Time `json:"created_at"`
}

// ImageView 镜像清单回显。
type ImageView struct {
	ID         string    `json:"id"`
	RegistryID string    `json:"registry_id"`
	Ref        string    `json:"ref"`
	CreatedAt  time.Time `json:"created_at"`
}

// ImageBatchInput 批量录入：每行一个镜像引用。
type ImageBatchInput struct {
	Refs string `json:"refs"`
}

// ImageBatchItem 批量录入逐行结果（第几行、原因）。
type ImageBatchItem struct {
	Line   int    `json:"line"`
	OK     bool   `json:"ok"`
	Ref    string `json:"ref,omitempty"`
	Reason string `json:"reason,omitempty"`
}
