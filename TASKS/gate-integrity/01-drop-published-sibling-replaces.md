# Drop the go-envelopes and go-modelsdev replaces, and bump go-envelopes to the version actually being built

**Phase:** 1 — Sibling decoupling
**Status:** not-started
**Depends on:** none
**Touches:** `go.mod`, `go.sum`, `.github/workflows/full-repo-quality.yml`
(the `go-modelsdev` and `go-envelopes` checkout steps only — at `77137106`
those are lines 35-47; **re-derive**, task `04` edits the same file lower
down). Repo: nanite.

Closes Torque `CW-20260816-0090` ("Follow-up: release go-envelopes with
report-card session_link, drop local go.mod replace"), tagged
`blocking-merge`, open since 2026-08-16.

## Context

`.github/workflows/full-repo-quality.yml` checks four sibling repos into
`libs/` at hard-pinned SHAs, and `go.mod`'s `replace` directives point the
modules at those checkouts. The consequence is that **the workflow pin — not
`go.mod`, not the published tag — decides what CI validates against**, and
nothing in either file makes that visible.

It has already cost a full CI cycle once: the US-English migration changed the
`session-task` enum in go-envelopes and released v0.3.0, and CI still failed
against the *old* enum because the pin sat one commit behind.

**The planning session's correction: for two of the four siblings this is
removable outright, not something to monitor.** Both `go-envelopes` and
`go-modelsdev` are pinned to a SHA that is byte-identical to their newest tag,
both tags are published on the module proxy, and both sibling working trees
are clean — so the `replace` and the proxy resolve to the same source. Dropping
them is a no-op for what gets compiled, and it removes two of the four coupled
pins permanently.

Derived at `77137106`:

```
d=~/dev/hollis-labs/libs/go-envelopes
git -C $d status --porcelain | wc -l                 # 0  (clean)
git -C $d rev-list -n1 v0.3.0                        # 58243d84...  == the CI pin
GOPROXY=https://proxy.golang.org go list -m -versions github.com/hollis-labs/go-envelopes
#   -> v0.1.0 v0.1.1 v0.2.0 v0.3.0
grep -rn 'session_link' $d/manifest/                 # report-card.schema.json:73
```

That last line is `CW-20260816-0090`'s actual acceptance condition: the schema
addition the replace existed for is in a published release now.

Two related defects in the same file, both real at `77137106`:

- **`go.mod:20` requires `go-envelopes v0.1.1`** while the build runs v0.3.0's
  source through the replace. The recorded version is three minor versions
  stale and actively misleading — a reader checking "what version of
  go-envelopes does nanite use?" gets the wrong answer from the obvious place.
- **`go.mod:115` says *"Mirrors the go-agent-wrapper replace immediately
  above."*** There is no go-agent-wrapper replace:
  `grep -c 'go-agent-wrapper =>' go.mod` returns `0`. The comment is
  load-bearing prose in a block explaining why the *other* replaces must stay,
  so a stale pointer there is worse than a stale pointer in a doc.

## What to do

1. **Re-derive the pin/tag/proxy state before changing anything.** Run the
   three-command block above for both `go-envelopes` and `go-modelsdev`. If
   either sibling tree is dirty, or either pin no longer equals its newest
   tag, or either tag is absent from the proxy — **stop and escalate.** This
   task's whole safety argument is "the two sources are identical"; if that is
   no longer true, the task is not a no-op and needs re-scoping.
2. Delete the `replace github.com/hollis-labs/go-envelopes => ../../libs/go-envelopes`
   directive and the `CW-20260816-0069` comment block above it, and bump the
   `require` from `v0.1.1` to `v0.3.0`.
3. Delete the `replace github.com/hollis-labs/go-modelsdev => ../../libs/go-modelsdev`
   directive. Its `require` is already `v0.2.0` — confirm, do not assume.
4. Delete the `Check out go-modelsdev` and `Check out go-envelopes` steps from
   the workflow. **Find them by name, not by the line numbers above** — task
   `04` edits the gosec step in the same file and may land first.
5. Fix the stale comment on the surviving `replace (...)` block. Do not write
   what it used to say or why it changed — state what is true now: these two
   siblings are pinned ahead of their published `v0.1.0` tags, so the replaces
   must stay until task `02`/`03` land. Name `TASKS/gate-integrity/03` as the
   thing that removes them.
6. Run `go mod tidy`. The gate runs `go mod tidy -diff`
   (`.github/workflows/full-repo-quality.yml`, "Verify modules" step) and will
   red on an untidied `go.sum`.

## Done means

- `grep -c 'go-envelopes =>\|go-modelsdev =>' go.mod` returns `0`.
- `grep -n 'go-envelopes' go.mod` shows `v0.3.0` and no replace.
- `grep -c 'go-agent-wrapper =>' go.mod` still returns `0`, and no comment in
  `go.mod` claims such a replace exists.
- `go build ./...` and `go test ./...` pass with **no** `libs/go-envelopes` or
  `libs/go-modelsdev` checkout present — the real proof. Verify by temporarily
  moving those two sibling directories aside, or by building in a clone that
  has no `libs/` sibling tree at all. A build that passes only because the
  sibling directory happens to sit at the replace path proves nothing.
- `go mod tidy -diff` exits clean.
- The workflow no longer names `hollis-labs/go-envelopes` or
  `hollis-labs/go-modelsdev`:
  `grep -c 'go-envelopes\|go-modelsdev' .github/workflows/full-repo-quality.yml`
  returns `0`.
- **A hand-dispatched gate run is green**, because the thing this task changes
  is precisely what CI resolves:
  `gh workflow run "Full-repo quality gate" --ref main`.
  Ignore an `internal/memory` `SQLITE_BUSY` failure — that is
  `CW-20260825-0001`, known and fixed upstream.
- `CW-20260816-0090` transitioned to `done` in Torque with a comment naming the
  landing commit.

## Work log

## Review notes
