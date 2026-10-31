# Toolchains run in Docker — nothing needs installing locally.
# Override the images to pin different versions.

# Machine-specific overrides (DOCKER_DNS, image pins). Gitignored, optional.
-include Makefile.local

# Docker's bridge network cannot use a host resolv.conf that points at systemd-resolved's
# 127.0.0.53 stub, so it substitutes 8.8.8.8 — which some networks block, leaving module
DOCKER_DNS  ?=
DNS_FLAGS   := $(foreach server,$(DOCKER_DNS),--dns $(server))

GO_IMAGE    ?= golang:1
NODE_IMAGE  ?= node:22-alpine
SQLC_IMAGE  ?= sqlc/sqlc:latest
LINT_IMAGE  ?= golangci/golangci-lint:latest

# Generator versions are pinned, not floated: generated output is committed, so a generator that
# changes its formatting turns every unrelated build red on the CI staleness check (ADR-31).
OAPI_VERSION        ?= v2.4.1
OPENAPI_TS_VERSION  ?= 7.4.4
OAPI_CODEGEN        := github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@$(OAPI_VERSION)

UID         := $(shell id -u)
GID         := $(shell id -g)
MOUNT       := -v $(CURDIR):/src -w /src
# Mounted at /go/pkg rather than /go/pkg/mod: the checksum database cache lives
# beside the module cache at /go/pkg/sumdb, and a module download fails without it.
CACHE       := -v solarr-gopath:/go/pkg -v solarr-gobuild:/.cache/go-build
GOENV       := -e GOCACHE=/.cache/go-build -e CGO_ENABLED=0 -e HOME=/tmp
RUN_GO      := docker run --rm -u $(UID):$(GID) $(DNS_FLAGS) $(MOUNT) $(CACHE) $(GOENV) $(GO_IMAGE)
# ADR-22's scale is 500000; the default keeps an ordinary test run to seconds.
BENCH_FILES ?=
RUN_GO_BENCH := docker run --rm -u $(UID):$(GID) $(DNS_FLAGS) $(MOUNT) $(CACHE) $(GOENV) \
                 -e SOLARR_BENCH=1 -e SOLARR_BENCH_FILES=$(BENCH_FILES) $(GO_IMAGE)
RUN_NODE    := docker run --rm -u $(UID):$(GID) $(DNS_FLAGS) $(MOUNT)/web -w /src/web \
                 -v solarr-npm:/tmp/.npm -e npm_config_cache=/tmp/.npm $(NODE_IMAGE)
# The spec lives at the repository root, not under web/, so generation needs a
# runner whose working directory is the root.
RUN_NODE_ROOT := docker run --rm -u $(UID):$(GID) $(DNS_FLAGS) $(MOUNT) \
                 -v solarr-npm:/tmp/.npm -e npm_config_cache=/tmp/.npm $(NODE_IMAGE)

.PHONY: help verify build test race bench bench-drivers lint vet fmt fmt-check sqlc sqlc-check \
        api web web-test web-dev e2e image image-local push scan-image builder binfmt milestone version cache-init clean-cache deps tidy \
        license-check

# Version is year.month.release.build, YYYY.MM.R.BBBB (documentation/versioning.md). Never parse
# it as one decimal; build is a zero-padded string, so arithmetic goes through expr and pad4.
VERSION     := $(shell tr -d '[:space:]' < VERSION 2>/dev/null || echo 0000.00.0.0000)
VERSION_PARTS := $(subst ., ,$(VERSION))
CUR_YEAR    := $(word 1,$(VERSION_PARTS))
CUR_MONTH   := $(word 2,$(VERSION_PARTS))
CUR_RELEASE := $(word 3,$(VERSION_PARTS))
CUR_BUILD   := $(word 4,$(VERSION_PARTS))
TODAY_YEAR  := $(shell date -u +%Y)
TODAY_MONTH := $(shell date -u +%m)
pad4         = $(shell printf %04d $(1))
NEXT_BUILD  := $(call pad4,$(shell expr $(CUR_BUILD) + 1))
# A release (make image, on the user's word only) is release+1 in the same month, 1 in a new one.
ifeq ($(TODAY_YEAR)-$(TODAY_MONTH),$(CUR_YEAR)-$(CUR_MONTH))
NEXT_RELEASE := $(shell expr $(CUR_RELEASE) + 1)
else
NEXT_RELEASE := 1
endif
NEXT_VERSION  := $(TODAY_YEAR).$(TODAY_MONTH).$(NEXT_RELEASE).$(NEXT_BUILD)
# Every successful build or push that is not a release advances only the build number.
BUILD_VERSION := $(CUR_YEAR).$(CUR_MONTH).$(CUR_RELEASE).$(NEXT_BUILD)
# Only trailing whitespace is trimmed -- a milestone name legitimately contains
# internal spaces ("music, usenet"), unlike VERSION's single unspaced token.
MILESTONE   := $(shell head -n1 MILESTONE 2>/dev/null | sed -e 's/[[:space:]]*$$//')
COMMIT      := $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
BUILD_DATE  := $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
# -ldflags's value is itself whitespace-split, so a milestone name containing a
# space ("music, usenet") needs its own quoting or the build fails outright.
# A local image built from uncommitted changes says so in its version.
DIRTY       := $(shell git diff --quiet && git diff --cached --quiet || echo -dirty)
LDFLAGS     := -X main.version=$(VERSION) -X main.commit=$(COMMIT) -X main.buildDate=$(BUILD_DATE) \
               -X 'main.milestone=$(MILESTONE)'
