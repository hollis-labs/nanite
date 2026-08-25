# Cut v0.1.1 releases for go-harness-filters and go-runtime-events

**Phase:** 1 — Sibling decoupling
**Status:** implemented
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

**2026-08-25 — implemented.** Both tags cut, pushed, and confirmed on the
module proxy. Nanite untouched. One deliverable is short of complete and is
called out under "Left undone" below: the two `CHANGELOG.md` commits exist
locally but are **not pushed** — pushing a branch was outside the dispatch's
authorization, which covered the tags and the changelog content only.

### Starting state

| Repo | HEAD at start | Tree | Notes |
|---|---|---|---|
| nanite | `325a6c21` | `git status --porcelain` empty | not `77137106`; `01` and others landed first |
| `libs/go-harness-filters` | `57a6b0919c0c5f06db90b367184988a72c430d39` | clean | `main` == `origin/main`; only tag `v0.1.0` |
| `libs/go-runtime-events` | `8756744985a6602d6ab1fb0df78d5aabc3920b1b` | clean | `main` == `origin/main`; only tag `v0.1.0` |

### Pins re-derived from the workflow, not from this file

```
grep -A2 'repository: hollis-labs/go-harness-filters' .github/workflows/full-repo-quality.yml
#   39:          ref: 57a6b0919c0c5f06db90b367184988a72c430d39
grep -A2 'repository: hollis-labs/go-runtime-events' .github/workflows/full-repo-quality.yml
#   46:          ref: 8756744985a6602d6ab1fb0df78d5aabc3920b1b
```

Derived at nanite `325a6c21`. Both pins are unchanged from this file's copy of
them; `01` deleted the `go-modelsdev` and `go-envelopes` checkout steps and
left these two, which now sit at lines 35-47.

### Release evidence, run directly (no `make` — neither repo has a Makefile)

`find <repo> -maxdepth 1 -iname 'Makefile*'` returns nothing in either repo, so
the `command -v tool && tool || echo "not installed"` hazard recorded in
`TASKS/ESCALATIONS.md` for the go-envelopes v0.2.0 release does not apply here.
Everything below was run directly and its real exit status read.

**`go-harness-filters` at `57a6b09`**

| Command | Result |
|---|---|
| `go test -count=1 ./...` | 5/5 packages `ok`, **exit 0** |
| `go test -race -count=1 ./...` | 5/5 packages `ok`, **exit 0** |
| `go vet ./...` | exit 0 |
| `govulncheck ./...` | "0 vulnerabilities" affecting this code, exit 0 |
| `$(go env GOROOT)/bin/gofmt -l ./classify ./directive ./event ./normalize ./repair` | **`directive/doc.go`**, exit 0 — see below |
| `golangci-lint run ./...` | **exit 1**, 2 staticcheck `QF1001` — see below |

**`go-runtime-events` at `8756744`**

| Command | Result |
|---|---|
| `go test -count=1 ./...` | 1/1 package `ok`, **exit 0** |
| `go test -race -count=1 ./...` | 1/1 package `ok`, **exit 0** |
| `go vet ./...` | exit 0 |
| `govulncheck ./...` | "0 vulnerabilities" affecting this code, exit 0 |
| `$(go env GOROOT)/bin/gofmt -l ./runtimeevents` | empty, exit 0 |
| `golangci-lint run ./...` | **exit 1**, 2 `errcheck` — see below |

The empty `gofmt` result for `go-runtime-events` is not a §4.2 vacuous pass: the
same binary, same invocation shape, flagged a file in the sibling repo minutes
earlier, so the mechanism is known to fire.

Counts derived at the tagged commits, with their commands:

```
# go-harness-filters: 37 tests, 5 packages
go test -count=1 -list '.*' ./... | grep -cE '^(Test|Example|Fuzz)'   # -> 37
go list ./... | wc -l                                                 # -> 5

# go-runtime-events: 27 tests, 1 package, 29 EventKind constants
go test -count=1 -list '.*' ./... | grep -cE '^(Test|Example|Fuzz)'   # -> 27
go list ./... | wc -l                                                 # -> 1
grep -cE '\bKind[A-Za-z]+ +EventKind = ' runtimeevents/kinds.go       # -> 29
```

