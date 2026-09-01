# Toolchains run in Docker — nothing needs installing locally.
# Override the images to pin different versions.

# Machine-specific overrides (DOCKER_DNS, image pins). Gitignored, optional.
-include Makefile.local

# Docker's bridge network cannot use a host resolv.conf that points at
# systemd-resolved's 127.0.0.53 stub, so it substitutes 8.8.8.8 — which some
# networks block, leaving module downloads to time out. Set DOCKER_DNS to the
# host's real upstream resolvers when that happens.
DOCKER_DNS  ?=
DNS_FLAGS   := $(foreach server,$(DOCKER_DNS),--dns $(server))

GO_IMAGE    ?= golang:1
NODE_IMAGE  ?= node:22-alpine
SQLC_IMAGE  ?= sqlc/sqlc:latest
LINT_IMAGE  ?= golangci/golangci-lint:latest

# Generator versions are pinned, not floated: generated output is committed, so
# a generator that changes its formatting turns every unrelated build red on the
# CI staleness check (ADR-31).
OAPI_VERSION        ?= v2.4.1
OPENAPI_TS_VERSION  ?= 7.4.4
OAPI_CODEGEN        := github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@$(OAPI_VERSION)

UID         := $(shell id -u)
GID         := $(shell id -g)
MOUNT       := -v $(CURDIR):/src -w /src
# Mounted at /go/pkg rather than /go/pkg/mod: the checksum database cache lives
# beside the module cache at /go/pkg/sumdb, and a module download fails without it.
CACHE       := -v medianexus-gopath:/go/pkg -v medianexus-gobuild:/.cache/go-build
GOENV       := -e GOCACHE=/.cache/go-build -e CGO_ENABLED=0 -e HOME=/tmp
RUN_GO      := docker run --rm -u $(UID):$(GID) $(DNS_FLAGS) $(MOUNT) $(CACHE) $(GOENV) $(GO_IMAGE)
RUN_NODE    := docker run --rm -u $(UID):$(GID) $(DNS_FLAGS) $(MOUNT)/web -w /src/web \
                 -v medianexus-npm:/tmp/.npm -e npm_config_cache=/tmp/.npm $(NODE_IMAGE)
# The spec lives at the repository root, not under web/, so generation needs a
# runner whose working directory is the root.
RUN_NODE_ROOT := docker run --rm -u $(UID):$(GID) $(DNS_FLAGS) $(MOUNT) \
                 -v medianexus-npm:/tmp/.npm -e npm_config_cache=/tmp/.npm $(NODE_IMAGE)

.PHONY: help verify build test race bench lint vet fmt fmt-check sqlc sqlc-check \
        api web web-dev image image-local builder milestone version cache-init clean-cache deps tidy

VERSION     := $(shell tr -d '[:space:]' < VERSION 2>/dev/null || echo 0.0.0)
COMMIT      := $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
BUILD_DATE  := $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS     := -X main.version=$(VERSION) -X main.commit=$(COMMIT) -X main.buildDate=$(BUILD_DATE)
IMAGE       ?= ghcr.io/makearr/medianexus
PLATFORMS   ?= linux/amd64,linux/arm64
ARCH        := $(shell uname -m | sed -e s/x86_64/amd64/ -e s/aarch64/arm64/)

# buildx is required for a release: the legacy builder cannot produce a
# multi-arch manifest, and shipping only amd64 would silently drop arm64 --
# which ADR-35 cares about, because fingerprinting runs there.
HAS_BUILDX  := $(shell docker buildx version >/dev/null 2>&1 && echo yes)

# The default "docker" driver cannot export a manifest list, so a multi-arch
# build needs a container-driver builder. Created on demand by the builder
# target; it persists, so this costs nothing after the first run.
BUILDER     ?= medianexus
TOKEN       ?= $(shell grep -oP 'ghp_[A-Za-z0-9]+' private/secrets.md 2>/dev/null | head -1)

