# 阿里云 ACR 企业版临时凭据配置指南

对应 [#4](https://github.com/LinuxSuRen/open-img-sync/issues/4)。本工具通过 `GetAuthorizationToken` 接口自动换取 ACR EE 的临时登录凭据（默认 1 小时有效），全程无需人工去控制台复制密码。

## 配置步骤

1. 在「Registry 管理」中创建类型为 `aliyun-temp` 的 registry，地址填实例域名，例如 `https://xxx-registry.cn-hangzhou.cr.aliyuncs.com`
2. 填入阿里云配置：`region`（如 `cn-hangzhou`）、`instance`（实例名，如 `xxx-registry`）、RAM 用户的 AccessKey ID 与 Secret
3. AK/SK 以 AES-256-GCM 加密存储（见 #10），任何界面与日志只出现掩码 `ak-****`

## RAM 最小授权策略

只需要目标实例的 GetAuthorizationToken 相关 Action，不要授予 `cr:*` 全量权限：

```json
{
  "Version": "1",
  "Statement": [
    {
      "Effect": "Allow",
      "Action": [
        "cr:GetAuthorizationToken"
      ],
      "Resource": "acs:cr:*:<account-id>:repository/<xxx-registry>/*"
    }
  ]
}
```

说明：

- `Action` 仅 `cr:GetAuthorizationToken`——换取临时登录凭据所需的最小权限
- `Resource` 限定到具体实例（`<xxx-registry>` 为实例名），不要使用通配 `*`
- 建议为同步工具单独创建 RAM 用户并只挂载本策略；AK 泄露的影响面被限制在「换取临时令牌」这一件事上
- 若返回 1002（未授权该实例），先检查 Resource 中的账号 ID 与实例名是否正确

## 实现方式

选择 **REST RPC 签名直调**（HMAC-SHA1，标准库实现），不引阿里云官方 SDK：

- 官方 Go SDK 依赖体积大（darabonba 系依赖树），而本项目只用 `GetAuthorizationToken` 一个接口
- 签名实现约 60 行标准库代码，无第三方依赖，符合「不引重依赖」约束
- 端点：`https://cr.{region}.aliyuncs.com/`，API 版本 `2018-12-01`

## 错误分类（稳定错误码）

| 码 | 场景 |
| --- | --- |
| 1001 | AccessKey 无效 / 签名不匹配（InvalidAccessKeyId.* / SignatureDoesNotMatch） |
| 1002 | 未授权该实例（Forbidden.* / NoPermission / AccessDenied） |
| 1003 | 接口限流（Throttling / RequestLimitExceeded） |
| 1004 | 其他获取失败（网络、响应损坏等） |

## 缓存与刷新

- 临时凭据仅存**内存缓存**，绝不落盘、绝不进日志
- 缓存 key = registry ID + AK 指纹；更换 AK 后旧缓存自动失效，保存 registry 时主动清除
- 到期前 5 分钟自动刷新；并发任务共享同一份缓存，同一 key 同时只发起一次换取
