# Add go-agent-wrapper as a Nanite dependency

**Phase:** 2 — Nanite host migration (`TASKS/agent-host-acp`)
**Status:** not-started
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
