# Cut the oembed plugin registration

**Phase:** 0
**Status:** implemented
**Depends on:** none
**Touches:** `plugins/repos.yaml` (remove the `oembed` "default" plugin entry), any oembed-related backend/frontend code registered through that plugin (locate at implementation time — see Context for why this wasn't fully traced during planning), doc references (see Context)

## Context

TASKS.md Phase 0 item 15 names oembed as a "confirmed demo" to cut, alongside giphy and support-ticket. Planning-pass verification confirmed `plugins/repos.yaml` lists `oembed` as a real, currently-registered "default" external-repo plugin (`hollis-labs/nanite-plugin-oembed`), the same registration mechanism giphy uses — but unlike giphy (which also has a first-party built-in self-tool independently verified as live) and support-ticket (which has real frontend source independently verified as live), no first-party built-in equivalent or frontend source was located for oembed during planning. Its liveness is genuinely less certain than the other two — this task's worker should do the liveness check the planning pass didn't have time to complete, not assume either way.

**Operator decision, 2026-08-18 — final.** Cut in full regardless of what the liveness check finds, per the same reasoning as `15a-cut-giphy.md`: `TASKS.md`'s decided action stands independent of whether the decision-log's "confirmed demo" rationale holds up. The liveness check below is about knowing the actual size/blast-radius of the removal, not about deciding whether to do it.

## What to do

1. Before removing anything, do the liveness check planning didn't finish: is `oembed` a subprocess plugin (separate repo, `plugins/repos.yaml`-registered) with real backend/frontend code that ships in this repo, or purely an external repo reference with nothing local to remove beyond the registration line? Check for any `oembed`-referencing code in `internal/` and `ui/src/` (component names, API routes, self-tools) the same way `15a`/`15c` found for giphy/support-ticket.
2. Remove the `oembed` entry from `plugins/repos.yaml`.
3. Remove whatever local backend/frontend code the liveness check in step 1 finds.
4. Remove doc references: `docs/audits/2026-04-11-telemetry-privacy-posture/07-low-oembed-and-giphy-outbound.md` (shared with `15a-cut-giphy.md` — coordinate so it's only edited once; check that task's Work Log first), `docs/tool-naming-audit.md` if it references oembed, `CLAUDE.md` if it references oembed.
5. Grep the whole repo for `oembed`/`oEmbed`/`OEmbed` (case-insensitive) after the cut to confirm no dangling references remain.
6. Run `go build ./cmd/nanite/`, `go vet ./...`, `go test ./...`, and `cd ui && npm run build` if any frontend file was touched.

## Done means

- `oembed` is no longer registered in `plugins/repos.yaml`.
- Whatever local code (if any) the liveness check found is removed.
- Doc references cleaned up (coordinated with `15a-cut-giphy.md` on the shared audit doc).
- `go build ./cmd/nanite/`, `go vet ./...`, `go test ./...` pass; frontend build passes if touched.
- The Work Log records what the liveness check actually found (purely a registration-line removal, or real local code) — this is worth knowing even though it doesn't change the outcome.

## Work log

**2026-08-18 — worker report:** Liveness check confirmed oembed is purely an external-repo registration with zero local implementation (no builtin package, no frontend component, no self-tool). Removed the `oembed` entry from `plugins/repos.yaml` and doc references (`CLAUDE.md`, `docs/tool-naming-audit.md`, the shared oembed/giphy audit doc — coordinated with `15a` on that shared file). `go build`/`go vet`/`go test` all pass. No escalations.

## Review notes
<Reviewer fills this in: pass/fail, what was checked, anything fixed and how.>
