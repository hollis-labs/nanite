# Add go-agent-wrapper as a Nanite dependency

**Phase:** 2 — Nanite host migration (`TASKS/agent-host-acp`)
**Status:** implemented
**Depends on:** none
**Touches:** `go.mod`, `go.sum`. Repo: Nanite.

## Context

Nanite does not currently import `go-agent-wrapper` at all — confirmed by grep, zero hits in
`go.mod`. This is a pure dependency-wiring task: get the module reachable so tasks `04`-`06`
can start importing its packages. No functional code changes here.

Nanite already has an established pattern for exactly this — a local-checkout `replace`
directive pointing at a sibling `libs/` repo, used for active co-development before a tagged
release exists:

- `go.mod:94` — `replace github.com/hollis-labs/go-modelsdev => ../../libs/go-modelsdev`
- `go.mod:96-101` — `replace github.com/hollis-labs/go-envelopes => ../../libs/go-envelopes`,
  with a comment documenting why (`CW-20260816-0069: local dev against the go-envelopes
  schema addition... mirroring the go-modelsdev precedent above... Remove once go-envelopes
  cuts a release that [includes it]`).

Task `01` (Phase 1) intentionally leaves `go-agent-wrapper`'s own `replace
github.com/hollis-labs/agentkit => ../agentkit` in place rather than removing it, and cuts a
real tag for `go-agent-wrapper` itself but not (necessarily) for `agentkit` beyond what
already exists. This task follows the same "local replace, tagged require line, remove the
replace once every dependency has a real published version" shape.

## What to do

1. Add `require github.com/hollis-labs/go-agent-wrapper <version>` to Nanite's `go.mod` —
   use whatever tag task `01` actually cut (check `libs/go-agent-wrapper`'s tags at dispatch
   time rather than assuming a specific number).
2. Add a `replace github.com/hollis-labs/go-agent-wrapper => ../../libs/go-agent-wrapper`
   directive, following the `go-modelsdev`/`go-envelopes` precedent exactly — same comment
   style explaining why (local dev against a zero-adopter library with no module-proxy
   history yet) and the same "remove once a real release makes this unnecessary" note.
   Note: this may not strictly be necessary if the tag itself resolves cleanly via the Go
   module proxy — check first (`go mod download github.com/hollis-labs/go-agent-wrapper@<tag>`
   before assuming a `replace` is required); only add the `replace` if the plain tagged
   require doesn't resolve (likely, since this is an unpublished internal monorepo library
   with no public proxy entry — same situation `go-envelopes`/`go-modelsdev` are in).
3. Run `go mod tidy` and confirm no unexpected dependency changes ride along.
4. No import of any go-agent-wrapper package yet — that's tasks `04`-`06`. This task only
   needs `go build ./cmd/nanite/` and `go test ./...` to stay clean with the new,
   unused-so-far dependency present.

## Done means

- `go.mod`/`go.sum` reference `go-agent-wrapper` (require + replace, matching the
  `go-modelsdev`/`go-envelopes` precedent).
- `go build ./cmd/nanite/`, `go vet ./...`, `go test ./...` clean — same baseline as before
  this task, with the new dependency present but unused.

## Work Log (2026-08-21)

**Tag check.** `libs/go-agent-wrapper` has two local tags: `v0.1.0` and `v0.2.0` (`v0.2.0` =
`3600d24`, task 01's release, per the dispatch brief). `git ls-remote --tags` against
`https://github.com/hollis-labs/go-agent-wrapper.git` showed only `v0.1.0` pushed to
origin — `v0.2.0` exists only in the local sibling checkout. Confirmed directly: `go mod
download github.com/hollis-labs/go-agent-wrapper@v0.2.0` from Nanite's repo root fails with
`invalid version: unknown revision v0.2.0`. So the `replace` directive is not optional here —
it's required, exactly as `libs/go-agent-wrapper`'s own `go.mod` comment already anticipates
("Nanite's own go.mod needs a matching local replace... until go-agent-wrapper has enough
tagged release history to be pinned via the module proxy instead").

**What was added to `go.mod`:**
- `require github.com/hollis-labs/go-agent-wrapper v0.2.0` (alphabetically placed in the
  existing `require (...)` block that already holds `go-apppaths`, `go-embed-contracts`,
  etc.).
- `replace github.com/hollis-labs/go-agent-wrapper => ../../libs/go-agent-wrapper`, placed
  immediately after the existing `go-envelopes` replace, with a comment following the same
  style/shape as the `go-modelsdev`/`go-envelopes` precedent (`CW-20260816-0069`) — explains
  why (zero-adopter library, `v0.2.0` only in the local checkout, plain tagged require
  doesn't resolve via proxy) and states the removal condition (once `go-agent-wrapper` pushes
  `v0.2.0`+ to origin, drop the replace and let the tagged `require` resolve normally).

**`go mod tidy` behavior — a real gap between the task's literal wording and Go's actual
tooling behavior, noted per this task's own "decision vs. rationale" discipline rather than
treated as blocking:** running `go mod tidy` after adding the require+replace silently
**removed** the `go-agent-wrapper` require line entirely (go.sum picked up zero new entries
either). This is correct, unavoidable `go mod tidy` behavior, not a bug — `go mod tidy` only
keeps a direct require if some package in the module's build graph actually imports it, and
per this task's own explicit scope ("No import of any go-agent-wrapper package yet — that's
tasks 04-06"), nothing does yet. By contrast, `go-modelsdev`/`go-envelopes` survive `tidy`
because they're already genuinely imported (confirmed: `cmd/nanite/main.go`,
`internal/chat/envelope.go`, `internal/envelope/*.go`, `internal/plugin/*.go`,
`internal/service/{container,chat}.go` all import one or both today) — this task's situation
isn't actually analogous to that precedent in this one respect, even though the task file's
"Done means" bullet assumes it is. Resolution: ran `go mod tidy` once to confirm it produces
no unexpected side effects elsewhere in the dependency graph (confirmed — `go.sum` diff is
zero bytes, no other module version moved), then restored the `require` line by hand
afterward so `go.mod` matches the stated "Done means" criterion. `go build`/`go vet`/`go
test` are unaffected either way — none of them complain about an explicit, currently-unused
`require` line the way `tidy` does. Whoever picks up task `04`+ and adds the first real
import should expect `go mod tidy` to become a no-op again once that import exists (the
require line will then be "used" and tidy will leave it alone).

