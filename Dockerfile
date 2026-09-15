# One build stage, because the UI build needs Go.
#
# ui/package.json's `prebuild` runs scripts/generate-envelope-types.mjs, and
# that spawns `go run github.com/hollis-labs/go-envelopes/cmd/envelopes-export`
# (scripts/lib/envelope-catalog.mjs) to read the envelope catalog. A node-only
# stage cannot satisfy it — the previous split ui-build stage failed with
# "Cannot find module '/app/scripts/generate-plugin-imports.mjs'" because the
# repo-root scripts/ was never copied into it, and would have failed again on
# the missing Go toolchain if it had been.
#
# --platform=$BUILDPLATFORM keeps the toolchain on the machine doing the
# building. Emulated Go does not merely run slowly, it crashes: under QEMU on an
# arm64 host, `go mod download` panics with "growslice: len out of range" inside
# crypto/x509 while parsing system root certificates. Verified 2026-09-15 —
# identical program, native arm64 parses 119 roots and fetches fine, emulated
# amd64 panics in the runtime. The Go build is CGO-free and the UI output is
# JavaScript, so both cross-compile cleanly; only the runtime stage is target-arch.
FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS build
ARG TARGETOS
ARG TARGETARCH
RUN apk add --no-cache nodejs npm
WORKDIR /app

# Module cache first, so a source-only change does not refetch.
COPY go.mod go.sum ./
RUN go mod download

# Then node_modules, for the same reason.
COPY ui/package.json ui/package-lock.json* ./ui/
RUN cd ui && npm ci

COPY . .

# prebuild regenerates ui/src/generated/*; it needs both toolchains and the
# module cache above.
RUN cd ui && npm run build

# The binary embeds the UI from this directory.
RUN cp -r ui/dist/. internal/server/ui_dist/

RUN CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH:-amd64} \
    go build -o nanite ./cmd/nanite

# Runtime: target architecture, nothing but the binary.
FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata
RUN adduser -D -u 1000 nanite
WORKDIR /app
COPY --from=build /app/nanite .
RUN mkdir -p /data && chown nanite:nanite /data
USER nanite
EXPOSE 8090
ENTRYPOINT ["./nanite", "serve", "-db", "/data/nanite.db"]
