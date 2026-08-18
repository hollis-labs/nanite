# Cut the giphy self-tool and plugin registration

**Phase:** 0
**Status:** not-started
**Depends on:** none
**Touches:** `internal/mcp/self_tools_giphy.go` (delete, incl. its test file), wherever this self-tool is registered into the MCP self-tool set (locate at implementation time), `plugins/repos.yaml` (remove the `giphy` "default" plugin entry), `CLAUDE.md`/other doc references (see Context)

## Context

TASKS.md Phase 0 item 15 names giphy as a "confirmed demo" to cut. Planning-pass verification found this framing doesn't hold — `internal/mcp/self_tools_giphy.go` (296 lines) is a real, first-party, compiled-in MCP self-tool with a live GIPHY API client (not just a demo-data stub, though it does have a demo-data fallback for when no API key is configured), plus its own dedicated test file. `plugins/repos.yaml` also lists `giphy` as a real, currently-registered "default" external-repo plugin (`hollis-labs/nanite-plugin-giphy`) — a separate registration from the built-in self-tool.

**Operator decision, 2026-08-18 — final.** Working and real is not the same as wanted. Per the sharpened `docs/engineering/EXECUTION-PROCESS.md` escalation rule — `TASKS.md`'s decided action (cut) stands even when the decision-log's supporting rationale ("confirmed demo") doesn't hold up against the code — cut it in full. This is a bigger removal than "delete dead code" (it's removing a real, working feature), so size and review it accordingly, but the action itself is not in question.

## What to do

1. Delete `internal/mcp/self_tools_giphy.go` and its test file.
2. Find and remove wherever this self-tool gets registered into the MCP self-tool set (the manager/registry that wires up `self`-tier tools — check `internal/mcp/` and `cmd/nanite/main.go`'s self-tool registration).
3. Remove the `giphy` entry from `plugins/repos.yaml`.
4. Remove doc references: `CLAUDE.md` (check for any giphy mention), `docs/audits/2026-04-11-telemetry-privacy-posture/07-low-oembed-and-giphy-outbound.md` (this doc covers both giphy and oembed — coordinate with `15b-cut-oembed.md` so it only gets touched once; note in the Work Log which task actually edited it), `docs/tool-naming-audit.md` if it references giphy.
5. Grep the whole repo for `giphy`/`Giphy`/`GIPHY` (case-insensitive) after the cut to confirm no dangling references remain (config, env var names for the API key, etc.).
6. Run `go build ./cmd/nanite/`, `go vet ./...`, `go test ./...`.

## Done means

- `internal/mcp/self_tools_giphy.go` no longer exists; the self-tool is no longer registered/reachable.
- The `giphy` plugin entry is removed from `plugins/repos.yaml`.
- Doc references are cleaned up (coordinated with `15b-cut-oembed.md` on the shared audit doc, not duplicated).
- `go build ./cmd/nanite/`, `go vet ./...`, `go test ./...` pass.
- No remaining references to giphy anywhere in the codebase, confirmed by grep.

## Work log
<Worker fills this in as it goes: what was actually done, any deviation from plan and why, anything escalated.>

## Review notes
<Reviewer fills this in: pass/fail, what was checked, anything fixed and how.>