**Transitive dependency check.** `go-agent-wrapper`'s own `go.mod` requires `agentkit v0.3.0`
(already required by Nanite, matching version), `go-harness-filters v0.1.0`,
`go-llm-types v0.3.0`, `go-providers v0.23.0` (already required by Nanite), and
`go-runtime-events v0.1.0`; indirectly `go-llm-contracts v0.3.0`, `go-runner v0.5.0`,
`go-sandbox v0.2.1`. Nanite already requires `go-llm-contracts v0.3.0` and `go-llm-types
v0.3.0` directly (pre-existing, unrelated to this task) and `go-runner`/`go-sandbox`
indirectly at matching versions. Checked `git ls-remote --tags` for every one of
`go-agent-wrapper`'s dependencies (`go-harness-filters`, `go-llm-types`, `go-runtime-events`,
`go-llm-contracts`, `go-runner`, `go-sandbox`, `agentkit`) — all of them have their required
tags pushed to origin already, so none of them needed their own `replace` directive. The only
thing not yet pushed to origin is `go-agent-wrapper`'s own `v0.2.0` tag, which is why exactly
one new `replace` (not several) was needed. Note also: because Go ignores a replaced
dependency's *own* `replace` directives when it's consumed as a dependency (only the main
module's `go.mod` replaces are honored), `go-agent-wrapper`'s local replaces for `agentkit`,
`go-harness-filters`, and `go-runtime-events` do **not** carry through to Nanite's build —
Nanite resolves all three via their pushed tags instead, which is exactly why the check above
mattered.

**Verification:**
- `go build ./cmd/nanite/` — clean.
- `go vet ./...` — fails with 4 pre-existing findings in `internal/service/container.go`
  (`stopReaper`/`stopRuntimeReaper` "not used on all paths" possible-context-leak warnings).
  Confirmed pre-existing and unrelated to this task by temporarily swapping in the original
  (pre-task) `go.mod` and re-running `go vet ./internal/service/...` — identical failure
  reproduces with zero `go-agent-wrapper` references in `go.mod`. Baseline unchanged by this
  task, per "Done means"'s own "same baseline as before this task" framing.
- `go test ./...` — all packages pass (`✓ go test`, no failures).
- `go list -m github.com/hollis-labs/go-agent-wrapper` — resolves to
  `github.com/hollis-labs/go-agent-wrapper v0.2.0 => ../../libs/go-agent-wrapper`, confirming
  the replace is wired correctly.
- `go.sum` diff against pre-task baseline: zero bytes changed (no new checksums needed —
  the replace target is a local filesystem path, and no unused-but-resolvable transitive
  module needed a new sum entry since nothing in the build graph imports `go-agent-wrapper`
  yet).

**GLOSSARY check.** No new names introduced — this task adds a dependency reference only, no
new Go identifiers, config keys, or docs terms. Nothing to check against
`docs/engineering/GLOSSARY.md` beyond confirming that (done).

**Scope discipline.** No files outside `go.mod` were changed by this task. The working tree
had pre-existing uncommitted changes to `TASKS/ESCALATIONS.md`, `TASKS/INDEX.md`, and tasks
`01`/`02`'s files from earlier work in this batch — left untouched and excluded from this
task's commit, per instruction not to touch unrelated files.
