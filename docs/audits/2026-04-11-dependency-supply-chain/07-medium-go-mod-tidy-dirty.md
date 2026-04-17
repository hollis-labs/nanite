# [Medium] go mod tidy produces a diff — module graph is not clean

**Scope:** go.mod — module hygiene
**Topic:** Standards — tooling
**Date:** 2026-04-11

## Problem

Running `go mod tidy` produces a diff, meaning the committed `go.mod` does not match what the Go toolchain considers canonical.

## Evidence

Command: `go mod tidy -diff`

```diff
--- current/go.mod
+++ tidy/go.mod
@@ -55,7 +55,7 @@
 	github.com/klauspost/compress v1.18.0 // indirect
 	github.com/lib/pq v1.12.0
 	github.com/mailru/easyjson v0.7.7 // indirect
-	github.com/mattn/go-isatty v0.0.20 // indirect
+	github.com/mattn/go-isatty v0.0.20
 	github.com/ncruces/go-strftime v1.0.0 // indirect
```

`go-isatty` is marked `// indirect` in the committed `go.mod` but is actually a direct dependency (imported somewhere in nanite's own code). `go mod tidy` would remove the `// indirect` comment.

## Impact

A dirty `go mod tidy` state means the dependency graph metadata is slightly inaccurate. The `// indirect` marker is informational only and does not affect builds, but:

- It makes dependency auditing harder (a human scanning for direct deps would miss `go-isatty`).
- CI checks that run `go mod tidy -diff` would fail.
- The `lefthook` pre-commit hooks do not appear to include a `go mod tidy` check.

## Recommendation

Run `go mod tidy` and commit the result. Consider adding `go mod tidy -diff` as a CI check or lefthook pre-commit step.

## References

- `go.mod:58` — the `go-isatty` line
