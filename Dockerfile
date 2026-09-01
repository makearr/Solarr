# Multi-stage: the build image never ships. Multi-arch via buildx, so the build
# stage runs natively on the builder and cross-compiles, which is fast because
# CGO is off (ADR-30) and cross-compiling pure Go is free.
# BUILDPLATFORM, TARGETOS and TARGETARCH are supplied automatically by buildx.
# They are declared with defaults so the legacy builder -- which does not set
# them -- still produces a working native image rather than failing to parse
# the first line.
ARG BUILDPLATFORM=linux/amd64

# The interface is compiled first, on the builder's own architecture: it is
# JavaScript, so the result is identical for every target and building it once
# per platform would double release time for no difference in output.
FROM --platform=$BUILDPLATFORM node:22-alpine AS web
WORKDIR /web
# The lockfile alone first, so a source-only change does not reinstall.
COPY web/package.json web/package-lock.json ./
RUN npm ci --no-audit --no-fund
COPY web/ ./
# outDir points outside web/, so the bundle lands where the Go package embeds it.
RUN npm run build

FROM --platform=$BUILDPLATFORM golang:1 AS build

ARG TARGETOS=linux
ARG TARGETARCH=amd64
ARG VERSION=0.0.0
ARG COMMIT=unknown
ARG BUILD_DATE=unknown
# A fork overrides this so the About page links to *its* source, not upstream.
# Pointing at upstream from a modified build is the misrepresentation AGPL
# section 13 exists to prevent (documentation/licence-compliance.md).
ARG SOURCE_URL=https://github.com/makearr/MediaNexus

WORKDIR /src

# Dependencies first: they change far less often than the source, so this layer
# survives most rebuilds.
COPY go.mod go.sum ./
RUN go mod download

COPY . .
# The compiled interface, which go:embed folds into the binary. Without this the
# image would build and run but answer every page with "not in this binary".
COPY --from=web /internal/webui/dist ./internal/webui/dist

RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -trimpath \
      -ldflags "-s -w \
        -X main.version=${VERSION} \
        -X main.commit=${COMMIT} \
        -X main.buildDate=${BUILD_DATE} \
        -X main.sourceURL=${SOURCE_URL}" \
      -o /out/medianexus ./cmd/medianexus

# Alpine rather than distroless: a self-hosted deployment gets debugged on the
# machine it runs on, and a shell is worth the few megabytes. It also runs under
# any --user, which is how a NAS matches the uid that owns its volumes.
FROM alpine:3

# Certificates and timezone data are copied from the build stage rather than
# installed with apk. A RUN in this stage would execute a foreign-architecture
# binary, which needs qemu binfmt emulation on the builder -- slow, and a hidden
# requirement that only shows up when someone tries to release. Copying files
# needs neither. ca-certificates for HTTPS to indexers and metadata providers,
# zoneinfo because air dates are timezone-dependent (architecture section 19.2).
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=build /usr/share/zoneinfo /usr/share/zoneinfo

# ADR-30 note: fpcalc and the tagging helper belong in this image, not the
# binary. They are added when MediaTool lands with music (stage 3) rather than
# now, so the image does not carry tools nothing calls.

COPY --from=build /out/medianexus /usr/local/bin/medianexus

# One volume holds config, databases, backups and cache, so a default
# deployment mounts a single path (architecture section 20.2).
VOLUME ["/config"]
EXPOSE 8080

# 65532 matches the conventional non-root uid. Override with --user to match
# whoever owns the mounted volume; startup reports the mismatch if it cannot
# write (architecture section 14.3).
USER 65532:65532

# A subcommand of the same static binary, so the image needs no curl or wget.
# start-period covers migrations on a large database.
HEALTHCHECK --interval=30s --timeout=5s --start-period=60s --retries=3 \
  CMD ["/usr/local/bin/medianexus", "healthcheck"]

ENTRYPOINT ["/usr/local/bin/medianexus"]
CMD ["serve"]
