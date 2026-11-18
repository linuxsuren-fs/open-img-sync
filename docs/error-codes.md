# 错误码段位表

用户可见错误一律为「稳定错误码 + 可读提示」；技术根因只进日志。**新增错误码必须先在本表登记**（协作约定，机制随 [#10](https://github.com/LinuxSuRen/open-img-sync/issues/10) 落地）。

## 段位分配

| 段位 | 模块 | 含义 |
| --- | --- | --- |
| 0 | - | 成功 / 无错误 |
| 1xxx | 凭据服务 | 阿里云 ACR EE 临时凭据获取与刷新（#4） |
| 2xxx | 服务器 | SSH 连通性与目标机 Docker 探测（#2） |
| 3xxx | registry | registry 连通性、镜像引用校验（#3） |
| 4xxx | 同步引擎 | 拉取、传输、加载、编排（#5） |
| 5xxx | 系统 | 通用系统错误：参数、鉴权、存储、内部错误 |

## 错误码登记表

> 状态列：`规划`（文档已登记、实现未合入）/ `生效`（对应代码已合入）。

| 码 | 模块 | 提示（用户可见） | 状态 |
| --- | --- | --- | --- |
| 5001 | 系统 | 系统内部错误 | 生效（internal/errs） |
| 5002 | 系统 | 请求参数不合法 | 生效（internal/errs） |
| 5003 | 系统 | 资源不存在 | 生效（internal/errs） |
| 5004 | 系统 | 未授权的请求 | 生效（internal/server 鉴权中间件） |
| 5005 | 系统 | 主密钥不可用 | 生效（internal/crypt、internal/store） |
| 1001 | 凭据服务 | 阿里云 AccessKey 无效 | 生效（internal/aliyun） |
| 1002 | 凭据服务 | AccessKey 未授权该 ACR 实例 | 生效（internal/aliyun） |
| 1003 | 凭据服务 | 阿里云接口限流，请稍后重试 | 生效（internal/aliyun） |
| 1004 | 凭据服务 | 临时凭据获取失败 | 生效（internal/aliyun） |
| 2001 | 服务器 | 服务器不可达 | 生效（internal/sshx） |
| 2002 | 服务器 | SSH 认证失败 | 生效（internal/sshx） |
| 2003 | 服务器 | 目标机 Docker 不可用 | 生效（internal/sshx） |
| 2004 | 服务器 | 连接超时 | 生效（internal/sshx） |
| 3001 | registry | registry 网络不可达 | 生效（internal/registry） |
| 3002 | registry | registry 认证失败 | 生效（internal/registry） |
| 3003 | registry | 目标不是 registry 服务 | 生效（internal/registry） |
| 3004 | registry | 镜像引用不合法 | 生效（internal/registry） |
| 4001 | 同步引擎 | 源 registry 认证失败 | 生效（internal/sync） |
| 4002 | 同步引擎 | 传输中断 | 生效（internal/sync、internal/registry） |
| 4003 | 同步引擎 | 目标机磁盘已满 | 生效（internal/sshx 流式会话归因） |
| 4004 | 同步引擎 | 目标机加载镜像失败 | 生效（internal/sshx 流式会话归因） |
| 4005 | 同步引擎 | 任务已取消 | 生效（internal/sync） |
| 4006 | 同步引擎 | 部分目标失败 | 生效（internal/sync） |
| 4007 | 同步引擎 | 推送中转 registry 失败 | 生效（internal/registry、internal/sync） |
