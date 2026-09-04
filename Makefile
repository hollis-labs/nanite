.PHONY: build build-dev install dev clean test lint vuln generate-envelopes eval

# Build React SPA then embed in Go binary. Production build: NO build tags —
# the `devmode` tag MUST NOT be set here. internal/plugin/devmode compiles to
# HostDevSigningBypass=false, which is what keeps catalog + per-plugin
# signature verification unconditional in release binaries.
build: generate-envelopes build-ui
	go build -o nanite ./cmd/nanite

# Developer build with signing bypass enabled. Adds the `devmode` build tag so
# internal/plugin/devmode.HostDevSigningBypass == true. In this build:
#   - Catalog signatures are skipped.
#   - Per-plugin signatures are skipped iff user_settings.allow_unsigned_plugins
#     is true.
# Never ship this binary to users.
build-dev: generate-envelopes build-ui
	go build -tags devmode -o nanite ./cmd/nanite

# Install to ~/go/bin/ (used by MCP and Cerberus)
install: generate-envelopes build-ui
	go install ./cmd/nanite

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

# Run Go tests with the race detector — Tier 3 of docs/engineering/testing-workflow.md
# §3, ~9 min, what the nightly full-repo quality gate runs. The pre-push hook and
# ./scripts/check.sh both run the no-`-race` suite instead; this is not that.
test:
	go test -race ./...

# Full lint pass: go vet + uncapped golangci-lint + staticcheck + errcheck +
# govulncheck. All four external tools must resolve on PATH
# (`go install` via the canonical invocations — see .nanite/agents/backend.md).
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
		internal/server internal/api internal/memory internal/workflow \
		internal/sandbox internal/permission internal/secrets \
		internal/pathsafe internal/fsutil \
		2>/dev/null | grep -v 'safego\.Go' | grep -v 'safego\.Call' || \
		echo "(no bare goroutines in target packages)"

# Interaction-quality eval suite (I3 / CW-20260420-0028).
# Gated behind the "eval" build tag — not included in default `go test ./...`.
# Set NANITE_EVAL_LIVE=1 (with provider credentials) to run against a real model.
eval:
	go test -tags eval -race -count=1 -timeout=120s -v ./internal/eval/...

# Run with default settings
run: build
	./nanite serve --port 8090