IMAGE       ?= ghcr.io/makearr/solarr
HUB_IMAGE   ?= docker.io/makearr/solarr
PLATFORMS   ?= linux/amd64,linux/arm64
ARCH        := $(shell uname -m | sed -e s/x86_64/amd64/ -e s/aarch64/arm64/)

# buildx is required for a release: the legacy builder cannot produce a multi-arch manifest, and
# shipping only amd64 would silently drop arm64 -- which ADR-35 cares about, because
HAS_BUILDX  := $(shell docker buildx version >/dev/null 2>&1 && echo yes)

# The default "docker" driver cannot export a manifest list, so a multi-arch build needs a
# container-driver builder.
BUILDER     ?= solarr
TOKEN       ?= $(shell grep -oP '(ghp_[A-Za-z0-9]+|github_pat_[A-Za-z0-9_]+)' private/secrets.md 2>/dev/null | head -1)
hub_field    = $(shell sed -n '/^\#\# Docker Hub/,/^\#\# /s/^| $(1) | `\(.*\)` |$$/\1/p' private/secrets.md 2>/dev/null)
HUB_USER    ?= $(call hub_field,User)
SCAN_DIR    ?= /tmp/solarr-release-scan
comma       := ,
RELEASE_ARGS = --build-arg VERSION=$(NEXT_VERSION) --build-arg COMMIT=$(COMMIT) \
               --build-arg BUILD_DATE=$(BUILD_DATE) --build-arg MILESTONE="$(MILESTONE)"

help:  ## List targets
	@grep -hE '^[a-z-]+:.*?## ' $(MAKEFILE_LIST) | awk -F':.*## ' '{printf "  %-14s %s\n", $$1, $$2}'

# # Docker creates named volumes owned by root, so a container running as the # invoking user
# cannot write to the module or build cache.
cache-init:
	@docker run --rm -u 0 $(CACHE) -v solarr-npm:/tmp/.npm $(GO_IMAGE) sh -c \
	  'for dir in /go/pkg /.cache/go-build /tmp/.npm; do \
	     [ "$$(stat -c %u $$dir)" = "$(UID)" ] || chown -R $(UID):$(GID) $$dir; \
	   done'

## ---- the gate: everything that must pass before a subsystem is "done" ----
verify: fmt-check vet lint test sqlc-check license-check  ## Full check, as CI runs it

license-check: ## Fail if the About page's embedded LICENSE/ATTRIBUTION copies drifted from the root
	@diff -q LICENSE internal/api/apiserver/legal/LICENSE >/dev/null || \
	  { echo "license-check: internal/api/apiserver/legal/LICENSE is stale -- copy LICENSE over it"; exit 1; }
	@diff -q ATTRIBUTION.md internal/api/apiserver/legal/ATTRIBUTION.md >/dev/null || \
	  { echo "license-check: internal/api/apiserver/legal/ATTRIBUTION.md is stale -- copy ATTRIBUTION.md over it"; exit 1; }

build: cache-init  ## Build the binary (CGO_ENABLED=0, per ADR-30)
	$(RUN_GO) go build -trimpath -ldflags "$(LDFLAGS)" -o /src/solarr ./cmd/solarr

test: cache-init  ## Unit tests
	$(RUN_GO) go test -count=1 ./...

