# 架构决策记录

本文档是 [#1](https://github.com/LinuxSuRen/open-img-sync/issues/1) 的核心产出：固化全项目必须先定的决策，后续实现 PR 不得与之冲突；如需推翻某项决策，先修改本文并给出理由。

## 决策点 A：镜像拉取发生在哪里？

| 方案 | 说明 | 优点 | 代价 |
| --- | --- | --- | --- |
| A1 本机 daemon | 工具所在机器装有 Docker，`pull` 后 `save` 流式管道到目标机 `load` | 实现最简单，格式兼容性由 Docker 保证 | 强依赖本机 daemon；平台侧容器化部署（#8）需挂载 docker.sock，攻击面大；跨架构镜像受本机 daemon 限制 |
| A2 纯 Go registry 直拷 | 直接走 Docker Registry V2 API（manifest + blob 流式转发），不依赖 daemon | 无 daemon 依赖；scratch 容器内可运行；天然支持跨架构 manifest list 选择；传输层可控（进度、断点、限速） | 需自行处理 schema2 / OCI index / manifest list、blob 并发与 Range 续传，实现复杂度最高 |
| A3 目标机执行 | 通过 SSH 在目标机 `pull`（目标机需能访问源或中转 registry） | 工具侧零传输实现 | 目标机必须可达 registry，与“内网服务器无法直连外网”的核心场景矛盾；凭据要下发到目标机 |

**结论：A2 为主路径。**

理由：

1. 本工具的定位是“平台侧部署”（见 #8 容器化形态），A1 在 scratch/distroless 镜像里无法工作，挂载 docker.sock 又违背 #10 的安全基线；
2. A3 与产品核心场景（目标机在受限网络）直接冲突，仅当中转 registry（M2）与目标机互通时才成立，此时它退化为 M2 的目标端动作，不作为独立拉取方案；
3. A2 的复杂度通过收窄首期支持范围控制：manifest 仅支持 **OCI image index / Docker manifest list（取目标架构）+ schema2 单 manifest**，不支持 schema1；blob 下载支持并发与 Range 断点续传。

A1 保留为**文档化的回退路径**（未来若出现 A2 无法覆盖的格式，可在后续版本以适配器形式接入），首期不实现。

**A2 实现补充（随 #5 落地）**：`docker load` 兼容 tar 的条目必须先声明大小，而 registry 只提供压缩层大小，因此 M1 逐层采用**临时 spool**——每层下载解压到临时文件（写完即删），再以已知大小写入流式 tar。磁盘占用上限 = 最大一层，不产生整镜像中间文件；SSH 管道侧保持流式。M2（中转推送）压缩大小已知，全程流式零中间文件。

## 决策点 B：同步到目标机的模式

| 模式 | 链路 | 适用 |
| --- | --- | --- |
| M1 SSH 直推（默认） | 源 registry →（A2 拉取）→ 工具内存流式组装 `docker load` 兼容 tar → SSH 管道 → 目标机 `docker load` | 目标机只要能被 SSH 访问即可，无需额外组件 |
| M2 中转 registry | 源 registry →（A2 拉取）→ 推送到中转 registry → 目标机 `pull`（或仅推送到中转即完成） | 目标机数量多、同镜像复用率高，或已有内部 registry |

**结论：两种模式并存，任务级选择；M1 为默认值。** 两种模式复用同一套 A2 拉取层，M2 的目标端就是 #3 中角色为 `relay` 的 registry。

## 决策点 C：模块划分

```
cmd/open-img-sync/     # 入口，仅组装
internal/cli/          # 子命令分发：serve / version / self-check
internal/server/       # HTTP 服务、路由、鉴权中间件、SSE
internal/model/        # 资源模型（Server/Registry/ImageRef/Task），snake_case JSON 契约
internal/store/        # bbolt 持久化 + 加密封装（依赖 internal/crypt）
internal/crypt/        # 主密钥装载与 AES-256-GCM 加解密
internal/errs/         # 稳定错误码 + 三段式错误类型
internal/sshx/         # SSH 连接、命令执行、流式管道
internal/registry/     # Registry V2 客户端：ping / manifest / blob / push、镜像引用解析
internal/aliyun/       # ACR EE 临时凭据：RPC 签名、获取、缓存与刷新
internal/sync/         # 同步引擎：任务状态机、编排、重试、取消、进度
internal/archive/      # docker load 兼容 tar 的流式组装
web/                   # Vue 3 + Vite 前端，构建产物嵌入二进制
docs/                  # 架构与契约文档
```

职责边界：`sync` 是唯一编排者，只通过 `registry`/`sshx`/`archive` 的接口做事；`server` 不含业务逻辑，只做参数校验与调用；`store` 不感知业务语义，只按 bucket 存取已加密的字节。

**存储选型（#10 遗留决策在此定）：bbolt**。理由：纯 Go 零 CGO（#7 要求交叉编译与 scratch 运行）；单文件便于“持久化文件不含明文”的二进制审计（#10 验收项）；由 etcd 团队维护。不选 sqlite（需要 CGO 驱动或引入 modernc 大体积翻译版）。

## 决策点 D：无参数启动行为

`open-img-sync`（无参数）等价于 `open-img-sync serve`，两者行为完全一致。工具的主要交互形态是 Web UI，“拷贝即用”场景下用户双击/裸执行就应得到可用服务。

## 全局技术约束

- Go 1.27+，零 CGO；前端 Vue 3 + Vite，产物 `go:embed` 嵌入，不引重依赖（无 UI 组件库、无路由库、无状态管理库）
- API 字段 snake_case；列表统一 `page`（从 1 起）+ `page_size`（默认 20，上限 100），响应 `{data, total, page, page_size}`；错误响应 `{code, message}`
- 用户可见错误 = 稳定错误码 + 可读提示；技术根因只进日志（#10）
- CLI 必须有 `version` 子命令（#7）
- 协作：feat/fix 分支 + PR；PR 必须附验证命令与输出；UI 改动附截图

## 数据流（M1 全链路）

```
源 registry ──(V2 API: manifest + blob 并发拉取, Range 续传)──> sync 引擎
    sync 引擎 ──(流式组装 docker load tar, 不落盘)──> sshx 管道 ──> 目标机 docker load
    sync 引擎 ──(步骤级进度/日志/字节数)──> server(SSE) ──> Web UI
```
