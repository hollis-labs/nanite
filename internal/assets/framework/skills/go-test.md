# Go Test (:go-test)

Run tests for the current Go project. Runs inline.

## When to use

- `/go-test` — Run all tests
- `/go-test ./internal/config/...` — Run tests for a specific package
- `/go-test -v` — Run with verbose output
- After making changes to verify nothing is broken

## Procedure

1. Check for a `Makefile` in the project root.
2. If `make test` target exists and no specific package was requested, run `make test`.
3. Otherwise, run `go test` with the requested flags:
   - Default: `go test ./...`
   - Specific package: `go test {package}`
   - Verbose: add `-v` flag
   - Race detection: add `-race` flag if requested
4. Report results — pass count, fail count, and any failure output.

## Output

```
✓ Tests passed: {N} packages, {duration}
```

or

```
✗ Tests failed: {N passed}, {M failed}
{failure output}
```

## Invariants

- ALWAYS run `go test`, never `go run` test files directly.
- ALWAYS show full failure output — don't truncate test errors.
- NEVER modify test files to make them pass. Report failures as-is.
