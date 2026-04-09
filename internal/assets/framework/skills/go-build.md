# Go Build (:go-build)

Build the current Go project. Runs inline.

## When to use

- `/go-build` — Build the project using Makefile or `go build`
- After making code changes to verify compilation
- Before committing to catch build errors

## Procedure

1. Check for a `Makefile` in the project root.
2. If `make build` target exists, run `make build`.
3. Otherwise, find entrypoints in `cmd/` and run `go build ./cmd/...`.
4. If no `cmd/` directory, run `go build ./...`.
5. Report success or failure with the exact error output.

## Output

```
✓ Build OK: {binary name or package count}
```

or

```
✗ Build failed:
{compiler error output}
```

## Invariants

- ALWAYS check for Makefile first — it may have custom build flags or output paths.
- NEVER skip build errors. Report the full error output.
- Do NOT run `go install` unless explicitly asked.
