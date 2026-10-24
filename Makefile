.PHONY: build build-dev install dev clean test lint vuln generate-envelopes eval package-release

# GONOPROXY=none (CW-20261024-0098): a developer machine often sets
# GOPRIVATE=github.com/hollis-labs/*, which also implies GONOPROXY and makes Go
# fetch every hollis-labs module straight from git. A module whose GitHub repo
# was renamed or removed (plugins-catalog) then fails with "Repository not
# found", although proxy.golang.org still serves it and CI, which sets no
# GOPRIVATE, builds fine. none = try the proxy first; GOPROXY's trailing
# `direct` still falls back to git on a 404/410, so genuinely private modules
# resolve as before, and GOPRIVATE still keeps them out of the checksum
# database. Cost: the paths of private modules reach proxy.golang.org first.
export GONOPROXY := none

# GOWORK=off: a shipped/deployed artifact must resolve dependencies from
# go.mod/go.sum only, never from a developer's ambient portfolio-wide
# go.work substituting a local sibling-repo checkout. Without this, `make
# build` silently depends on whatever state a completely unrelated local
# repo happens to be in — e.g. Cerberus's deploy failed with "no required
# module provides package github.com/hollis-labs/agentkit/artifact" because
# go.work pointed agentkit at a local checkout that had already removed
# that package on its own trunk, while go.mod's pinned agentkit v0.6.1
# (the real, published dependency) has it fine.
build: generate-envelopes build-ui
	GOWORK=off go build -ldflags "$(VERSION_LDFLAGS)" -o nanite ./cmd/nanite

# Developer build enables development-only agent profiles. Never publish it.
build-dev: generate-envelopes build-ui
	GOWORK=off go build -tags devmode -ldflags "$(VERSION_LDFLAGS)" -o nanite ./cmd/nanite

# Install to ~/go/bin/ (used by MCP and Cerberus)
install: generate-envelopes build-ui
	GOWORK=off go install -ldflags "$(VERSION_LDFLAGS)" ./cmd/nanite

# Product version comes from the nearest repo-level v* tag (pkg/pluginapi/* tags
# are the plugin API module's own versions and are excluded by --match). Untagged
# commits read like 0.3.0-beta.1-12-gabc123456789; no tags at all reads "dev".
GIT_VERSION := $(shell git describe --tags --match 'v*' --dirty 2>/dev/null | sed 's/^v//')
VERSION_PKG := github.com/hollis-labs/nanite/internal/version
VERSION_LDFLAGS := -X $(VERSION_PKG).GitSHA=$(shell git rev-parse --short=12 HEAD 2>/dev/null || echo dev)$(if $(GIT_VERSION), -X $(VERSION_PKG).Version=$(GIT_VERSION))

# Generate TypeScript types from the released go-envelopes module selected by
# go.mod. The module-owned exporter resolves embedded schemas and metadata.
generate-envelopes:
	node scripts/generate-envelope-types.mjs

# Check that generated envelope types are not stale (CI use)
check-envelopes:
	node scripts/generate-envelope-types.mjs --check
	node scripts/generate-plugin-imports.mjs --check

build-ui:
	cd ui && npm run build
	rm -rf internal/server/ui_dist
	mkdir -p internal/server/ui_dist
	cp -R ui/dist/. internal/server/ui_dist/

# Development
dev:
	@echo "Run in two terminals:"
	@echo "  Terminal 1: air"
	@echo "  Terminal 2: cd ui && npm run dev"

# Clean build artifacts
clean:
	rm -f nanite
	rm -rf ui/dist

# Run Go tests with the race detector — Tier 3: the full suite under `-race`,
# ~9 min, what the nightly full-repo quality gate runs. The pre-push hook and
# ./scripts/check.sh both run the no-`-race` suite instead; this is not that.
test:
	go test -race ./...

# Full lint pass: go vet + uncapped golangci-lint + staticcheck + errcheck +
# govulncheck. All four external tools must resolve on PATH
# (`go install` via each tool's canonical upstream invocation).
# Each step is run in sequence; the first failure aborts the pipeline.
lint:
	go vet ./...
	golangci-lint run --max-issues-per-linter=0 --max-same-issues=0
	staticcheck ./...
	errcheck ./...
	govulncheck ./...

# Standalone vulnerability scan (also included in `make lint`).
vuln:
	govulncheck ./...

# Phase 1 Wave 1 (2026-04-12): surface bare `go` statements in target
# packages that should adopt internal/safego. forbidigo cannot match the
# `go` keyword, so this is a grep-based sweep. Output lists files +
# line numbers; non-zero exit when matches are found (kept non-fatal by
# the leading `-` so `make lint` as a whole isn't gated on adoption).
.PHONY: lint-goroutines
lint-goroutines:
	@echo "==> internal/safego adoption sweep (bare 'go ' statements)"
	@-grep -rn --include='*.go' --exclude='*_test.go' -E '^\s+go [A-Za-z_][A-Za-z0-9_.]*\(' \
		internal/plugin internal/worker internal/mcp internal/service \
		internal/server internal/api internal/memory \
		internal/sandbox internal/permission internal/secrets \
		2>/dev/null | grep -v 'safego\.Go' | grep -v 'safego\.Call' || \
		echo "(no bare goroutines in target packages)"

# Interaction-quality eval suite (I3 / CW-20260420-0028).
# Gated behind the "eval" build tag — not included in default `go test ./...`.
# Set NANITE_EVAL_LIVE=1 (with provider credentials) to run against a real model.
eval:
	go test -tags eval -race -count=1 -timeout=120s -v ./internal/eval/...

# Run with default settings
run: build
	NANITE_WORKSPACE=dev ./nanite serve --port 8090

# Build distributable macOS release archives + checksums, for the
# hollis-labs/homebrew-tap Formula/nanite.rb. VERSION is the git tag the
# release workflow is packaging (e.g. v0.4.0) — passed in, never guessed here.
# The binary's Version is injected from VERSION (the tag, leading v stripped)
# along with GitSHA; see internal/version/version.go.
VERSION ?= dev
RELEASE_COMMIT := $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
RELEASE_LDFLAGS := -s -w -X $(VERSION_PKG).GitSHA=$(RELEASE_COMMIT) -X $(VERSION_PKG).Version=$(patsubst v%,%,$(VERSION))

package-release: generate-envelopes build-ui
	mkdir -p dist
	for target in darwin/amd64 darwin/arm64; do \
		os=$${target%/*}; \
		arch=$${target#*/}; \
		stage="dist/nanite_$(VERSION)_$${os}_$${arch}"; \
		archive="dist/nanite_$(VERSION)_$${os}_$${arch}.tar.gz"; \
		rm -rf "$$stage" "$$archive"; \
		mkdir -p "$$stage"; \
		CGO_ENABLED=0 GOOS="$$os" GOARCH="$$arch" GOWORK=off go build -trimpath -ldflags "$(RELEASE_LDFLAGS)" -o "$$stage/nanite" ./cmd/nanite; \
		cp README.md LICENSE "$$stage/"; \
		tar -C dist -czf "$$archive" "$$(basename "$$stage")"; \
	done
	cd dist && shasum -a 256 nanite_$(VERSION)_*.tar.gz > checksums.txt
