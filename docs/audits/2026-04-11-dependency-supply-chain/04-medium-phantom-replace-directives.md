# [Medium] Phantom and unnecessary replace directives in go.mod

**Scope:** go.mod — module hygiene
**Topic:** Supply chain — version pinning
**Date:** 2026-04-11

## Problem

`go.mod` contains two replace directives for modules that the main module does not need, and all seven replace directives use `v0.0.0` pseudo-versions that hide the actual state of the sibling libraries.

## Evidence

**Phantom replace — `mcp-helpers`:**

`go.mod:38`:
```
replace github.com/hollis-labs/mcp-helpers => ../framework/libs/go-mcp
```

```
$ go mod why github.com/hollis-labs/mcp-helpers
(main module does not need package github.com/hollis-labs/mcp-helpers)
```

This module is not imported anywhere in nanite. The replace directive is dead weight. It causes `go list -m -u all` to fail because it tries to resolve `github.com/hollis-labs/mcp-helpers` against GitHub where no such repository exists.

**Unnecessary replace — `go-queue`:**

`go.mod:36`:
```
replace github.com/hollis-labs/go-queue => ../framework/libs/go-queue
```

`go-queue` is used transitively through `vanta-conduit`, not directly by nanite. The replace is needed for the build to resolve, but it's an artifact of the sibling-lib development model.

**All replace directives use `v0.0.0`:**

`go.mod:9-11,27-28,53`:
```
github.com/hollis-labs/go-plugin v0.0.0
github.com/hollis-labs/otel v0.0.0
github.com/hollis-labs/tool-broker v0.0.0
github.com/hollis-labs/go-providers v0.0.0
github.com/hollis-labs/vanta-conduit v0.0.0
github.com/hollis-labs/go-queue v0.0.0
github.com/hollis-labs/mcp-helpers v0.0.0
```

With `v0.0.0` and local replaces, there is no version contract. Any breaking change in a sibling lib is invisible until build failure. There is no way to audit which version of these libraries was used in a given build.

## Impact

- The phantom `mcp-helpers` replace blocks `go list -m -u all`, preventing update checks across the entire module graph.
- The `v0.0.0` pseudo-versions mean binary provenance is unverifiable — a built binary cannot report which version of `go-plugin` or `tool-broker` it was compiled against.
- `go mod tidy` does not remove the phantom replace because replaces are not tidied.

## Recommendation

1. Remove the `mcp-helpers` replace directive — it serves no purpose and actively breaks tooling.
2. For the remaining sibling libs, consider tagging releases (even `v0.1.0`) so that `go.mod` records a meaningful version. The replace directives can still override for local development, but the `require` line would show the last tagged version as documentation.
3. Run `go mod tidy` after removing the phantom replace to verify no breakage.

## References

- `go.mod:20-38` — all replace directives
- `go help mod edit` — replace directive semantics
