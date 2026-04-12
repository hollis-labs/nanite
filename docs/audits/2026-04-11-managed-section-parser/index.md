# Managed-section parser audit — 2026-04-11

## Scope

**Scope parameter:** `managed-section-parser`

**Interpretation:** A narrow subsystem review of `internal/agent/managed_section.go` — the writer/reader/remover trio for Nanite's `<!-- nanite:start -->` / `<!-- nanite:end -->` managed block protocol. The trio is on the hot path of every adapter plugin's `SyncProjectRoot` call and of the installer's `UpdateCLAUDEmd`. The subsystem is small (~185 LoC) but cross-cuts six callers and is the primary integrity gate for user-editable markdown files Nanite modifies.

**Read in full:**
- `internal/agent/managed_section.go` — the parser / writer / remover (185 lines).
- `internal/agent/managed_section_test.go` — the existing tests (287 lines).
- `internal/plugin/builtin/adapter-claude/plugin.go` — caller.
- `internal/plugin/builtin/adapter-codex/plugin.go` — caller.
- `internal/plugin/builtin/adapter-gemini/plugin.go` — caller.
- `internal/plugin/builtin/adapter-opencode/plugin.go` — caller.
- `internal/service/install/claudemd.go` — caller (`UpdateCLAUDEmd`).
- `internal/service/install/adapter_cleanup.go` — caller (`cleanupRemovedAdapters`).
- `docs/audits/2026-04-10-installer/03-high-managed-section-end-marker-confusion.md` — sibling audit already covering the file-content marker issue.
- `docs/audits/2026-04-10-installer/02-high-symlink-following-on-adapter-target-files.md` — sibling audit covering symlink-following on the same file set.

**Scope-label vs. real layout discovery:** The task prompt mentioned `agent.ParseMDFile` (adapter-claude/plugin.go:L114) as having a test-coverage gap to address in this scope. Inspection shows `agent.ParseMDFile` is the frontmatter-based **agent definition parser** (`internal/agent/parser.go`), not the managed-section parser. The two are unrelated — one parses per-agent `.claude/agents/*.md` files into `agent.Definition` structs; the other writes the shared managed block into top-level CLAUDE.md / AGENTS.md / GEMINI.md / OPENCODE.md files. This audit covers the managed-section trio only. The `ParseMDFile` coverage gap is noted in "Noticed but out of scope" for a future pass.

**Not checked (blind spots disclosed):**
- The agent definition parser (`internal/agent/parser.go`, `discovery.go`). Different subsystem, different trust boundary, different failure modes.
- `adapter-nanite-native/plugin.go` — listed as a caller in the task prompt but does not invoke any managed-section function. Grep confirms zero call sites. Excluded from the review.
- Filesystem-level race conditions under concurrent session-prep from multiple sibling processes writing the same `projectDir/FILE.md`. Noted as a gap in finding 02 (gap group G) but not exhaustively modeled — the current code has no locking.
- Windows-specific file I/O semantics (`os.Rename` atomicity on Windows, `os.WriteFile` truncate-then-write behavior on ReFS, case-insensitive path matching against marker strings). Not tested; out of scope for a POSIX-first review.

## Methodology

Applied categories from the Go review rubric: Security, Concurrency, Error Handling, Idioms, Antipatterns, Test Quality. Did not apply: Memory & Resources (no resource acquisition beyond a file handle per call, no goroutines), Standards & Tooling (deferred — see below).

**Cross-audit grounding.** Read `docs/audits/INDEX.md` and the two adjacent installer findings (02 and 03) before starting. Installer finding 03 already covers the *file-content-contains-markers* case in depth, so this audit cross-references it rather than re-flagging. Finding 01 of this audit is the *caller-supplied-content-contains-markers* case, which is the symmetric bug on the other trust boundary and is not covered by installer-03.

**Tooling evidence run.** `go vet ./internal/agent/` clean. `go test -race ./internal/agent/` clean (cached). Did not run `staticcheck`, `errcheck`, `golangci-lint`, `govulncheck` — narrow-scope review, deferred to the repo-wide tooling sweep (`docs/audits/2026-04-11-whole-repo-tooling-and-tests-sweep/`).

**Traversal strategy.** Read the parser file linearly. For each exported function, walked every branch by hand, looked for (a) inputs that produce a different output path than the happy case, (b) inputs that could corrupt the file, (c) inputs that could confuse the caller. Then grep'd for every caller, read the caller call-site with ±10 lines of context, and checked whether the caller makes assumptions about the return value that the function does not guarantee. Finally, compared the parser's behavior against the bug described in installer-03 to determine which findings are inherited and which are new.

## Findings

### By severity

**Critical (0)**
- _none_

**High (2)**
- [01 — Content payload can inject marker strings and corrupt on next write](01-high-content-payload-can-inject-marker-strings.md)
- [02 — Test coverage does not exercise any of the hard cases callers depend on](02-high-test-coverage-enumeration.md)

