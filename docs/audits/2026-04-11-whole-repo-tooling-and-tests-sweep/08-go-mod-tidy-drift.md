# go mod tidy drift — 2026-04-11 sweep

**Command:** `go mod tidy` (followed by `git diff go.mod go.sum` and `git checkout -- go.mod go.sum`)
**Result:** One line of drift in `go.mod`, no drift in `go.sum`.

## Diff

```diff
 	github.com/lithammer/fuzzysearch v1.1.8
-	github.com/mattn/go-isatty v0.0.20 // indirect
+	github.com/mattn/go-isatty v0.0.20
```

A single line. `github.com/mattn/go-isatty` is marked `// indirect` in the committed `go.mod` but is actually a direct dependency somewhere in the tree. `go mod tidy` promotes it by removing the `// indirect` marker.

## Severity

**Medium.** Consistent with the task brief classification for `go mod tidy` drift. The drift is not a bug in the sense of "code is broken," but:

1. The committed `go.mod` is not the `go mod tidy` fixed point. A developer who runs `go mod tidy` and commits the result will produce a diff vs. the committed state, which causes review friction ("why is this file changed?") and can mask real drift (if a future run produces a real indirect→direct or vice-versa change on top of this pre-existing drift, the diff becomes ambiguous).
2. The pre-commit hook list in `.agentrc/agents/backend.md` §Build & Run does not include `go mod tidy` as a lint target. If it did, this would have been caught.

## Confirmation that the file is reverted

```
$ git status go.mod go.sum
On branch audit-campaign-2026-04-11
nothing to commit, working tree clean
```

The working tree is restored to its pre-sweep state. No mutation survives the audit.

## Recommendation

Run `go mod tidy` once, commit the resulting single-line diff to `go.mod`, and consider adding a lefthook pre-commit check that runs `go mod tidy -check` (Go 1.25+ supports `-check` to verify without mutating) or diff-checks `go.mod`/`go.sum` after a tidy pass. This closes the drift class for future runs.

## Cross-ref

No cross-audit. This is a first observation — none of the prior audits ran `go mod tidy`.
