# [Info] Trust boundary audit coverage map

**Scope:** All — cross-cutting
**Topic:** Security
**Date:** 2026-04-11

## Problem

This is an informational finding documenting which trust boundaries from `reviewer-backend.md` have been audited and which gaps remain after this `security-threat-model` pass.

## Evidence

### Trust boundary coverage after this audit

| # | Trust Boundary | Audited By | Key Findings |
|---|---|---|---|
| 1 | HTTP API requests | `api-privilege-boundary` (3C/4H) | CORS reflection, unvalidated event emission, plugin install path traversal |
| 2 | User messages in chat | **This audit** finding 04 | No prompt injection defense, scope_guard dead |
| 3 | Tool arguments | `toolbroker` (3H) | Permission bypasses, unvalidated arguments |
| 4 | MCP tool results | `mcp-client-transport` (2C/5H) | Zero validation, subprocess leaks, OOM |
| 5 | Plugin manifests | **This audit** findings 01, 06, 07 | No schema validation, entrypoint command injection, unsigned warning-only |
| 6 | Plugin code | `plugin-system-plan-eval` (1C/6H), design-acknowledged | In-process, no isolation (deliberate) |
| 7 | PTY bridge I/O | **This audit** findings 02, 03, 09 | Full env inheritance, no output validation, log leaks |
| 8 | Subprocess execution | `sandbox-hardening` (3C/3H), `dev-tools-input-validation` (3C/5H) | Path traversal, RCE via dev_bash, Linux silent fallback |
| 9 | Environment variables | **This audit** findings 02, 05 | Full env inheritance in PTY/subprocess/MCP/plugin, incomplete secret patterns |
| 10 | File system | `managed-section-parser` (2H), `installer` (4H) | Marker injection, symlink follow, TOCTOU |
| 11 | Outbound HTTP | `provider-abstractions` (2H), `sandbox-hardening` (proxy SSRF) | Gemini key in URL, unbounded response bodies, proxy bypass |

### Remaining gaps not covered by any audit

1. **Memory extraction PII**: user-pasted secrets persist in the memory store. Flagged as Medium in `memory-ranking` but no mitigation audited.
2. **Worktree subprocess audit**: `internal/worktree/manager.go` git operations — queued as item 51 in INDEX.md.
3. **Tool YAML loader**: `internal/tool/yaml_loader.go` subprocess invocation — queued as item 52.
4. **MCP server inbound permission boundary**: `internal/mcpserver/server.go` bypasses ToolClient — queued as item 60.
5. **Project repo path validation**: `internal/api/autocomplete.go` directory enumeration — queued as item 55.

## Impact

All 11 trust boundaries now have at least one audit pass. The five boundaries audited in this pass (5, 7, 9, and partially 2 and 11) had no prior dedicated coverage. The remaining gaps are all queued in INDEX.md with scope definitions.

## Recommendation

No action required — this is a coverage tracking document. The "Recommended next steps" section of the index will prioritize the remaining gaps.

## References

- `reviewer-backend.md` §"Trust boundaries" — the 11-item enumeration
- `docs/audits/INDEX.md` — full audit queue with cross-references