help:  ## List targets
	@grep -hE '^[a-z-]+:.*?## ' $(MAKEFILE_LIST) | awk -F':.*## ' '{printf "  %-14s %s\n", $$1, $$2}'

## Docker creates named volumes owned by root, so a container running as the
## invoking user cannot write to the module or build cache. Fixed once, guarded
## by a marker so the check costs one container start per make invocation.
cache-init:
	@docker run --rm -u 0 $(CACHE) -v medianexus-npm:/tmp/.npm $(GO_IMAGE) sh -c \
	  'for dir in /go/pkg /.cache/go-build /tmp/.npm; do \
	     [ "$$(stat -c %u $$dir)" = "$(UID)" ] || chown -R $(UID):$(GID) $$dir; \
	   done'

## ---- the gate: everything that must pass before a subsystem is "done" ----
verify: fmt-check vet lint test sqlc-check  ## Full check, as CI runs it

build: cache-init  ## Build the binary (CGO_ENABLED=0, per ADR-30)
	$(RUN_GO) go build -trimpath -ldflags "$(LDFLAGS)" -o /src/medianexus ./cmd/medianexus

test: cache-init  ## Unit tests
	$(RUN_GO) go test -count=1 ./...

race: cache-init  ## Tests with the race detector (needs CGO)
	docker run --rm -u $(UID):$(GID) $(DNS_FLAGS) $(MOUNT) $(CACHE) -e GOCACHE=/.cache/go-build -e HOME=/tmp \
	  $(GO_IMAGE) go test -race -count=1 ./...

bench: cache-init  ## Benchmarks — ADR-28 performance targets
	$(RUN_GO) go test -run=^$$ -bench=. -benchmem ./...

vet: cache-init
	$(RUN_GO) go vet ./...

lint: cache-init
	docker run --rm -u $(UID):$(GID) $(MOUNT) $(CACHE) -e HOME=/tmp $(LINT_IMAGE) golangci-lint run

fmt: cache-init  ## Format in place
	$(RUN_GO) gofmt -w -s .

fmt-check: cache-init
	@out=$$($(RUN_GO) gofmt -l -s .); \
	 if [ -n "$$out" ]; then echo "unformatted:"; echo "$$out"; exit 1; fi

## ---- dependencies ----
deps: cache-init  ## Add or update a module: make deps MODULE=modernc.org/sqlite
	@test -n "$(MODULE)" || { echo "usage: make deps MODULE=<path>"; exit 1; }
	$(RUN_GO) sh -c 'GOFLAGS=-mod=mod go get $(MODULE) && go mod tidy'

tidy: cache-init  ## Reconcile go.mod and go.sum with the source
	$(RUN_GO) go mod tidy

## ---- generated code: committed, never hand-edited (ADR-31) ----
sqlc:       ## Regenerate from queries/*.sql
	@test -f sqlc.yaml || { echo "no sqlc.yaml yet"; exit 1; }
	docker run --rm -u $(UID):$(GID) $(MOUNT) $(SQLC_IMAGE) generate

## Skips until sqlc.yaml exists so `verify` is green from the first commit,
## matching .github/workflows/ci.yml. It is a hard failure once it does.
sqlc-check: ## Fail if generated SQL code is stale
	@test -f sqlc.yaml || { echo "sqlc-check: no sqlc.yaml yet, skipping"; exit 0; }; \
	 docker run --rm -u $(UID):$(GID) $(MOUNT) $(SQLC_IMAGE) diff

api:        ## Regenerate Go server + TS client from api/openapi.yaml
	@test -f api/openapi.yaml || { echo "api: no spec yet, skipping"; exit 0; }
	@mkdir -p internal/apiserver/gen web/src/api
	$(RUN_GO) go run $(OAPI_CODEGEN) -config api/oapi-codegen.yaml api/openapi.yaml
	$(RUN_NODE_ROOT) npx --yes openapi-typescript@$(OPENAPI_TS_VERSION) \
	  api/openapi.yaml -o web/src/api/schema.d.ts
	$(RUN_GO) gofmt -w internal/apiserver/gen