race: cache-init  ## Tests with the race detector (needs CGO)
	docker run --rm -u $(UID):$(GID) $(DNS_FLAGS) $(MOUNT) $(CACHE) -e GOCACHE=/.cache/go-build -e HOME=/tmp \
	  $(GO_IMAGE) go test -race -count=1 -timeout 40m ./...

# -p 1: packages run one at a time, or a latency figure measures the other packages.
bench: cache-init  ## Benchmarks and ADR-28 targets. Scale with BENCH_FILES=500000
	$(RUN_GO_BENCH) go test -count=1 -p 1 -v -run=TestADR28 -bench=. -benchmem -timeout 60m ./...

bench-drivers: cache-init  ## ADR-5: the same measurements under modernc and ncruces
	$(RUN_GO_BENCH) go test -count=1 -p 1 -v -run=TestADR28 -bench=. -benchmem -timeout 60m ./internal/db/library/
	$(RUN_GO_BENCH) go test -count=1 -p 1 -v -tags ncruces -run=TestADR28 -bench=. -benchmem -timeout 60m ./internal/db/library/

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

api: cache-init  ## Regenerate Go server + TS client from api/openapi.yaml
	@test -f api/openapi.yaml || { echo "api: no spec yet, skipping"; exit 0; }
	@mkdir -p internal/api/apiserver/gen web/src/api
	$(RUN_GO) go run $(OAPI_CODEGEN) -config api/oapi-codegen.yaml api/openapi.yaml
	$(RUN_NODE_ROOT) npx --yes openapi-typescript@$(OPENAPI_TS_VERSION) \
	  api/openapi.yaml -o web/src/api/schema.d.ts
	$(RUN_GO) gofmt -w internal/api/apiserver/gen

## ---- web ----
## Old bundles are cleared first: vite keeps the folder (for its .gitkeep), and go:embed takes everything in it.
web:        ## Production build, embedded via embed.FS. Lints and tests first.
	@rm -rf internal/api/webui/dist/assets
	$(RUN_NODE) npm ci && $(RUN_NODE) npm run build

e2e:        ## One-shot smoke run: builds the image, drives a throwaway instance, removes it
	@$(MAKE) --no-print-directory image-local
	@E2E_IMPORT_FOLDER="$(E2E_IMPORT_FOLDER)" scripts/e2e.sh

web-test:   ## Frontend unit tests only
	$(RUN_NODE) npm ci && $(RUN_NODE) npm run test

web-dev:    ## Vite dev server on :5173, proxying the API
	docker run --rm -it -u $(UID):$(GID) $(MOUNT)/web -w /src/web -p 5173:5173 \
	  -v solarr-npm:/tmp/.npm -e npm_config_cache=/tmp/.npm $(NODE_IMAGE) npm run dev

## ---- versioning: builds and pushes advance BUILD, releases advance RELEASE ----
## Full scheme: documentation/versioning.md
version:    ## Print the current version (and milestone, if named)
	@echo "$(VERSION)$(if $(MILESTONE), -- $(MILESTONE),)"

