# [Low] shadcn-ui evals.json embedded but unlikely to be used at runtime

**Scope:** single-binary asset embedding mechanics
**Topic:** Binary size / embed hygiene
**Date:** 2026-04-11

## Problem

The embedded framework tree includes `vendor/shadcn-ui/evals/evals.json`, a test/evaluation dataset that is not consumed by any Go code path. It inflates the binary marginally and sets a precedent for accumulating non-runtime data in the embed tree.

## Evidence

File present in the embedded tree:

```
internal/assets/framework/vendor/shadcn-ui/evals/evals.json
```

Grepping the Go codebase for `evals.json` or `evals/` references in non-test, non-asset code:

No Go code reads this file at runtime. It is extracted to `~/.nanite/vendor/shadcn-ui/evals/evals.json` where it may be referenced by a skill prompt but is never loaded by the binary itself.

This is a shadcn-ui project artifact (evaluation data for their AI coding agent) that was vendored wholesale. The file is small (likely < 4KB based on the du output not listing it in the top 20 by size), but it is pure dead weight in the binary.

## Impact

Minimal size impact. The concern is hygiene: the vendor tree was copied wholesale without pruning non-essential files. As more vendor content is added, these accumulate.

## Recommendation

When the vendor tree is next touched, prune files that are neither documentation nor skill content. Specifically: `evals/evals.json`, `agents/openai.yml` (OpenAI-specific agent config from the shadcn project, not a nanite agent definition), and any other shadcn project scaffolding.

## References

- `internal/assets/framework/vendor/shadcn-ui/evals/evals.json`
- `internal/assets/framework/vendor/shadcn-ui/agents/openai.yml`
- `internal/assets/framework.go:L17` — `all:framework` captures everything
