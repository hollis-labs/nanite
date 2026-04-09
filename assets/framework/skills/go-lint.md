# Go Lint (:go-lint)

Lint the current Go project. Runs inline.

## When to use

- `/go-lint` — Run all available linters
- Before committing to catch issues early
- During code review to verify code quality

## Procedure

1. Run `go vet ./...` (always available, always first).
2. Check if `golangci-lint` is available (`which golangci-lint`).
   - If available and `.golangci.yml` exists in project root, run `golangci-lint run ./...`.
3. Check for a `Makefile` with a `lint` target — if it exists and does something beyond `go vet`, run `make lint` instead of the above.
4. Report all findings grouped by severity.

## Output

```
✓ Lint clean: {N} packages checked
```

or

```
✗ Lint issues:
{file}:{line}: {message}
{file}:{line}: {message}
```

## Invariants

- ALWAYS run `go vet` at minimum, even if other linters are configured.
- Report ALL findings — don't filter or suppress warnings.
- NEVER auto-fix lint issues without explicit user request.
