# open-img-sync

<p align="center"><img src="docs/logo.svg" width="120"></p>

把 Docker 镜像同步到无法直连外网（或网络受限）的服务器，终结手动 `docker pull` → `docker save` → `scp` → `docker load` 的循环。

## 痛点

| 现在的手动流程 | 用 open-img-sync |
| --- | --- |
| 本地 `docker pull`，占用本机磁盘与带宽 | 平台侧拉取，不依赖本机 Docker |
| `docker save` 导出大文件 | 流式传输，无中间文件 |
| `scp` 到目标服务器，手动输入账号密码 | 服务器与账号集中管理，一次配置 |
| 登录目标机 `docker load` | 自动远端 load，进度与失败重试可见 |
| 阿里云企业版等临时密码每次去控制台复制 | 自动获取并刷新临时凭据 |

## 规划能力

本仓库当前处于**需求收集与拆解阶段**，实现以 [issues](https://github.com/LinuxSuRen/open-img-sync/issues) 为准，欢迎认领：

- 需求总览与架构决策：[#1](https://github.com/LinuxSuRen/open-img-sync/issues/1)
- Linux 服务器与 SSH 账号管理：[#2](https://github.com/LinuxSuRen/open-img-sync/issues/2)
- 镜像源与 Registry 凭据管理：[#3](https://github.com/LinuxSuRen/open-img-sync/issues/3)
- 阿里云 ACR 企业版临时凭据自动获取：[#4](https://github.com/LinuxSuRen/open-img-sync/issues/4)
- 同步引擎与任务编排：[#5](https://github.com/LinuxSuRen/open-img-sync/issues/5)
- Web 管理界面（Vue 3）：[#6](https://github.com/LinuxSuRen/open-img-sync/issues/6)
- 单二进制分发与 CLI 规范：[#7](https://github.com/LinuxSuRen/open-img-sync/issues/7)
- 容器化运行与镜像发布：[#8](https://github.com/LinuxSuRen/open-img-sync/issues/8)
- CI 门面与仓库自动化：[#9](https://github.com/LinuxSuRen/open-img-sync/issues/9)
- 安全基线：凭据加密与错误码规范：[#10](https://github.com/LinuxSuRen/open-img-sync/issues/10)

## 技术形态与架构摘要

- 后端 Go，前端 Vue 3 + Vite，构建产物嵌入二进制
- 单二进制即可运行，同时提供多架构容器镜像（GHCR）
- CLI 提供 `serve` 与 `version` 子命令；无参数执行等价于 `serve`
- 拉取层：纯 Go 直连 Docker Registry V2 API（manifest + blob 流式转发），不依赖本机 Docker daemon
- 同步双模式：M1 SSH 直推（默认，流式组装 `docker load` 管道）/ M2 中转 registry
- 存储：bbolt 单文件，所有凭据 AES-256-GCM 加密落盘

架构决策（拉取位置、传输模式、模块划分、存储选型）与理由见 [docs/architecture.md](docs/architecture.md)；接口契约见 [docs/api-contract.md](docs/api-contract.md)；错误码段位表见 [docs/error-codes.md](docs/error-codes.md)。

## 快速开始

```bash
make build && ./bin/open-img-sync        # 无参数等价于 serve，浏览器打开 http://127.0.0.1:8080
```

Web 管理界面已嵌入二进制：总览、服务器管理、镜像源与清单、同步任务（步骤级进度与日志）、系统信息五个页面，与 API 能力一一对应。

## 开发

```bash
make build    # 纯 Go 环境：编译 bin/open-img-sync（前端产物已提交，无需 Node）
make test     # go test -race ./...
make web      # 需要 Node 20+：构建前端
make release  # 五平台交叉编译 + 打包（tar.gz/zip 含 README/LICENSE）+ sha256
```

## 发布

- CLI 子命令：`serve`（默认，无参数等价）与 `version`（`--version` 等价并存）
- tag（`v*`）创建 Release 后自动触发 [release workflow](.github/workflows/release.yml)：linux/darwin × amd64/arm64 + windows/amd64 五平台静态二进制，打包附 README、LICENSE 与 sha256 校验，统一上传到该 Release
- 同一 Release 事件并行触发 [docker workflow](.github/workflows/docker-publish.yml)：多架构镜像（linux/amd64 + arm64）推送 GHCR `ghcr.io/linuxsuren/open-img-sync`，与二进制**同版本同源**（同 tag 注入 VERSION）。GHCR 包首次推送后默认私有，如需公开：GitHub 仓库 → Packages → 该包 → Package settings → Change visibility
- 镜像标签策略：`latest` 仅正式版；语义化三级（`v1.2.3` / `1.2` / `1`）；`sha-<短哈希>`
- 容器运行：scratch 基础层、非 root、内置 `self-check` 健康探测（HEALTHCHECK 不依赖 wget/curl）、SIGTERM 优雅排空；加固 compose 示例见 [compose.yml](compose.yml)（read_only + tmpfs + no-new-privileges + 资源上限）
- 版本注入走 ldflags（`git describe` 同源），`version` 子命令与 `/api/version` 输出一致

## 安全基线

- 凭据（SSH 密码/私钥、registry 密码、阿里云 AK/SK）一律 **AES-256-GCM 加密**后落盘，库文件不含明文（标准库实现，无第三方加密依赖）
- 主密钥优先级：环境变量 `OIS_MASTER_KEY`（base64 的 32 字节）> 首次启动自动生成 `数据目录/master.key`（0600）
- 所有读接口凭据只回显掩码（如 `ssh-****`）与指纹，日志不落明文
- 可选 Bearer 鉴权：设置环境变量 `OIS_TOKEN` 后，`/api/*` 需携带 `Authorization: Bearer <token>`，`/healthz` 豁免
- 错误呈现三段式：稳定错误码 + 可读提示 + 技术根因（只进日志），段位表见 [docs/error-codes.md](docs/error-codes.md)

## 协作约定

- 功能开发一律 `feat/`、`fix/` 分支 + Pull Request，主分支受保护（禁直推、禁强推），合并由维护者执行
- PR 描述须附验证证据（测试命令与输出）；涉及 UI 的改动须附页面截图（模板见 [.github/PULL_REQUEST_TEMPLATE.md](.github/PULL_REQUEST_TEMPLATE.md)）
- PR 打开即自动跑 [CI](.github/workflows/ci.yml)：gofmt/vet、`go test -race` 全量、前端构建与提交产物一致性校验、二进制构建 + Playwright 关键路径 e2e、镜像构建验证；`test` 为 required check（红灯在 review 前拦截）
- 提交信息使用 Conventional Commits（中文描述）

## 许可证

[Apache-2.0](LICENSE)
