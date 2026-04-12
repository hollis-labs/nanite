# [Info] Embed security model and cold-start performance are sound

**Scope:** single-binary asset embedding mechanics
**Topic:** Security / Cold-start performance
**Date:** 2026-04-11

## Problem

Not a problem. This finding confirms the security posture and cold-start behavior of the embed system.

## Evidence

### Post-build tamper resistance

Go's `embed.FS` is compiled into the binary's read-only data section. At runtime:

- `frameworkAssets` (`internal/assets/framework.go:18`) is a read-only filesystem. There is no `Write`, `Remove`, or `Chmod` method on `embed.FS`.
- `embeddedUI` (`internal/server/spa.go:11`) is similarly read-only.
- An attacker who modifies the binary on disk changes the embedded content, but this is equivalent to replacing the binary entirely — standard binary integrity measures (code signing, filesystem permissions, checksum verification) apply. There is no embed-specific tamper vector.

### Cold-start performance

The binary does not decompress or process embedded assets at startup:

1. **No `init()` functions** in `internal/assets/`, `internal/server/`, or any embed-hosting package that read from the embedded FS.
2. **`assets.Version()`** is the only framework read triggered during install, and it reads a single 7-byte file (`VERSION`). It is not called during `nanite serve` startup.
3. **SPA serving** is lazy — `handleSPA` reads from `embeddedUI` only when an HTTP request arrives, not at startup.
4. **Migration running** (`internal/store/store.go`) reads from `migrationsFS` at DB open time, which is early in startup but reads only 5 small SQL files (36KB total). This is I/O-bound on the SQLite `PRAGMA` and `CREATE TABLE` calls, not on reading the embedded content.
5. **Skill/agent loading** from `internal/skill/builtin/` and `internal/agent/builtin/` happens at seed time (first DB open or explicit seed), not on every startup.

Go's embed implementation uses the binary's mmap'd data section — there is no copy-to-heap step. Reads from `embed.FS` are effectively pointer arithmetic into the already-loaded binary.

### No decompression

None of the seven embed directives use compressed content. Go's `embed` package does not support compression natively. The embedded bytes are stored verbatim in the binary. This means:

- No CPU cost at read time.
- Binary size equals content size (no compression savings, but also no decompression overhead).
- For the current asset sizes (3.1MB total), compression would save ~1.5MB at the cost of adding a decompression step. Not worth it at this scale.

## Impact

The embed system adds zero measurable overhead to cold-start time. Security properties are inherited from Go's embed design — read-only, no runtime mutation, tamper-resistant to the same degree as the binary itself.

## Recommendation

No action needed. The current design is appropriate for the asset sizes involved.

## References

- Go embed package documentation: https://pkg.go.dev/embed
- `internal/assets/framework.go:L17-18` — framework embed
- `internal/server/spa.go:L10-11` — SPA embed
- `internal/store/store.go:L15-16` — migration embed
- `cmd/nanite/main.go:L43-67` — startup path (no embed reads)
