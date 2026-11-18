# API 契约（草案）

本文档是 [#1](https://github.com/LinuxSuRen/open-img-sync/issues/1) 的产出，随各资源实现 PR（#2/#3/#5）细化；字段一经合入即视为对外契约，改动需走 PR 并说明兼容性。

## 通用约定

- 所有接口返回 JSON，字段命名 **snake_case**
- 列表接口统一分页：`page`（从 1 起）+ `page_size`（默认 20，上限 100），响应体：

```json
{ "data": [], "total": 0, "page": 1, "page_size": 20 }
```

- 错误响应统一信封：`{ "code": 4001, "message": "人话提示" }`；`code` 是稳定错误码（段位表见 [error-codes.md](error-codes.md)，随 #10 落地），技术根因只进服务日志
- 凭据类字段：写入时明文提交（仅 HTTPS/本机场景），**任何读接口一律掩码回显**（如 `ssh-****`、`ak-****`），编辑时留空表示不变更

## 服务器（#2）

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| GET | `/api/servers?name=&tag=&page=&page_size=` | 分页列表，支持名称模糊与标签过滤 |
| POST | `/api/servers` | 创建 |
| GET | `/api/servers/{id}` | 详情 |
| PUT | `/api/servers/{id}` | 更新（凭据留空 = 不变更） |
| DELETE | `/api/servers/{id}` | 删除 |
| POST | `/api/servers/{id}/test` | 连通性测试：SSH 握手 + `docker version` 探测 |
| GET | `/api/servers/export` | 导出 JSON（凭据以加密块或掩码呈现，不含明文） |
| POST | `/api/servers/import` | 导入 JSON（还原非凭据字段） |

服务器字段：

```json
{
  "id": "srv-01hxxx",
  "name": "edge-01",
  "host": "192.168.1.10",
  "port": 22,
  "auth_type": "password | private_key",
  "username": "root",
  "secret_mask": "ssh-****",
  "secret_fingerprint": "sha256:abcd1234",
  "tags": ["prod"],
  "created_at": "2026-01-01T00:00:00Z",
  "updated_at": "2026-01-01T00:00:00Z"
}
```

连通性测试响应：

```json
{
  "reachable": true,
  "docker_ok": true,
  "latency_ms": 35,
  "code": 0,
  "message": "可达，Docker 正常"
}
```

## Registry 与镜像清单（#3）

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| GET | `/api/registries?name=&role=&page=&page_size=` | 列表，role 过滤 `source`/`relay` |
| POST | `/api/registries` | 创建 |
| GET/PUT/DELETE | `/api/registries/{id}` | 详情 / 更新 / 删除 |
| POST | `/api/registries/{id}/test` | `GET /v2/` 探测，区分网络/认证/非 registry |
| POST | `/api/registries/{id}/images/batch` | 批量录入镜像（每行一个引用） |
| GET | `/api/registries/{id}/images?page=&page_size=` | 该来源下的镜像清单 |
| DELETE | `/api/images/{id}` | 删除清单项 |

Registry 字段：

```json
{
  "id": "reg-01hxxx",
  "name": "aliyun-ee-hz",
  "address": "https://xxx-registry.cn-hangzhou.cr.aliyuncs.com",
  "type": "standard | aliyun-temp | anonymous",
  "role": "source | relay",
  "username": "ci-bot",
  "secret_mask": "reg-****",
  "aliyun": {
    "region": "cn-hangzhou",
    "instance": "xxx-registry",
    "ak_mask": "ak-****"
  }
}
```

批量录入响应（逐行给结果）：

```json
{
  "data": [
    { "line": 1, "ok": true, "ref": "library/nginx:1.27" },
    { "line": 2, "ok": false, "reason": "tag 含非法字符" }
  ]
}
```

## 同步任务（#5）

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| POST | `/api/tasks` | 创建并立即排队 |
| GET | `/api/tasks?status=&page=&page_size=` | 历史 |
| GET | `/api/tasks/{id}` | 详情（含步骤级进度） |
| POST | `/api/tasks/{id}/cancel` | 取消（运行中可取消） |
| GET | `/api/tasks/{id}/logs` | SSE 流式日志；带 `?since=<seq>` 时退化为轮询 JSON |

任务字段：

```json
{
  "id": "task-01hxxx",
  "mode": "m1 | m2",
  "status": "queued | running | success | failed | canceled",
  "image_refs": ["library/nginx:1.27"],
  "targets": { "server_ids": ["srv-01hxxx"], "relay_registry_id": "" },
  "steps": [
    { "image_ref": "library/nginx:1.27", "target": "edge-01", "status": "success",
      "bytes": 123456789, "skipped": false, "error_code": 0, "started_at": "", "finished_at": "" }
  ],
  "created_at": "", "finished_at": ""
}
```

## 系统接口

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| GET | `/healthz` | 健康检查，鉴权豁免（容器探针） |
| GET | `/api/version` | 版本信息，与 CLI `version` 子命令同源 |
| GET | `/api/system/info` | 运行信息：数据目录、主密钥来源、并发上限等（#6 页面用） |
