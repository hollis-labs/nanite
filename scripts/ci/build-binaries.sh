#!/usr/bin/env bash
# build-binaries.sh: build the release-shaped Nanite binaries for CI (CW-20261024-0120).
# Produces, under ./dist: nanite_<version>_<os>_<arch>.tar.gz for each target (a bare `nanite`
# binary, deploy.yaml, README.md, LICENSE), build-info.json (schema fleet-build-info/v1) and
# SHA256SUMS. Pure Go cross-build: CGO_ENABLED=0. No tags are created or required.
#
#   scripts/ci/build-binaries.sh [targets...]      default: linux/amd64 darwin/arm64
#   SKIP_UI=1 skips `make generate-envelopes build-ui` (the UI must already be embedded)
set -euo pipefail

cd "$(dirname "$0")/../.."
TARGETS=("$@"); [ ${#TARGETS[@]} -gt 0 ] || TARGETS=(linux/amd64 darwin/arm64)
APP=nanite
PKG=github.com/hollis-labs/nanite/internal/version

commit=$(git rev-parse HEAD)
sha12=${commit:0:12}
# Nearest repo v* tag (pkg/pluginapi/* tags excluded), as the Makefile does; "dev" if none.
version=$(git describe --tags --match 'v*' 2>/dev/null | sed 's/^v//' || true)
[ -n "$version" ] || version=dev
go_version=$(go version | awk '{print $3}')
migrations_max=$(ls internal/store/migrations/*.sql | sed 's#.*/##; s/_.*//' | sort -n | tail -1 | sed 's/^0*//')
built_at=$(date -u +%Y-%m-%dT%H:%M:%SZ)

[ -f deploy/deploy.yaml ] || { echo "build-binaries: deploy/deploy.yaml is missing" >&2; exit 1; }

if [ "${SKIP_UI:-0}" != 1 ]; then
  make generate-envelopes build-ui
fi

# Refuse a build that cannot be CGO-free: any package with cgo files is a failure.
cgo=$(GOWORK=off go list -f '{{if .CgoFiles}}{{.ImportPath}}{{end}}' ./... 2>/dev/null | grep -v '^$' || true)
if [ -n "$cgo" ]; then echo "build-binaries: packages with cgo files:" >&2; echo "$cgo" >&2; exit 1; fi

rm -rf dist; mkdir -p dist
targets_json=""
for t in "${TARGETS[@]}"; do
  os=${t%/*}; arch=${t#*/}
  stage="dist/${APP}_${version}_${os}_${arch}"
  mkdir -p "$stage"
  CGO_ENABLED=0 GOOS=$os GOARCH=$arch GOWORK=off go build -trimpath \
    -ldflags "-s -w -X $PKG.GitSHA=$sha12 -X $PKG.Version=$version" -o "$stage/$APP" ./cmd/nanite
  cp deploy/deploy.yaml README.md LICENSE "$stage/"
  # The binary must be CGO-free and built for the requested target.
  info=$(go version -m "$stage/$APP")
  echo "$info" | grep -q 'CGO_ENABLED=0' || { echo "build-binaries: $t binary was not built with CGO_ENABLED=0" >&2; exit 1; }
  tar -C dist -czf "$stage.tar.gz" "$(basename "$stage")"
  rm -rf "$stage"
  targets_json="${targets_json:+$targets_json,}\"$t\""
done

cat > dist/build-info.json <<JSON
{"schema":"fleet-build-info/v1","app":"$APP","commit":"$commit","sha12":"$sha12","version":"$version","go_version":"$go_version","built_at":"$built_at","targets":[$targets_json],"migrations_max":${migrations_max:-0}}
JSON

(cd dist && if command -v sha256sum >/dev/null; then sha256sum ./*.tar.gz build-info.json; else shasum -a 256 ./*.tar.gz build-info.json; fi | sed 's# \./# #' > SHA256SUMS)
echo "built $APP $version ($sha12), migrations_max=${migrations_max:-0}:"; ls -1 dist