### Tags

Created with `git tag -a v0.1.1 <pin-sha>` — the pinned SHA passed explicitly,
not `HEAD` — matching each repo's existing convention (`git cat-file -t v0.1.0`
returns `tag` in both; both v0.1.0 messages read `v0.1.0 — initial release`).

| Repo | Tag object | Peeled commit | Workflow pin | Match |
|---|---|---|---|---|
| go-harness-filters | `c0a30cbf...` | `57a6b0919c0c5f06db90b367184988a72c430d39` | same | YES |
| go-runtime-events | `6d89ba73...` | `8756744985a6602d6ab1fb0df78d5aabc3920b1b` | same | YES |

Messages: `v0.1.1 — concrete normalize and repair rules` and
`v0.1.1 — policy approval event helpers`.

Pushed one at a time by name (`git push origin v0.1.1`), never `--tags`. Both
`* [new tag]`, exit 0. Verified on the remote with the peeled form per
hazard §3.10 — `git ls-remote origin 'refs/tags/v0.1.1^{}'` returns the commit;
the unpeeled `refs/tags/v0.1.1` returns the tag object above and would have
reported a spurious mismatch.

`git describe --tags --abbrev=0` returns `v0.1.1` in both repos.

### Proxy confirmation

Asked over HTTP per §3.11, never via `go list -m -versions`. Positive control:
the identical command returned **only `v0.1.0`** in both repos immediately
before tagging, so the state change is the check firing, not a constant.

```
curl -sS https://proxy.golang.org/github.com/hollis-labs/go-harness-filters/@v/list
#   before tagging -> v0.1.0
#   2026-08-25T09:43-0500 -> v0.1.0  v0.1.1
curl -sS https://proxy.golang.org/github.com/hollis-labs/go-runtime-events/@v/list
#   before tagging -> v0.1.0
#   2026-08-25T09:43-0500 -> v0.1.0  v0.1.1
```

Checked for the version string rather than for exit status, since `curl -sS`
without `-f` exits 0 on a 404. The proxy populated on the first `@v/v0.1.1.info`
request, and its own answer names the commit:

```
{"Version":"v0.1.1", ...,"Origin":{"VCS":"git","URL":".../go-harness-filters",
 "Hash":"57a6b0919c0c5f06db90b367184988a72c430d39","Ref":"refs/tags/v0.1.1"}}
{"Version":"v0.1.1", ...,"Origin":{"VCS":"git","URL":".../go-runtime-events",
 "Hash":"8756744985a6602d6ab1fb0df78d5aabc3920b1b","Ref":"refs/tags/v0.1.1"}}
```

`sum.golang.org/lookup/...@v0.1.1` returns signed hashes for both. An
end-to-end consume test in a throwaway module outside both repos, with
`GOPRIVATE=` `GONOPROXY=none` so the proxy is genuinely consulted, downloaded
both modules, produced a `go.sum` whose `h1:` lines match the checksum DB
byte-for-byte, and exercised `repair.Chain` and `KindPolicyApprovalRequested`
at runtime (`go run` exit 0). Per §3.11's second half, that is evidence about
*published* bytes rather than a git-to-git comparison. **`03` is unblocked.**

### Files changed

Nothing in nanite except this Work log and Status.

- `libs/go-harness-filters/CHANGELOG.md` — new `## v0.1.1 — 2026-08-25` section
  above the v0.1.0 one, matching that repo's own Keep-a-Changelog dialect
  (`## vX.Y.Z — date`, summary line with test counts, `### Added`, `### Notes`).
  Committed locally as `48dfd6a`.
- `libs/go-runtime-events/CHANGELOG.md` — same shape. Committed locally as
  `531d0c0`.

The two repos' dialect is **not** go-envelopes' (`## [0.3.0] - 2026-08-24` with
an `[Unreleased]` heading); each was matched to its own file, as the task
required.

Because the tag has to point at the pinned commit, the changelog entry
necessarily lands *after* the tag and the released tree does not contain it.
Each entry says so in its own opening paragraph rather than leaving a reader to
discover it.

### Left undone — needs the operator