image:      ## Release (user's word only): secrets scan, multi-arch, ghcr + Docker Hub, changelog, tag. [RELEASE_NOTES=file]
	@test -f Dockerfile || { echo "no Dockerfile yet"; exit 1; }
	@test -n "$(HAS_BUILDX)" || { \
	  echo "docker buildx is not installed, and a release must be multi-arch."; \
	  echo "Install it (Debian/Ubuntu: 'apt install docker-buildx'), then re-run."; \
	  echo "Use 'make image-local' for a single-arch image without it."; \
	  exit 1; }
	@$(MAKE) --no-print-directory binfmt
	@$(MAKE) --no-print-directory builder
	@# A release image must correspond to a commit, or the About page (AGPL §13)
	@# claims a source revision that does not describe the running binary.
	@git diff --quiet && git diff --cached --quiet || \
	  { echo "working tree is dirty — commit first, or use 'make image-local'"; exit 1; }
	@# Each platform's filesystem is scanned for private keys before anything is pushed.
	@rm -rf $(SCAN_DIR) && mkdir -p $(SCAN_DIR)
	@for p in $(subst $(comma), ,$(PLATFORMS)); do \
	  docker buildx build --builder $(BUILDER) --platform $$p $(RELEASE_ARGS) \
	    --output type=tar,dest=$(SCAN_DIR)/$${p##*/}.tar . || exit 1; \
	  ./scripts/scan-image-secrets.sh $(SCAN_DIR)/$${p##*/}.tar || exit 1; \
	done; rm -rf $(SCAN_DIR)
	@echo "$(TOKEN)" | docker login ghcr.io -u makearr --password-stdin
	@printf '%s' '$(call hub_field,Password)' | docker login docker.io -u '$(HUB_USER)' --password-stdin
	docker buildx build --builder $(BUILDER) --push --platform $(PLATFORMS) $(RELEASE_ARGS) \
	  -t $(IMAGE):$(NEXT_VERSION) -t $(IMAGE):latest \
	  -t $(HUB_IMAGE):$(NEXT_VERSION) -t $(HUB_IMAGE):latest .
	@NEXT_VERSION=$(NEXT_VERSION) MILESTONE="$(MILESTONE)" RELEASE_NOTES="$(RELEASE_NOTES)" ./scripts/release.sh

scan-image: ## Scan a local image for private keys: make scan-image [REF=ghcr.io/makearr/solarr:dev]
	@./scripts/scan-image-secrets.sh $(or $(REF),$(IMAGE):dev)

push:       ## Advance the build number, commit VERSION, push main to GitHub
	@git diff --cached --quiet || { echo "staged changes: commit them first"; exit 1; }
	@echo "$(BUILD_VERSION)" > VERSION
	@git add VERSION && git commit -q -m "Build $(BUILD_VERSION)" -m "Pushed to GitHub; every push advances the build number."
	@git push -q "https://x-access-token:$(TOKEN)@github.com/makearr/Solarr.git" HEAD:main || \
	  { git reset -q HEAD~1 && git checkout -- VERSION; echo "push failed; build number left at $(VERSION)"; exit 1; }
	@echo "pushed - version $(BUILD_VERSION)"

image-local: ## Local single-arch build: allows a dirty tree, advances only the build number, no push
	@test -f Dockerfile || { echo "no Dockerfile yet"; exit 1; }
	@# Falls back to the legacy builder when buildx is absent. The Dockerfile
	@# gives its platform args defaults for exactly this, so a machine without
	@# buildx can still build and run the image it is about to deploy.
	@if [ -n "$(HAS_BUILDX)" ]; then \
	  $(MAKE) --no-print-directory builder; \
	  docker buildx build --builder $(BUILDER) --load --platform linux/$(ARCH) \
	    --build-arg VERSION=$(BUILD_VERSION)$(DIRTY) --build-arg COMMIT=$(COMMIT) \
	    --build-arg BUILD_DATE=$(BUILD_DATE) -t $(IMAGE):dev . || exit 1; \
	else \
	  echo "buildx not found; building single-arch with the legacy builder"; \
	  docker build --build-arg VERSION=$(BUILD_VERSION)$(DIRTY) --build-arg COMMIT=$(COMMIT) \
	    --build-arg BUILD_DATE=$(BUILD_DATE) --build-arg TARGETARCH=$(ARCH) \
	    -t $(IMAGE):dev . || exit 1; \
	fi
	@# Answered 2026-09-25: a local image advances the build number too; only `make image` releases.
	@echo "$(BUILD_VERSION)" > VERSION
	@echo "built $(IMAGE):dev - version $(BUILD_VERSION)$(DIRTY)"

milestone:  ## Name the milestone shown beside the version: make milestone NAME="music, usenet"
	@test -n "$(NAME)" || { echo 'usage: make milestone NAME="name"'; exit 1; }
	@echo "$(NAME)" > MILESTONE
	@echo "milestone: $(NAME)"

builder:    ## Create the multi-arch builder if it does not exist
	@docker buildx inspect $(BUILDER) >/dev/null 2>&1 || { \
	  echo "creating the $(BUILDER) buildx builder"; \
	  docker buildx create --name $(BUILDER) --driver docker-container --bootstrap >/dev/null; }

binfmt:     ## Register qemu emulators for cross-arch builds (needed once per build host)
	@# The runtime stage's apk install (ADR-30's fpcalc/ffprobe) means a real multi-arch
	@# build emulates every non-native platform; without this the arm64 stage fails.
	@docker run --privileged --rm tonistiigi/binfmt --install all >/dev/null

clean-cache:
	-docker volume rm solarr-gopath solarr-gobuild solarr-npm
	-docker buildx rm $(BUILDER)