## ---- web ----
web:        ## Production build, embedded via embed.FS
	$(RUN_NODE) npm ci && $(RUN_NODE) npm run build

web-dev:    ## Vite dev server on :5173, proxying the API
	docker run --rm -it -u $(UID):$(GID) $(MOUNT)/web -w /src/web -p 5173:5173 \
	  -v medianexus-npm:/tmp/.npm -e npm_config_cache=/tmp/.npm $(NODE_IMAGE) npm run dev

## ---- versioning: builds advance BUILD, milestones advance MINOR ----
## Full scheme: documentation/versioning.md
version:    ## Print the current version
	@echo $(VERSION)

image:      ## Release build: multi-arch, push to ghcr, bump, changelog, tag, push, release
	@test -f Dockerfile || { echo "no Dockerfile yet"; exit 1; }
	@test -n "$(HAS_BUILDX)" || { \
	  echo "docker buildx is not installed, and a release must be multi-arch."; \
	  echo "Install it (Debian/Ubuntu: 'apt install docker-buildx'), then re-run."; \
	  echo "Use 'make image-local' for a single-arch image without it."; \
	  exit 1; }
	@$(MAKE) --no-print-directory builder
	@# A release image must correspond to a commit, or the About page (AGPL §13)
	@# claims a source revision that does not describe the running binary.
	@git diff --quiet && git diff --cached --quiet || 	  { echo "working tree is dirty — commit first, or use 'make image-local'"; exit 1; }
	@echo "$(TOKEN)" | docker login ghcr.io -u makearr --password-stdin
	docker buildx build --builder $(BUILDER) --push --platform $(PLATFORMS) \
	  --build-arg VERSION=$(VERSION) --build-arg COMMIT=$(COMMIT) \
	  --build-arg BUILD_DATE=$(BUILD_DATE) \
	  -t $(IMAGE):$(VERSION) -t $(IMAGE):latest .
	@./scripts/release.sh

image-local: ## Local single-arch build: allows a dirty tree, no bump, no push
	@test -f Dockerfile || { echo "no Dockerfile yet"; exit 1; }
	@# Falls back to the legacy builder when buildx is absent. The Dockerfile
	@# gives its platform args defaults for exactly this, so a machine without
	@# buildx can still build and run the image it is about to deploy.
	@if [ -n "$(HAS_BUILDX)" ]; then \
	  $(MAKE) --no-print-directory builder; \
	  docker buildx build --builder $(BUILDER) --load --platform linux/$(ARCH) \
	    --build-arg VERSION=$(VERSION)-dirty --build-arg COMMIT=$(COMMIT) \
	    --build-arg BUILD_DATE=$(BUILD_DATE) -t $(IMAGE):dev . ; \
	else \
	  echo "buildx not found; building single-arch with the legacy builder"; \
	  docker build --build-arg VERSION=$(VERSION)-dirty --build-arg COMMIT=$(COMMIT) \
	    --build-arg BUILD_DATE=$(BUILD_DATE) --build-arg TARGETARCH=$(ARCH) \
	    -t $(IMAGE):dev . ; \
	fi
	@echo "built $(IMAGE):dev - version held at $(VERSION)"

milestone:  ## Advance to the next milestone: MINOR+1, BUILD reset to 0
	@awk -F. '{printf "%s.%d.0\n", $$1, $$2+1}' VERSION > VERSION.tmp && mv VERSION.tmp VERSION
	@echo "milestone: $(VERSION) -> $$(cat VERSION)"
	@echo "tag it:  git tag v$$(cat VERSION) && git push origin v$$(cat VERSION)"

builder:    ## Create the multi-arch builder if it does not exist
	@docker buildx inspect $(BUILDER) >/dev/null 2>&1 || { \
	  echo "creating the $(BUILDER) buildx builder"; \
	  docker buildx create --name $(BUILDER) --driver docker-container --bootstrap >/dev/null; }

clean-cache:
	-docker volume rm medianexus-gopath medianexus-gobuild medianexus-npm
	-docker buildx rm $(BUILDER)
