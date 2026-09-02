# Multi-stage, multi-arch via buildx. Defaults let the legacy builder work too.
ARG BUILDPLATFORM=linux/amd64

# Pinned to the builder's platform: a RUN in the final stage would need qemu.
FROM --platform=$BUILDPLATFORM alpine:3 AS rootfs
RUN addgroup -g 1000 media \
 && adduser -D -u 1000 -G media -h /config -s /sbin/nologin media \
 && mkdir -p /skeleton/config /skeleton/data /skeleton/media \
 && chown -R 1000:1000 /skeleton

# Built once on the builder: JavaScript is identical for every target.
FROM --platform=$BUILDPLATFORM node:22-alpine AS web
WORKDIR /web
# Lockfile first, so a source-only change does not reinstall.
COPY web/package.json web/package-lock.json ./
RUN npm ci --no-audit --no-fund
COPY web/ ./
# outDir points at internal/webui/dist, where go:embed reaches it.
RUN npm run build

FROM --platform=$BUILDPLATFORM golang:1 AS build

ARG TARGETOS=linux
ARG TARGETARCH=amd64
ARG VERSION=0.0.0
ARG COMMIT=unknown
ARG BUILD_DATE=unknown
# A fork overrides this: documentation/licence-compliance.md.
ARG SOURCE_URL=https://github.com/makearr/MediaNexus

WORKDIR /src

# Dependencies first: this layer survives most rebuilds.
COPY go.mod go.sum ./
RUN go mod download

COPY . .
# Without this the image runs but serves no interface.
COPY --from=web /internal/webui/dist ./internal/webui/dist

RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -trimpath \
      -ldflags "-s -w \
        -X main.version=${VERSION} \
        -X main.commit=${COMMIT} \
        -X main.buildDate=${BUILD_DATE} \
        -X main.sourceURL=${SOURCE_URL}" \
      -o /out/medianexus ./cmd/medianexus

# Alpine rather than distroless: a self-hosted deployment gets debugged in place.
FROM alpine:3

# Copied rather than installed: a RUN here would need qemu. zoneinfo because air
# dates and TZ are timezone-dependent (architecture section 19.2).
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=build /usr/share/zoneinfo /usr/share/zoneinfo

# fpcalc and the tagging helper arrive with MediaTool at stage 3 (ADR-30).

COPY --from=build /out/medianexus /usr/local/bin/medianexus

# So ownership reads as a name and the process has a passwd entry.
COPY --from=rootfs /etc/passwd /etc/passwd
COPY --from=rootfs /etc/group /etc/group

# Owned here, or a VOLUME-created mount point belongs to root and the first
# `docker run` cannot write its own config.yml.
COPY --from=rootfs --chown=1000:1000 /skeleton/config /config
COPY --from=rootfs --chown=1000:1000 /skeleton/data /data
VOLUME ["/config"]
EXPOSE 8080

# Root only until internal/privilege drops to PUID:PGID, which it does before
# anything else. No ENV defaults for PUID/PGID/TZ, so a set variable means the
# operator set it. See documentation/container-runtime.md.

# A subcommand of the same binary, so the image needs no curl. start-period
# covers migrations on a large database.
HEALTHCHECK --interval=30s --timeout=5s --start-period=60s --retries=3 \
  CMD ["/usr/local/bin/medianexus", "healthcheck"]

ENTRYPOINT ["/usr/local/bin/medianexus"]
CMD ["serve"]
