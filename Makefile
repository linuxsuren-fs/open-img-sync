# open-img-sync 构建入口。make build 不依赖 Node（前端产物已提交），
# make web 才需要 Node 20+。

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
BUILDTIME := $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -X github.com/linuxsuren/open-img-sync/internal/cli.version=$(VERSION) \
           -X github.com/linuxsuren/open-img-sync/internal/cli.commit=$(COMMIT) \
           -X github.com/linuxsuren/open-img-sync/internal/cli.buildTime=$(BUILDTIME)
BINARY := bin/open-img-sync

.PHONY: build web test fmt vet clean

build:
	go build -trimpath -ldflags "$(LDFLAGS)" -o $(BINARY) ./cmd/open-img-sync

# 需要 Node 20+：构建前端产物到 web/dist（产物已提交，纯 Go 环境无需执行本目标）
web:
	cd web && npm install && npm run build

# 五平台交叉编译 + 打包（tar.gz/zip 含 README/LICENSE）+ sha256；
# 零 CGO，scratch 静态链接验证由 CI 的 docker job 承担
.PHONY: release
release: VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
release:
	mkdir -p dist
	set -e; for target in linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64; do \
		os=$${target%/*}; arch=$${target#*/}; \
		echo "==> $$os/$$arch"; \
		CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch go build -trimpath -ldflags "-s -w $(LDFLAGS)" -o build/open-img-sync-$${os}-$${arch} ./cmd/open-img-sync; \
		mkdir -p stage; cp README.md LICENSE stage/; \
		bin=open-img-sync; if [ "$$os" = "windows" ]; then cp build/open-img-sync-$${os}-$${arch} stage/open-img-sync.exe; else cp build/open-img-sync-$${os}-$${arch} stage/open-img-sync; fi; \
		if [ "$$os" = "windows" ]; then (cd stage && zip -q -r ../dist/open-img-sync_$(VERSION)_$${os}_$${arch}.zip .); else (cd stage && tar -czf ../dist/open-img-sync_$(VERSION)_$${os}_$${arch}.tar.gz .); fi; \
		rm -rf stage; \
	done
	cd dist && for f in *; do sha256sum "$$f" > "$$f.sha256"; done && ls -l

test:
	go test -race ./...

fmt:
	gofmt -l -w .

vet:
	go vet ./...

clean:
	rm -rf bin/ web/dist
