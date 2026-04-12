# [Info] Binary size and embedded asset inventory

**Scope:** single-binary asset embedding mechanics
**Topic:** Binary size
**Date:** 2026-04-11

## Problem

Not a problem. This finding documents the current state of embedded assets for future reference.

## Evidence

Total binary size (darwin/arm64, `go build ./cmd/nanite`): **37MB**.

Seven `//go:embed` directives exist across the codebase:

| Directive | File | Content | Size on disk |
|---|---|---|---|
| `//go:embed all:ui_dist` | `internal/server/spa.go:10` | React SPA (Vite build output) | 2.5MB (40 files) |
| `//go:embed all:framework` | `internal/assets/framework.go:17` | Nanite framework (roles, skills, commands, docs, templates, vendor) | 492KB (76 files) |
| `//go:embed schemas/*.json` | `internal/envelope/contracts_test.go:13` | Envelope JSON schemas (**test-only**, not in production binary) | 96KB |
| `//go:embed migrations/*.sql` | `internal/store/store.go:15` | SQLite DDL migrations | 36KB (5 files) |
| `//go:embed *.md` | `internal/skill/builtin/embed.go:11` | Built-in skill definitions | ~32KB (8 files) |
| `//go:embed templates/*.tmpl` | `internal/plugin/scaffold/scaffold.go:16` | Plugin scaffold Go templates | 24KB |
| `//go:embed default.md` | `internal/agent/builtin/embed.go:9` | Default agent definition | ~4KB (1 file) |

**Total embedded content:** ~3.1MB (excluding the test-only schema embed).

The remaining ~34MB is the Go binary itself (runtime, stdlib, all imported packages including `modernc.org/sqlite` which is notably large as a pure-Go SQLite implementation).

## Impact

The embedded content is 8.4% of the total binary size. The SPA is the dominant contributor (2.5MB, 80% of embedded content). Framework content is well-controlled at 492KB for 76 files, all text except 8KB of PNGs.

No embedded content is unreasonably large. The `modernc.org/sqlite` dependency (pure Go SQLite) likely contributes significantly more to binary size than all embedded assets combined.

## Recommendation

No action needed. The embed inventory is healthy. Future additions should be mindful of:

- The `all:` prefix on `ui_dist` and `framework` means any file added to those directories is automatically embedded. Use `.gitignore` (already in place for `ui_dist/assets/`) or directory structure to control what lands in the embed.
- The envelope schemas embed is test-only (defined in `_test.go`), which is correct — test-only embeds do not inflate the production binary.

## References

- All seven embed directives listed above
- Binary size measured via `go build -o /tmp/nanite-size-check ./cmd/nanite && ls -lh`
