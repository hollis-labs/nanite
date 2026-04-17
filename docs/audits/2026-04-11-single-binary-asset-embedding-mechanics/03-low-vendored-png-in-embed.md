# [Low] Vendored PNG images embedded via `all:framework`

**Scope:** single-binary asset embedding mechanics
**Topic:** Binary size / embed hygiene
**Date:** 2026-04-11

## Problem

The `all:framework` embed directive includes vendored binary files that are not framework content. Specifically, two PNG images from the shadcn-ui vendor tree are embedded into the Go binary.

## Evidence

`internal/assets/framework.go:17`:

```go
//go:embed all:framework
var frameworkAssets embed.FS
```

The `all:` prefix embeds every file in the `framework/` tree, including:

```
internal/assets/framework/vendor/shadcn-ui/assets/shadcn-small.png  (4KB)
internal/assets/framework/vendor/shadcn-ui/assets/shadcn.png        (4KB)
```

The `.gitignore` explicitly whitelists the vendor tree:

```
# .gitignore:8-9
!internal/assets/framework/vendor/
!internal/assets/framework/vendor/**
```

These PNGs are extracted to `~/.nanite/vendor/shadcn-ui/assets/` when `nanite install` runs. They appear to be reference images for the shadcn-ui skill documentation, not runtime assets.

## Impact

8KB total. Negligible in a 37MB binary. The concern is not the current size but the precedent: as the vendor tree grows, any binary files added there are automatically embedded without review. The `all:` directive does not distinguish between text content (roles, skills, docs) and binary blobs.

## Recommendation

No immediate action needed. If the vendor tree grows to include larger binaries (fonts, compiled WASM, etc.), consider one of:

1. Replace `all:framework` with explicit glob patterns that exclude binary types: `//go:embed framework/*.md framework/*.yaml framework/*.toml framework/**/*.md ...`
2. Add a `.gitattributes` or CI check that flags binary files added to `internal/assets/framework/`.
3. Move vendor assets that include binaries to a separate embed directive with its own size budget.

For now, the 8KB is well within acceptable bounds.

## References

- `internal/assets/framework.go:L17` — embed directive
- `internal/assets/framework/vendor/shadcn-ui/assets/` — PNG files
- `.gitignore:L8-9` — vendor tree whitelist