**Neither changelog commit is pushed.** Both repos are `main...origin/main
[ahead 1]` with a clean tree. The dispatch authorized the tags and the changelog
content but explicitly excluded pushing branches, so the commits were left
local. One command each finishes it:

```
git -C ~/dev/hollis-labs/libs/go-harness-filters push origin main
git -C ~/dev/hollis-labs/libs/go-runtime-events  push origin main
```

Until then the published repos show a v0.1.1 tag with no v0.1.1 changelog entry.
This does not gate `03` — the modules are on the proxy and consumable.

### Scope-parked — found, deliberately not fixed

Every item below lives in the **v0.1.0 commit** of its repo, not in the commit
being tagged, so fixing any of them means a new commit, and tagging that commit
instead of the pin is the exact failure this task exists to prevent.

1. **Both repos' own `check` workflow has never passed — including at v0.1.0.**
   `gh run list -R hollis-labs/go-harness-filters -L 5` and the same for
   go-runtime-events return `failure` for all four runs that exist.
   - go-harness-filters fails at `go fmt (verify)`; every later step is skipped.
     Reproduced locally: `gofmt -l` flags `directive/doc.go`, a Go 1.19
     doc-comment indent (spaces -> tab) in an example block.
     `git blame -L 6,10 -- directive/doc.go` attributes every affected line to
     `ada40c4`, the v0.1.0 commit, as does
     `git log --oneline -- directive/doc.go` (that one commit, nothing since).
     Both go1.25.3 and go1.26.7 `gofmt` agree.
   - go-runtime-events fails at `golangci-lint-action@v7` with `exit code 3`.
     **Not reproduced** — locally `golangci-lint run ./...` (v2.11.4) exits **1**
     with 2 `errcheck` findings on `defer f.Close()` in `filesink_test.go:53`
     and `:182`, both blamed to `71a0cf2`, the v0.1.0 commit. Exit 3 is a
     different class from exit 1, CI pins v2.1.6, and the run logs have expired
     (HTTP 410, 3 months old), so the CI cause is unconfirmed.
   - go-harness-filters also carries 2 staticcheck `QF1001` findings under the
     local linter, both blamed to `ada40c4` (`directive/directive.go:117,138`).
   None of this is a regression introduced by the tagged commits, and v0.1.0 was
   already published with all of it. Worth its own task.
2. **`go-harness-filters/README.md` is stale at the tagged commit.** It says
   "27 tests across 5 packages" (real: 37, command above) and heads its status
   block "Status (v0.1.0, 2026-05-26)". Commit `57a6b09` updated the prose
   around both and left the numbers.
3. **`go-runtime-events/CHANGELOG.md`'s v0.1.0 entry says "31 `EventKind`
   constants"; the real figure at that tag is 28.** Counted two ways —
   `grep -cE '\bKind[A-Za-z]+ +EventKind = '` on `kinds.go`, and a `go/ast`
   walk counting `ValueSpec`s typed `EventKind`, which agree. My own first
   count said 28-at-HEAD and was **wrong**: the regex was anchored `^\s+`,
   which misses `const KindSandboxApplied EventKind = "sandbox.applied"` — a
   single-line const with no leading whitespace. The disagreement with the
   published number is what exposed the bad command (§1.3). Corrected figures:
   29 at `8756744`, 28 at `v0.1.0`. The v0.1.1 entry states 29; the v0.1.0
   entry was left alone as a historical record.
4. **Semver shape.** Both releases add exported API, which strict semver calls a
   minor bump. `v0.1.1` is what the task, the operator and `03` all specify, and
   pre-1.0 the distinction is conventional; recorded, not acted on.

### Verification of the nanite fence

```
git -C <nanite> rev-parse HEAD
#   before: 325a6c21c54d027bad9276ce40c3f52679a57855
#   after:  325a6c21c54d027bad9276ce40c3f52679a57855
git -C <nanite> status --porcelain
#   before: (empty)   after: (empty, measured before this Work log was written)
```

`go build -o /dev/null ./cmd/nanite/` exits 0 and `git status --porcelain`
stays empty after it — the `-o /dev/null` form is deliberate, since a plain
`go build ./cmd/nanite/` deposits an untracked `./nanite` and would itself
break the property being claimed. Nanite's `go.mod` and workflow were not
touched; that is `03`.


