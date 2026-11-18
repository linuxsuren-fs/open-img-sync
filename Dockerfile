# open-img-sync container image.
#
# Base images are overridable so builds work behind registry mirrors, e.g.:
#   docker build \
#     --build-arg GO_IMAGE=docker.1ms.run/library/golang:1.27-alpine \
#     --build-arg NODE_IMAGE=docker.1ms.run/library/node:22-alpine \
#     --build-arg GOPROXY=https://goproxy.cn,direct \
#     -t open-img-sync:dev .
#
# 与二进制同版本同源：VERSION 构建参数即 Release tag（见 docker-publish.yml）。

ARG GO_IMAGE=golang:1.27-alpine
ARG NODE_IMAGE=node:22-alpine

# ---- Stage 1: build the Vue frontend into static assets --------------------
FROM ${NODE_IMAGE} AS web
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
ARG NPM_REGISTRY=""
RUN if [ -n "$NPM_REGISTRY" ]; then npm config set registry "$NPM_REGISTRY"; fi \
    && npm ci --no-audit --no-fund
COPY web/ ./
# logo 单源文件（vite 插件构建期从 ../docs/logo.svg 复制）
COPY docs/logo.svg /src/docs/logo.svg
RUN npm run build

# ---- Stage 2: build the static Go binary with the embedded UI --------------
FROM ${GO_IMAGE} AS build
WORKDIR /src
ARG GOPROXY=""
ENV GOPROXY=${GOPROXY}
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
COPY --from=web /src/web/dist ./web/dist
ARG TARGETOS
ARG TARGETARCH
ARG VERSION=dev
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath \
      -ldflags "-s -w \
        -X github.com/linuxsuren/open-img-sync/internal/cli.version=${VERSION} \
        -X github.com/linuxsuren/open-img-sync/internal/cli.commit=$(git rev-parse --short HEAD 2>/dev/null || echo none)" \
      -o /out/open-img-sync ./cmd/open-img-sync \
    && mkdir -p /out/data

# ---- Stage 3: scratch runtime ----------------------------------------------
# 零 CGO 静态二进制 + 内置 self-check 健康探测，无需 shell/CA/包管理器。
# 非 root（nobody 65534）运行，数据目录与 /tmp 可写。
FROM scratch
COPY --from=build /out/open-img-sync /usr/local/bin/open-img-sync
COPY --from=build --chown=65534:65534 /out/data /data
COPY --from=build --chown=65534:65534 /out/data /tmp
ENV OIS_ADDR=:8080
ENV OIS_DATA_DIR=/data
EXPOSE 8080
USER 65534:65534
HEALTHCHECK --interval=30s --timeout=5s --start-period=3s --retries=3 \
  CMD ["/usr/local/bin/open-img-sync", "self-check"]
ENTRYPOINT ["/usr/local/bin/open-img-sync"]
