# Tool availability — 2026-04-11 sweep environment

Baseline of what is actually installed on the dev machine running this audit sweep, so future runs are reproducible. Per the task brief, missing tools are NOT installed on demand — absence is itself a finding.

## Environment

- **Machine:** macOS Darwin 24.6.0 (arm64)
- **Go:** `go version go1.26.1 darwin/arm64` (path `/opt/homebrew/bin/go`)
- **Module:** `github.com/hollis-labs/nanite`
- **Declared minimum Go version:** `.golangci.yml` says `run.go: "1.25"`, module itself uses 1.26.1

## Installed

| Tool | Version | Path | Used in sweep |
|---|---|---|---|
| `go` | 1.26.1 | `/opt/homebrew/bin/go` | steps 1–3, 8 |
| `golangci-lint` | 2.11.3 (built with go1.26.1, 2026-03-10) | `/opt/homebrew/bin/golangci-lint` | step 5 |
| `git` | (system) | — | step 8 (diff + revert) |

## Not installed

| Tool | Sweep step | Would-be install command | Gap |
|---|---|---|---|
| `staticcheck` | 4 | `go install honnef.co/go/tools/cmd/staticcheck@latest` | Standalone SA/ST/S/QF analyzer set. Bundled into golangci-lint v2 as the `staticcheck` linter, so the sweep captured 43 findings via step 5. A standalone run would likely produce a different (possibly larger) set, particularly under the SA1xxx series. |
| `govulncheck` | 6 | `go install golang.org/x/vuln/cmd/govulncheck@latest` | **No equivalent in golangci-lint.** This is the only tool that scans `go.sum` + code paths against the Go vulnerability database. Its absence from the sweep means this audit has **zero dependency-CVE signal**. |
| `errcheck` | 7 | `go install github.com/kisielk/errcheck@latest` | Bundled into golangci-lint v2 as the `errcheck` linter — 239 findings captured in step 5. Standalone errcheck is usually stricter (fewer ignored call sites) and would likely report more. |

## Claimed vs. actual

- `~/Projects-apps/nanite/.nanite/agents/reviewer-backend.md` §"How to run this review" lists `staticcheck`, `errcheck`, and `govulncheck` in the canonical tool set a backend reviewer should run. None of them exist on this machine.
- `~/Projects-apps/nanite/.nanite/agents/backend.md` §"Build & Run" lists only `gofmt`, `goimports`, `golangci-lint`, `go vet`, and `lefthook` as the backend tool set. This is the narrower canon and matches what's installed.
- The two files are inconsistent. Either the reviewer-context document is aspirational (reviewers are expected to install the extra trio themselves) or the backend context document is incomplete (the extra trio is supposed to be part of the devcontainer / lefthook set). Reconciling the two is a small but load-bearing follow-up because every future tooling sweep will hit the same gap.

## Reproducibility checklist

For a future run of this sweep to be apples-to-apples:

1. Go 1.26.1 (or later within the major).
2. golangci-lint 2.x with the project's committed `.golangci.yml`. Run with `--timeout 10m --max-issues-per-linter=0 --max-same-issues=0` to avoid the 50-issue-per-linter default cap.
3. staticcheck, govulncheck, errcheck installed (or explicitly documented as missing and the delta reconciled with the bundled golangci-lint linters).
4. All local `replace` sibling libs present at `../framework/libs/go-plugin`, `go-toolbroker`, `go-otel`, `go-providers`, and `../vanta-conduit`. Without them the module does not build.
5. Sandbox permission for `go test -race ./...` at full-tree scope. Long packages: `internal/mcp` ~66s, `pkg/provider` ~33s.
6. Clean working tree at sweep start (`go mod tidy` step mutates `go.mod` and needs to be revertible).