## Review notes

**Reviewed 2026-08-25 at nanite `9969e873` by a fresh reviewer dispatch** — no
shared context with the implementing worker. Transcribed by the Orchestrator;
the reviewer agent type is read-only by design.

**Verdict: PASS**, with one low-severity Work-log accuracy defect.

**The irreversible part is correct.** Both `v0.1.1` tags resolve on the *remote*
to exactly the SHAs nanite's workflow pins — verified five independent ways
(`ls-remote` peeled with `^{}` per §3.10, `rev-list -n1`, `describe`, the
proxy's own `Origin.Hash`, and a full `ls-remote origin` showing exactly four
refs per repo with no strays). Nothing here needs superseding.

**The reviewer produced stronger evidence than the Work log claimed.** Rather
than rely on a local `go.sum` — which §3.11 says is not evidence about published
bytes, since `GOPRIVATE` defaults `GONOSUMDB` — it downloaded both published
`.zip`s from proxy.golang.org and diffed them against the `git archive`'d tagged
trees. **Identical for both modules.** That is direct proof about what the proxy
serves, and it closes the gap that made `01`'s byte-identity comparison
near-tautological.

Also verified independently: tag shape and message convention match each repo's
own `v0.1.0`; each `CHANGELOG.md` entry matches its own repo's dialect and
correctly does *not* adopt `go-envelopes`'; tests re-run from the tagged trees in
scratch at `-count=1` with and without `-race`, all exit 0, plus `go vet` and
`govulncheck` clean; neither repo has a Makefile, confirmed two ways, so the
`command -v tool && tool || echo` misreporting hazard genuinely did not apply.

**The worker's `EventKind` self-correction is right.** An independent `go/ast`
walk gives 29 at `8756744` and **28 at `71a0cf2`** — the second figure being a
positive control proving the counting mechanism differentiates. The regex failure
reproduces exactly: `const KindSandboxApplied EventKind = "sandbox.applied"` at
`kinds.go:72` is a single-line `const` with no leading whitespace, which the
`^\s+`-anchored pattern missed.

### Finding — low severity, high confidence

**The Work log's stated reason for `go build -o /dev/null` is factually wrong.**
It says a plain `go build ./cmd/nanite/` "deposits an untracked `./nanite` and
would itself break the property being claimed." `./nanite` is gitignored
(`.gitignore:75`, and again at `:2`), and a 71,804,466-byte `./nanite` sits on
disk right now while `git status --porcelain` returns zero lines — its mtime
predates the tagging, so it is not something this task left behind.

`-o /dev/null` remains the better command, for a different reason: it avoids
clobbering a large artifact someone may be running. Only the rationale is wrong.
Marked inline above rather than rewritten.

**Who hits it:** no build or runtime impact. It hits the next agent that mines
this Work log for a nanite-fence recipe and concludes `git status --porcelain`
catches stray build output. It does not — not for `./nanite`, nor for
`/cmd/nanite/nanite` (`.gitignore:3`).

### Observations, not findings

- **The tagged trees' `CHANGELOG.md` tops out at v0.1.0.** Unavoidable given
  "the tag must equal the pin," and disclosed by the worker in three places.
- **Neither repo's `check` workflow fires on tags** (`on: push: branches:
  [main]` + `pull_request`), so the tag pushes generated no CI signal in either
  direction.
- **Once the changelog commits are pushed, `main` will again sit one commit past
  the newest tag in both repos** — the same *shape* this batch's original
  detection command flags. Harmless: `03` deletes the SHA pins entirely and
  `go.mod` resolves `v0.1.1` through the proxy. Noted so a re-run of
  `rev-list --count v0.1.0..<pin>` is not misread as a regression.

### Parallel-session check

The reviewer was warned mid-pass that a separate session might be operating in
both sibling repos. It re-verified all state three times across an eight-minute
window: both `HEAD`s, `[ahead 1]`, empty `--porcelain`, and the full live
`ls-remote origin` ref lists were byte-for-byte identical at every observation,
and both `v0.1.1` tags resolved to the workflow pins each time. **No activity
from another session was observed in either repo during that window.**
