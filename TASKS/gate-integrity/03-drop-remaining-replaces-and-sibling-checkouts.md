# Drop the last two replaces and delete the sibling-checkout block entirely

**Phase:** 2 — Sibling decoupling, completion
**Status:** not-started
**Depends on:** `01` (edits the same two files) and `02` (its tags must exist
on the proxy). Both are **real** dependencies, not sequencing preferences:
without `02`'s tags this task cannot resolve the modules at all, and without
`01` it conflicts in both files it edits.
**Touches:** `go.mod`, `go.sum`, `.github/workflows/full-repo-quality.yml`.
Repo: nanite.

## Context

This task ends the pinned-sibling coupling. After `01` removed two replaces
and `02` published the tags the other two were waiting on, all four `replace`
directives and all four `actions/checkout` steps can go, and the workflow stops
checking out any sibling repository at all.

The coupling being removed, restated once so this task stands alone: the
workflow pins sibling SHAs, `go.mod` redirects the modules at those checkouts,
and therefore **the pin decides what CI validates — not `go.mod`, not the
published tag.** Nothing in either file made that visible, and it cost a full
CI cycle once already when a pin sat one commit behind a release.

Why deleting beats checking: a drift check would have to be maintained, would
fire only after someone had already introduced the drift, and leaves the
underlying "CI validates unreleased source" property intact. Removing the
replaces makes the pins unnecessary, which makes the drift impossible rather
than detectable. `[[nanite_workflow_pinned_sibling_refs]]`

## What to do

1. **Gate check first.** Confirm `02` actually published:
   ```
   for m in go-harness-filters go-runtime-events; do
     printf '%-20s ' "$m"
     curl -sS "https://proxy.golang.org/github.com/hollis-labs/$m/@v/list" | tr '\n' ' '; echo
   done
   ```
   Both must list `v0.1.1`. If not, stop — this task is blocked, not slow.

   **Ask the proxy over HTTP.** `go env GOPRIVATE` is `github.com/hollis-labs/*`,
   which defaults `GONOPROXY` to the same value and overrides a `GOPROXY=`
   prefix, so `go list -m -versions` answers from git and never consults the
   proxy — it returns the same "yes" whether or not the proxy has the tag.
   proxy.golang.org populates lazily on first request, so that difference is
   real for minutes after a push. See `agent-verification-discipline.md` §3.11.
2. Remove the `replace (...)` block containing `go-harness-filters` and
   `go-runtime-events`, along with the `TASKS/agent-host-acp/06` comment above
   it (which exists solely to explain why those replaces are needed).
3. Bump both `require` lines from `v0.1.0` to `v0.1.1`. Note that
   `go-harness-filters` is currently marked `// indirect` and
   `go-runtime-events` is a direct require — `go mod tidy` will sort out which
   is which; do not hand-edit the `// indirect` markers.
4. Delete the `Check out go-harness-filters` and `Check out go-runtime-events`
   steps. Then check whether the surrounding comment at the top of the checkout
   block (at `77137106`, `.github/workflows/full-repo-quality.yml:28`, *"Nanite's
   go.mod has four local replaces at ../../libs/<module>."*) still describes
   anything real. After this task it does not — remove it rather than
   amending it to describe a smaller number.
5. Run `go mod tidy`. Expect `go.sum` churn for all four modules.
6. Verify the workflow no longer creates a `libs/` directory at all, and that
   nothing else in it assumes one exists:
   `grep -n 'libs/' .github/workflows/full-repo-quality.yml`.

## Done means

- `grep -c ' => \.\./\.\./libs/' go.mod` returns `0` — no replace points into
  the sibling tree.
- `grep -c 'hollis-labs/go-modelsdev\|hollis-labs/go-envelopes\|hollis-labs/go-harness-filters\|hollis-labs/go-runtime-events' .github/workflows/full-repo-quality.yml`
  returns `0`.
- `grep -n 'libs/' .github/workflows/full-repo-quality.yml` returns nothing.
- `go build ./...` and `go test ./...` pass **with the entire
  `~/dev/hollis-labs/libs/` tree moved aside.** This is the acceptance test that
  matters and the only one that actually proves decoupling; a build run with
  the siblings present cannot distinguish success from the replaces still
  silently working. Restore the tree afterwards.
- `go mod tidy -diff` exits clean.
- **A hand-dispatched gate run is green** —
  `gh workflow run "Full-repo quality gate" --ref main` — with the run URL and
  its conclusion recorded in the Work log. This is the first gate run in which
  CI resolves every sibling from the proxy; a local pass does not substitute
  for it. Ignore an `internal/memory` `SQLITE_BUSY` failure (`CW-20260825-0001`).

## Work log

## Review notes
