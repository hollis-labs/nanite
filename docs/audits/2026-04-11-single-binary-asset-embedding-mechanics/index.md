# Deep Review — Single Binary Asset Embedding Mechanics

**Date:** 2026-04-11
**Reviewer:** `nanite-reviewer-backend` (code-review + go roles, deep-review skill)
**Scope slug:** `single-binary-asset-embedding-mechanics`

## Scope

**Scope string:** `single-binary-asset-embedding-mechanics` (INDEX.md item 42). Audit of the MECHANICS of `//go:embed` usage across the Nanite codebase — embed directive correctness, extraction atomicity, binary size impact, cold-start performance, security posture, and fresh-checkout build behavior.

**Distinct from:** `assets-framework-content-correctness` (INDEX.md item 14), which audits the CONTENT of the embedded framework files (are the roles/skills/templates correct?). This audit treats the content as opaque bytes and focuses on the embedding and extraction machinery.

### Files read in full

- `internal/assets/framework.go` — framework embed directive and `ExtractTo` implementation
- `internal/assets/framework_test.go` — test coverage for embed accessors and extraction
- `internal/server/spa.go` — SPA embed directive and serving logic
- `internal/store/store.go:L1-30` — migration embed directive
- `internal/skill/builtin/embed.go` — builtin skill embed directive
- `internal/agent/builtin/embed.go` — builtin agent embed directive
- `internal/plugin/scaffold/scaffold.go:L1-30` — scaffold template embed directive
- `internal/envelope/contracts_test.go:L1-20` — test-only schema embed directive
- `internal/service/install/install.go` — install service `InstallHome` and `InstallProject`
- `internal/service/install/scaffold.go` — scaffold implementation using `assets.File()`
- `internal/service/install/install_test.go` — install service tests
- `cmd/nanite/install_cmd.go` — CLI entry point for `nanite install`
- `cmd/nanite/main.go:L1-80` — binary entrypoint, startup path
- `Makefile` — build sequence
- `.gitignore` — tracked vs. gitignored embed source files

### Files sampled

- `internal/assets/framework/` — full directory listing (76 files), size measurements, file type inventory
- `internal/server/ui_dist/` — directory listing, git-tracked vs. gitignored files, size measurements

### Files NOT read (explicit blind spots)

- `internal/assets/framework/` content files (roles, skills, docs, templates, vendor) — content correctness is a separate scope
- `internal/service/install/state.go`, `resume.go`, `rollback.go`, `migrate.go` — install state machine covered by prior `2026-04-10-installer` audit
- Runtime behavior under memory pressure — embed.FS memory mapping behavior is documented by Go stdlib, not verified empirically

## Methodology

1. **Scope parsing:** subsystem review covering all `//go:embed` directives in the codebase, their extraction paths, and their impact on binary size and startup.
2. **Category enumeration:** Security (tamper resistance, secret inclusion), Correctness (embed patterns, fresh-clone build), Error handling (extraction failure modes), Build hygiene (binary size, unnecessary inclusions). Concurrency, idioms, antipatterns, test quality, and standards tooling **deferred** — not relevant to this narrow embed-mechanics scope.
3. **Evidence gathering:** read all embed directives, measured directory sizes, built the binary to measure total size, inspected `.gitignore` for tracked vs. untracked embed sources, verified cold-start code path does not access embeds.
4. **Tooling deferred:** `go vet`, `golangci-lint`, `go test -race`, `govulncheck` — narrow scope does not warrant full tooling pass. Recommend running these as part of a `tooling-and-tests` follow-up.

## Findings

### By severity

**Critical (0)**
- _none_

**High (0)**
- _none_

**Medium (2)**
- [01 — Fresh `git clone` + `go build` produces a broken SPA embed](01-medium-fresh-clone-build-fails.md)
- [02 — Framework extraction is direct-write, not atomic](02-medium-extraction-not-atomic.md)

**Low (2)**
- [03 — Vendored PNG images embedded via `all:framework`](03-low-vendored-png-in-embed.md)
- [04 — shadcn-ui evals.json embedded but unused at runtime](04-low-shadcn-evals-json-embedded.md)

**Info (2)**
- [05 — Binary size and embedded asset inventory](05-info-binary-size-and-embed-inventory.md)
- [06 — Embed security model and cold-start performance are sound](06-info-security-and-cold-start.md)

### By topic

**Embed directive correctness**
- [01 — Fresh `git clone` + `go build` produces a broken SPA embed](01-medium-fresh-clone-build-fails.md)
- [03 — Vendored PNG images embedded via `all:framework`](03-low-vendored-png-in-embed.md)
- [04 — shadcn-ui evals.json embedded but unused at runtime](04-low-shadcn-evals-json-embedded.md)

**Extraction mechanics**
- [02 — Framework extraction is direct-write, not atomic](02-medium-extraction-not-atomic.md)

**Binary size**
- [05 — Binary size and embedded asset inventory](05-info-binary-size-and-embed-inventory.md)

**Security and cold-start**
- [06 — Embed security model and cold-start performance are sound](06-info-security-and-cold-start.md)

## Recommended next steps

1. **Fix fresh-clone build** (finding 01) — commit a placeholder `index.html` or add a build-time check. This is the most user-visible issue.
2. **Add version marker to `ExtractTo`** (finding 02) — low effort, makes partial extraction self-healing.
3. **Prune vendor tree** (findings 03, 04) — next time the vendor content is updated, remove non-essential files (PNGs, evals.json, openai.yml).
4. **Follow-up scope: `assets-framework-content-correctness`** — audits whether the embedded roles, skills, templates, and docs are correct and complete. Distinct from this mechanics audit.
5. **Follow-up scope: `tooling-and-tests`** — full `go vet`, `golangci-lint`, `go test -race`, `govulncheck` pass. Deferred from this narrow scope.

## Known issues skipped

- **`.agentrc/` vs `.nanite/` path drift** — tracked in `reviewer-backend.md` pre-existing known issues item 4.
- **Installer atomicity at the project level** — the `2026-04-10-installer` audit covered `InstallProject`'s state machine. This audit only flagged the gap at the `InstallHome`/`ExtractTo` layer which was explicitly out of scope in that prior audit.

## Noticed but out of scope

- **`internal/server/spa.go:13` — placeholder HTML says "Mentat Chat"** — the `placeholderHTML` constant still uses the pre-rebrand product name. Not an embed mechanics issue; suggest a `branding-consistency` follow-up scope.
- **`internal/server/spa.go:L40-53` — `fs.Sub` called on every request** — the `handleSPA` function creates a new `fs.Sub` on every HTTP request rather than caching it once at server construction time. Likely negligible overhead (fs.Sub is cheap on embed.FS) but worth noting for a `http-handler-optimization` follow-up.
- **`internal/envelope/contracts_test.go` schema embed** — the test-only embed pattern is clean and correct, but the 96KB of JSON schemas are not validated against the actual Go types at build time. A `schema-contract-drift` scope could verify this.
