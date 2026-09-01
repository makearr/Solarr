# Toolchains run in Docker — nothing needs installing locally.
# Override the images to pin different versions.
GO_IMAGE    ?= golang:1.23
NODE_IMAGE  ?= node:22-alpine
SQLC_IMAGE  ?= sqlc/sqlc:latest
LINT_IMAGE  ?= golangci/golangci-lint:latest

UID         := $(shell id -u)
GID         := $(shell id -g)
MOUNT       := -v $(CURDIR):/src -w /src
CACHE       := -v medianexus-gomod:/go/pkg/mod -v medianexus-gobuild:/.cache/go-build
GOENV       := -e GOCACHE=/.cache/go-build -e CGO_ENABLED=0 -e HOME=/tmp
RUN_GO      := docker run --rm -u $(UID):$(GID) $(MOUNT) $(CACHE) $(GOENV) $(GO_IMAGE)
RUN_NODE    := docker run --rm -u $(UID):$(GID) $(MOUNT)/web -w /src/web \
                 -v medianexus-npm:/tmp/.npm -e npm_config_cache=/tmp/.npm $(NODE_IMAGE)

.PHONY: help verify build test race bench lint vet fmt fmt-check sqlc sqlc-check \
        api web web-dev image clean-cache

help:  ## List targets
	@grep -hE '^[a-z-]+:.*?## ' $(MAKEFILE_LIST) | awk -F':.*## ' '{printf "  %-14s %s\n", $$1, $$2}'

## ---- the gate: everything that must pass before a subsystem is "done" ----
verify: fmt-check vet lint test sqlc-check  ## Full check, as CI runs it

build:      ## Build the binary (CGO_ENABLED=0, per ADR-30)
	$(RUN_GO) go build -trimpath -o /src/medianexus ./cmd/medianexus

test:       ## Unit tests
	$(RUN_GO) go test -count=1 ./...

race:       ## Tests with the race detector (needs CGO)
	docker run --rm -u $(UID):$(GID) $(MOUNT) $(CACHE) -e GOCACHE=/.cache/go-build -e HOME=/tmp \
	  $(GO_IMAGE) go test -race -count=1 ./...

bench:      ## Benchmarks — ADR-28 performance targets
	$(RUN_GO) go test -run=^$$ -bench=. -benchmem ./...

vet:
	$(RUN_GO) go vet ./...

lint:
	docker run --rm -u $(UID):$(GID) $(MOUNT) $(CACHE) -e HOME=/tmp $(LINT_IMAGE) golangci-lint run

fmt:        ## Format in place
	$(RUN_GO) gofmt -w -s .

fmt-check:
	@out=$$($(RUN_GO) gofmt -l -s .); \
	 if [ -n "$$out" ]; then echo "unformatted:"; echo "$$out"; exit 1; fi

## ---- generated code: committed, never hand-edited (ADR-31) ----
sqlc:       ## Regenerate from queries/*.sql
	docker run --rm -u $(UID):$(GID) $(MOUNT) $(SQLC_IMAGE) generate

sqlc-check: ## Fail if generated SQL code is stale
	docker run --rm -u $(UID):$(GID) $(MOUNT) $(SQLC_IMAGE) diff

api:        ## Regenerate Go server + TS client from api/openapi.yaml
	@echo "TODO: wire oapi-codegen and openapi-typescript once api/openapi.yaml exists"

## ---- web ----
web:        ## Production build, embedded via embed.FS
	$(RUN_NODE) npm ci && $(RUN_NODE) npm run build

web-dev:    ## Vite dev server on :5173, proxying the API
	docker run --rm -it -u $(UID):$(GID) $(MOUNT)/web -w /src/web -p 5173:5173 \
	  -v medianexus-npm:/tmp/.npm -e npm_config_cache=/tmp/.npm $(NODE_IMAGE) npm run dev

image:      ## Build the container image
	docker build -t medianexus:$$(cat VERSION) .

clean-cache:
	-docker volume rm medianexus-gomod medianexus-gobuild medianexus-npm