**Medium (3)**
- [03 — Non-atomic write leaves torn file on crash](03-medium-non-atomic-write-torn-file-on-crash.md)
- [04 — `ReadManagedSection` silently strips notice text and conflates empty/truncated states](04-medium-read-managed-section-silent-notice-stripping.md)
- [05 — Second managed block never reclaimed; adapter cleanup leaves dead content](05-medium-second-managed-block-never-reclaimed.md)

**Low (4)**
- [06.1 — Partial-line start marker handled implicitly](06-low-info-observations.md#61-low-partial-line-start-marker-handled-implicitly)
- [06.2 — `before` trailing whitespace not trimmed in `WriteManagedSection`](06-low-info-observations.md#62-low-before-trailing-whitespace-not-trimmed-in-writemanagedsection)
- [06.3 — `strings.Index` on large files is unmeasured](06-low-info-observations.md#63-low-stringsindex-on-large-files-linear-scan-cost-is-bounded-but-unmeasured)
- [06.4 — File mode not preserved on write](06-low-info-observations.md#64-low-no-file-mode-preservation-on-write)

**Info (4)**
- [06.5 — No symlink handling (cross-ref installer-02)](06-low-info-observations.md#65-info-no-symlink-handling--inherited-from-installer-audit-02)
- [06.6 — Praise: exported functions are well-documented](06-low-info-observations.md#66-info-praise--the-five-public-functions-are-well-documented)
- [06.7 — Praise: sentinel strings are well-chosen](06-low-info-observations.md#67-info-package-level-constants-are-well-chosen-sentinel-strings)
- [06.8 — Praise: `RemoveManagedSection`'s three-tuple return shape](06-low-info-observations.md#68-info-removemanagedsections-three-tuple-return-is-the-right-shape)

### By topic

**Security / trust boundary**
- [01 — Content payload can inject marker strings and corrupt on next write](01-high-content-payload-can-inject-marker-strings.md)
- [06.5 — No symlink handling](06-low-info-observations.md#65-info-no-symlink-handling--inherited-from-installer-audit-02)
- Cross-ref: `docs/audits/2026-04-10-installer/03-high-managed-section-end-marker-confusion.md` — file-content marker confusion (not re-flagged here).
- Cross-ref: `docs/audits/2026-04-10-installer/02-high-symlink-following-on-adapter-target-files.md` — symlink following on the same file set (not re-flagged here).

**Correctness / idempotency**
- [05 — Second managed block never reclaimed](05-medium-second-managed-block-never-reclaimed.md)
- [04 — `ReadManagedSection` state ambiguity and notice stripping](04-medium-read-managed-section-silent-notice-stripping.md)
- [06.1 — Partial-line start marker](06-low-info-observations.md#61-low-partial-line-start-marker-handled-implicitly)
- [06.2 — `before` trailing whitespace](06-low-info-observations.md#62-low-before-trailing-whitespace-not-trimmed-in-writemanagedsection)

**Memory & resources / durability**
- [03 — Non-atomic write leaves torn file on crash](03-medium-non-atomic-write-torn-file-on-crash.md)
- [06.4 — File mode not preserved](06-low-info-observations.md#64-low-no-file-mode-preservation-on-write)
- [06.3 — `strings.Index` cost is unmeasured](06-low-info-observations.md#63-low-stringsindex-on-large-files-linear-scan-cost-is-bounded-but-unmeasured)

**Test quality**
- [02 — Test coverage gap enumeration](02-high-test-coverage-enumeration.md)

**Idioms / documentation**
- [06.6 — Exported functions are well-documented](06-low-info-observations.md#66-info-praise--the-five-public-functions-are-well-documented)
- [06.7 — Sentinel strings are well-chosen](06-low-info-observations.md#67-info-package-level-constants-are-well-chosen-sentinel-strings)
- [06.8 — `RemoveManagedSection` return shape](06-low-info-observations.md#68-info-removemanagedsections-three-tuple-return-is-the-right-shape)

## Recommended next steps

Prioritized by technical severity and shared-primitive leverage (no delivery timing implied):

1. **Reject content containing marker substrings in `WriteManagedSection` (finding 01).** One-line guard at the top of the function. Prevents the caller-supplied-content corruption path. This is orthogonal to and necessary alongside installer-03's recommended file-content-marker count check.
2. **Pair with installer-03's marker-count guard.** The two fixes together give full trust-boundary coverage: installer-03 catches pre-existing file corruption, finding 01 catches content-originated corruption. Neither subsumes the other.
3. **Add atomic-write temp-file-then-rename (finding 03).** Small change, low risk, closes a catastrophic-corruption path on crash or interrupt. Should be a shared helper — the same pattern is needed in any future file writer Nanite adds.
4. **Fill the test coverage enumerated in finding 02.** Not a fix, but a safety net for every future refactor on this file. A `FuzzWriteReadRoundTrip` is particularly cheap and would surface finding 01 on first run.
5. **Decide the product answer for the two-pairs case (finding 05)** before implementing installer-03's reject-on-multi-pair guard, so the user experience is coherent when the guard fires.
6. **Re-scope: tighten `ReadManagedSection` (finding 04)** when the first production caller is introduced. Until then, the current behavior is consistent with the docstring; the latent gaps can be deferred.

**Follow-up review passes suggested:**
- **`agent-definition-parser`** — `internal/agent/parser.go` and `discovery.go`. Not covered here. The task prompt flagged a test coverage gap on `ParseMDFile` (adapter-claude/plugin.go:L114) that turned out to reference this separate parser. Scope: the frontmatter / YAML parser trust boundary, its call sites, and its interaction with `adapter-claude` and `adapter-codex` discovery walks.
- **`shared-pathsafe-primitive`** — meta-pass to design the `internal/pathsafe.ResolveUnder(root, userPath)` helper proposed in the cross-cutting themes section of `docs/audits/INDEX.md`. Once merged, sweep the file I/O sites in this audit (and installer-02, and the dev-tools findings) to adopt it.

## Known issues skipped

Issues that are already tracked elsewhere and deliberately not re-flagged:

- **Installer-03 (`docs/audits/2026-04-10-installer/03-high-managed-section-end-marker-confusion.md`)** — file-content containing marker strings. Cross-referenced from findings 01, 02, 05. Not re-flagged. The fix proposed there (count markers + add a fingerprint to the marker format) is necessary and should be implemented alongside finding 01.
- **Installer-02 (`docs/audits/2026-04-10-installer/02-high-symlink-following-on-adapter-target-files.md`)** — symlink following on adapter target files. Noted in observation 06.5 as a cross-ref. Same file I/O layer, same fix. Not re-flagged.
- **Pre-existing known issues from `.nanite/agents/plugin-dev.md` §Known Limitations** — none overlap with this scope.

## Noticed but out of scope

- **`agent.ParseMDFile` test coverage gap** (`internal/agent/parser.go`, called from `adapter-claude/plugin.go:L114`). The installer handoff flagged this but it is a different parser than the managed-section trio. It reads YAML frontmatter from individual agent definition files and produces `agent.Definition` structs. Its error paths (malformed frontmatter, missing slug, filename-fallback ambiguity) are a separate review scope. Follow-up: `agent-definition-parser` audit.
- **`RemoveAgentrcSection` in `internal/service/install/claudemd.go:L16-75`** — the legacy `## agentrc` heading stripper. Related family of issues to the managed-section parser (finds the first heading, assumes one section, trims whitespace with a fragile regex). Installer-03 mentions it briefly in its References section. Follow-up: fold into `agent-definition-parser` audit or a narrow `legacy-agentrc-migration` sweep.
- **Adapter plugin profile validation gap.** None of the adapter plugins validate that `store.AgentProfile.Name` / `.Description` are free of HTML comment markers, script injection, markdown injection, or length bounds before feeding them into the managed-section content. This cuts across all four callers and is a trust-boundary issue at the profile CRUD layer, not the parser layer. Finding 01 fixes the parser-side of this, but the profile-layer side deserves its own pass. Follow-up: `agent-profile-validation` scope, covering `internal/store/agents.go`, `internal/api/agents.go`, and the four `SyncProjectRoot` implementations.
- **Placeholder content leakage to other locales.** The four adapter plugins share a near-identical `placeholderContent` constant but are not factored into a shared helper. Style-level, not correctness. Follow-up: `adapter-plugin-shared-helpers` refactor pass.
- **`cleanupRemovedAdapters` partial-failure recovery.** In `internal/service/install/adapter_cleanup.go:L42-55`, if the first adapter's `RemoveManagedSection` succeeds but the second adapter's fails, the first adapter's file has been modified and the second has not. The returned error carries no hint about which adapters were partially processed. Medium-severity at most; out of scope for this pass because the audit boundary is the parser, not the install service. Follow-up: re-surface in any future `install-service-rollback` scope.
- **`UpdateCLAUDEmd` double-write**: `internal/service/install/claudemd.go:L127-138` writes the cleaned content back, then immediately calls `WriteManagedSection` which reads the same file and writes again. Two sequential writes instead of one combined write. Inefficient but correct; would benefit from a `WriteManagedSectionWithCleanup` helper. Style/perf, not correctness. Follow-up: style sweep of the install service.

## Proposed theme entries for `docs/audits/INDEX.md` Cross-cutting themes

To be folded into the INDEX by the orchestrator:

- **Content-channel vs. file-channel trust boundaries.** The managed-section parser has two independent trust boundaries — the file content (addressed by installer-03) and the caller-supplied content (addressed by finding 01 of this audit). Every parser that wraps untrusted data in a structured format has both. Future reviewers: for any `Wrap(innerContent) -> wrappedFormat` function, check *both* whether `innerContent` can contain wrapper-format sentinels AND whether the wrapper-format can be confused by adjacent user data. Two distinct classes, both require mitigation.
- **Non-atomic file writes are repo-wide.** Finding 03 here. Installer-03 does not fix this. Adapter cleanup also uses plain `os.WriteFile`. A repo-wide `internal/fsutil.AtomicWriteFile(path, data, mode)` primitive would close the class. Orthogonal to the proposed `internal/pathsafe` and `internal/safego` primitives; all three are leaf utilities that should be enforced by lint rules once introduced.
