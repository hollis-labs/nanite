# Post-Demo Issues Log — 2026-03-22

Four issues surfaced during demo-prep on the work machine. All were fixed well enough to unblock the demo. This document records root causes and the proper fixes to land after the demo.

---

## Issue 1: `kb_smart_search` function missing from kb_demo Postgres database — DONE

**What broke:** The IT Support agent's `search_kb` MCP tool failed silently. `KBTransport` connected to Postgres and the table and data existed, but every query errored with `function kb_smart_search does not exist`.

**Root cause:** The database was exported from the home machine and imported via `psql -f dump.sql`. During import, `CREATE EXTENSION pg_trgm` failed silently — either pg_trgm was installed globally in `template1` on the source machine and never required an explicit `CREATE EXTENSION`, or it hit a permissions error. Because `psql` continues on error by default, the subsequent `CREATE FUNCTION kb_smart_search` also failed silently (it depends on pg_trgm's `similarity()` function). Both failures printed to stderr and were ignored.

**Fix applied:** Manually ran `CREATE EXTENSION pg_trgm` and recreated the `kb_smart_search` function directly in kb_demo.

**Post-demo work:** COMPLETE
- [x] Created `scripts/kb_demo_setup.sql` — installs pg_trgm extension and creates kb_smart_search function
- [ ] Document the setup step in the support-ticket plugin README *(frontend/docs)*
- [x] Note: always use `psql --set ON_ERROR_STOP=1 -f dump.sql` or `pg_restore --exit-on-error`

**Reference:** `kb_smart_search` signature: `(p_query text, p_category text, p_tag text, p_limit int, p_source text)` returning `TABLE(id, title, category, severity, tags, related, rank float8, headline text, match_method text)`. Uses `websearch_to_tsquery` for full-text search with pg_trgm trigram similarity fallback.

---

## Issue 2: Wrong plugin loaded — builtin copy instead of submodule — DONE

**What broke:** `internal/plugin/allplugins/allplugins.go` was importing `github.com/hollis-labs/conduit/internal/plugin/builtin/supportticket` (a compiled-in copy) instead of `github.com/hollis-labs/conduit/plugins/support-ticket` (the live submodule). Changes to the submodule had no effect.

**Root cause:** `allplugins.go` was not updated when setting up the work machine. Both the builtin copy and the submodule call `RegisterPlugin("support", ...)` in their `init()` — importing both would panic. The submodule has no separate `go.mod` (it is part of the conduit module), so the correct import path is `github.com/hollis-labs/conduit/plugins/support-ticket`.

**Fix applied:** Changed the import in `allplugins.go` from the builtin path to the submodule path.

**Post-demo work:** COMPLETE
- [x] Deleted `internal/plugin/builtin/supportticket/` — dead code removed
- [x] `allplugins.go` already imports the submodule path (`plugins/support-ticket`)
- [ ] Add a note to the setup/onboarding doc *(docs)*

---

## Issue 3: Ticket confirmation card not appearing after ticket submission — PARTIAL (frontend remaining)

**What broke:** After submitting a ticket via the `TicketFormCard` UI component, the `ticket-confirmation` envelope (rich card with Download button) did not appear in the agent response. This worked on the home machine.

**Root cause:** The submodule was bumped from commit `ff8c92f` to `c2456d8` ("feat: IT Support plugin — standalone Conduit plugin using FE plugin SDK"). That rewrite of `TicketFormCard.tsx` dropped the `<!--TICKET_DATA:{...}:TICKET_DATA-->` marker from the `onSendMessage` call. The engine at `internal/chat/engine.go:1013` parses this marker from the user message to inject the `ticket-confirmation` envelope. Without the marker the engine never receives the ticket data and no confirmation card is produced. `TicketInitFlow.tsx` (the quick-action path) still included the marker; only `TicketFormCard` (the agent-emitted `ticket-form` envelope path) was missing it.

**Fix applied:** Added the `TICKET_DATA` marker back to `TicketFormCard.onSendMessage`, building the ticket JSON from the API response fields: `id`, `title`, `description`, `category`, `priority`, `status`, `requester`, `routing`, `created_at`.

**Post-demo work:**
- [ ] `TicketInitFlow` and `TicketFormCard` both construct similar `TICKET_DATA` payloads — extract a shared helper to prevent future divergence *(frontend)*
- [x] Added comment in `engine.go` near the marker-parsing logic referencing both components
- [ ] Consider a frontend integration test that asserts the marker is present in the message string after form submission *(frontend)*

---

## Issue 4: TypeScript build failing — @tsconfig/strictest v2 + TypeScript 5.9 — FRONTEND

**What broke:** `npm run build` produced approximately 30 TypeScript errors across roughly 15 files. All were strict-mode violations, not logic bugs.

**Root cause:** The work machine had `@tsconfig/strictest@2.0.8` + TypeScript 5.9.3. `tsconfig.app.json` extends `@tsconfig/strictest/tsconfig.json`, and v2 of that package enables options the codebase was not written for:

- `exactOptionalPropertyTypes: true` — `T | undefined` is not assignable to an optional `T` prop (the bulk of errors)
- `noUncheckedIndexedAccess: true` — array and object indexing returns `T | undefined`
- `noImplicitOverride: true` — class methods overriding a base class require the `override` keyword
- `noPropertyAccessFromIndexSignature: true` — index-signature properties must use bracket notation

The home machine had an older TypeScript version where these options were not enforced.

Two files needed individual fixes beyond the tsconfig override:
- `ComposerToolbar.tsx`: `useEffect` callback had an implicit `undefined` return on the else path (`noImplicitReturns` violation) — fixed by adding an explicit `return undefined`
- `ErrorCard.tsx`: JSX expression `{data.details && data.details.raw && (...)}` where `raw` is typed `unknown` — `unknown &&` propagates as `unknown` to JSX children, which is not a valid `ReactNode` — fixed with `data.details.raw != null &&`

**Fix applied:** Added explicit overrides for the four options in `tsconfig.app.json`. Fixed the two individual file errors.

**Post-demo work:** *(all frontend)*
- [ ] Pin `@tsconfig/strictest` to an exact version in `package.json` (remove the `^` from `^2.0.8`) so future installs do not pick up breaking strictness bumps — or accept the strict options and fix the codebase properly
- [ ] Pin TypeScript to an exact version (e.g. `5.9.3` rather than `~5.9.3`) for build reproducibility across machines
- [ ] If adopting the strict options properly: the `exactOptionalPropertyTypes` violations are mostly prop-passing patterns that need `| undefined` added to required prop types, or callers need explicit `?? defaultValue`
- [ ] Add `package-lock.json` to the repo (or document `npm ci`) so dependency versions are locked across machines
