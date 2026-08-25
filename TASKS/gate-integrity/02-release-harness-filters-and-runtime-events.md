# Cut v0.1.1 releases for go-harness-filters and go-runtime-events

**Phase:** 1 — Sibling decoupling
**Status:** not-started
**Depends on:** none
**Touches:** **Repo: `~/dev/hollis-labs/libs/go-harness-filters` and
`~/dev/hollis-labs/libs/go-runtime-events`, not nanite.** No file in the
nanite repo changes in this task. Tags, and whatever each repo's own release
convention requires (`CHANGELOG.md` at minimum — check each repo, do not
assume they match each other).

## Context

Nanite's quality gate pins these two siblings **one commit past their newest
published tag**, so CI validates against source that exists in no release.
Derived at nanite `77137106`:

```
for m in go-harness-filters:57a6b0919c0c5f06db90b367184988a72c430d39 \
         go-runtime-events:8756744985a6602d6ab1fb0df78d5aabc3920b1b; do
  n=${m%%:*}; s=${m##*:}; d=~/dev/hollis-labs/libs/$n
  echo "$n: $(git -C $d rev-list --count v0.1.0..$s) commit(s) ahead of v0.1.0"
  git -C $d log --oneline v0.1.0..$s
done
```

That returns `1` for each:

- `go-harness-filters` `57a6b09` "Add concrete normalize and repair rules" —
  adds `repair.Chain`, `MissingClosingDelimiterJSON`, `SlugNormalizer`.
- `go-runtime-events` `8756744` "Add policy approval event helpers" — adds
  `Emitter.EmitReturning` and a policy-approval event kind.

This is not cosmetic. `go.mod:112` in nanite records that building **without**
these replaces fails with `undefined: hrepair.Chain` inside
`go-agent-wrapper/filters` — a symbol added by exactly the unreleased
`57a6b09`. So `go-agent-wrapper v0.8.1`, a published module, depends on
go-harness-filters source that was never released. The replaces in nanite are
patching over a gap in a *different* repo's dependency graph.

Note for whoever picks this up: nanite itself does not reference either new
symbol —
`grep -rn 'hrepair\.\|EmitReturning\|PolicyApprovalRequested' --include='*.go' .`
returns nothing in the nanite tree at `77137106`. The dependency runs through
go-agent-wrapper, not through nanite's own code. Do not conclude from nanite's
grep that the commits are unused.

Each repo's `README`/batch history describes a "drop before tagging"
discipline for these two — the reasoning being that nothing outside the repo
depended on the replace staying. That premise is now false: nanite's gate and
`go-agent-wrapper v0.8.1` both depend on the unreleased commits. Tagging is
the fix.

## What to do

1. In each repo, confirm the working tree is clean and `HEAD` is the pinned
   SHA (`git -C <repo> status --porcelain` empty;
   `git -C <repo> rev-parse HEAD` matches the pin above — **re-derive the pin
   from nanite's workflow**, do not trust this file's copy of it).
2. Run each repo's own test suite with `-count=1`. A cached pass proves
   nothing about a release.
3. Check each repo's release convention before tagging — read its
   `CHANGELOG.md` and the shape of its existing `v0.1.0` tag. These two repos
   may not share a convention with each other or with go-envelopes.
4. Tag `v0.1.1` on each and push the tags.
5. Confirm the proxy has picked each one up:
   ```
   curl -sS https://proxy.golang.org/github.com/hollis-labs/go-harness-filters/@v/list
   curl -sS https://proxy.golang.org/github.com/hollis-labs/go-runtime-events/@v/list
   ```

   **Ask the proxy over HTTP, not via `go list -m -versions`.** `go env
   GOPRIVATE` is `github.com/hollis-labs/*`, which defaults `GONOPROXY` to the
   same value and beats a `GOPROXY=` prefix, so `go list` answers from
   `git ls-remote` on your own origin — it will say `v0.1.1` the instant you
   push the tag, whether or not the proxy has it. That is a false green on the
   one thing this step exists to confirm. `agent-verification-discipline.md`
   §3.11.
   The proxy is not instantaneous. Do not mark this task done until both list
   `v0.1.1`; task `03` cannot start before that.
6. **Do not touch nanite's `go.mod` or workflow in this task.** That is `03`.
   Keeping them separate is what lets `02` run in parallel with `01`.

## Done means

- `git -C <repo> describe --tags --abbrev=0` returns `v0.1.1` in both repos.
- Both `v0.1.1` tags point at the exact SHA nanite's workflow pins — verify per
  repo with `git rev-list -n1 v0.1.1` against the pin re-derived from
  `.github/workflows/full-repo-quality.yml`.
- Both `curl` calls above list `v0.1.1`. Re-run until they do; the proxy
  populates lazily on first request.
- Each repo's test suite passed at that SHA with `-count=1`, with the command
  and result recorded in the Work log.
- Nanite is **untouched** by this task. Compare `git -C <nanite> rev-parse HEAD`
  and `git -C <nanite> status --porcelain` before and after, and show they are
  identical — do not assert the tree is *empty*, since other Wave A tasks land
  in nanite around this one and an empty tree is not the property being claimed.

## Work log

## Review notes
