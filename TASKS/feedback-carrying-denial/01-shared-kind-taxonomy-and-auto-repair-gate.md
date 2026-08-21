# Shared Kind taxonomy + auto-repair-eligibility split

**Phase:** 1 — Shared taxonomy foundation (`TASKS/feedback-carrying-denial`)
**Status:** not-started
**Depends on:** none
**Touches:** `internal/recover/recover.go` (new `Kind` constants, new `Source` field on
`RecoverableError`, new `Kind.AutoRepairEligible()` method), `internal/service/tool.go`
(`attemptRepair`'s gating logic, `buildAgentErrorEnvelope`'s payload), `docs/engineering/GLOSSARY.md`
(one new entry).

## Context

`docs/engineering/architecture/23-feedback-carrying-denial.md`'s target design: extend
`internal/recover`'s existing `RecoverableError{Kind, ToolName, SentArgs, SchemaURI,
ErrorPath, ErrorReason, Suggestion}` taxonomy with new policy-class `Kind` values —
illustrative in the doc: `KindPermissionDenied`, `KindCapabilityForbidden`,
`KindResultTooLarge`, `KindPolicyRefused` — "each explicitly marked ineligible for C2's
automatic LLM-repair loop." This task builds that shared foundation; tasks `02`-`05` each
construct and render one of these Kinds at their own real call site.

**Why extend `internal/recover` in place, not a sibling type** (doc 23's own first open
question). Traced this planning session, not assumed:

- `internal/service/tool.go:573`, `buildAgentErrorEnvelope(rec *recoverpkg.RecoverableError)
  string`, is the **one existing, already-correct JSON-envelope renderer** — `{"recoverable_error":
  true, "kind": ..., "tool": ..., "reason": ..., "suggestion": ..., "path": ..., "schema_uri":
  ...}` — used today by every real repair-bypass return in `attemptRepair` (lines 433, 437,
  443, 467, 499). `chat_tool_executor.go` (task `02`/`03`/`04`'s call sites) is the **same Go
  package** (`package service`) as `tool.go`, so it can call `buildAgentErrorEnvelope`
  directly — no export needed, no second renderer to build or keep in sync. A sibling type
  would either duplicate this function or need an adapter back to `*RecoverableError`; both
  are the exact "a value duplicated across call sites instead of one typed source of truth"
  anti-pattern this project's own review process is built to catch.
- The MCP surface (task `05`) is the one surface that already flows through
  `recover.Classify`/`Wrap`: `internal/mcp/manager.go`'s `ExecuteTool`/`ExecuteToolOnServer`
  return `ValidateResultSize`'s error as the tool-transport error, which reaches
  `internal/service/tool.go`'s `attemptRepair` (line 412) via `Execute` → `callTransport`
  (`tool.go:364-381`). `Classify()`'s own idempotent path — `errors.As(err, &rec); return
  rec.Kind` when `err` is already a `*RecoverableError` (`recover.go:169-172`) — means task
  `05` only has to construct a `*RecoverableError` with `Kind: KindResultTooLarge` directly
  at the point `ValidateResultSize` fails; `Classify()` needs **no new prose-matching
  branch** and **no import of `internal/mcp`** (which would invert this package's current
  dependency direction — `internal/recover` today only imports `internal/envelope` and
  `jsonschema`, a deliberately narrow, low-level package; `internal/mcp` is a large
  subsystem). A sibling type would need to reimplement this idempotent-recognition path
  itself.
- The other three surfaces (permission, human-reject, plugin-prehook) never call
  `recover.Classify`/`Wrap` at all, today or after this batch — they decide and render
  **before the tool transport is ever invoked**, inside `chat_tool_executor.go`'s
  tool-plan-building loop (confirmed: `internal/service/chat_tool_executor.go` lines
  155-277, all three denial branches `continue` out of the loop well before
  `executeToolBatch` — the function that eventually calls `Execute`/`attemptRepair` — ever
  runs). They construct a `*RecoverableError` directly with the right `Kind` already set and
  render it with `buildAgentErrorEnvelope`; whether `RecoverableError` is one type or two
  makes no difference to them, but reusing the one type means one shared method
  (`AutoRepairEligible`, below) and one shared render function serve all four surfaces
  uniformly, exactly matching doc 23's "one shape, reused everywhere a denial can talk back
  to the model."

**The real required change: split what `IsRecoverable()` currently conflates.**
`Kind.IsRecoverable()` (`recover.go:86`, `return k != KindNone && k != ""`) is the single
gate two functions in `tool.go` use:

- `attemptRepair` (`tool.go:412-421`): `kind := recoverpkg.Classify(origErr); if
  !kind.IsRecoverable() { return &ToolResult{Output: fmt.Sprintf("Error: %v", origErr),
  IsError: true} }` — then wraps, builds `rec`, and (if the three repair gates at lines
  431-445 all pass) calls `recoverpkg.Repair(...)` to attempt an LLM-based auto-repair retry.
- `classifyAndFormatToolError` (`tool.go:537-541`) — same `IsRecoverable()` gate, but this
  function is **not on the production path** (confirmed: its only call sites are in
  `internal/service/tool_recover_test.go`; `Execute` calls `attemptRepair` directly, never
  this function). No repair-attempt logic lives here at all — it only classifies and
  envelopes. No change needed to this function itself; it will pick up the new Kinds
  automatically once `IsRecoverable()` covers them, and it has nothing else to gate.

Today every existing `Kind` (`KindSchemaValidation`, `KindTypeCoercion`, `KindWrongCardType`,
`KindMissingOptionalField`, `KindFormatMismatch`) wants both "produce a structured envelope"
and "C2 may attempt an LLM repair" to be true together — so one boolean has sufficed. The
four new policy Kinds want the first (yes — a denial should still explain itself) and
explicitly not the second (doc 23: "no payload-shape repair can fix a policy denial, so C2
shouldn't burn a turn trying" — this is the design's "fail loudly" rule, restated, not
reopened). Simply adding the new Kinds to the enum without any other change would make
`IsRecoverable()` return `true` for all of them (since the check is just `!= KindNone &&
!= ""`), which would make them auto-repair-**eligible** in `attemptRepair` — the opposite of
what doc 23 requires, and a real, concrete bug if left unaddressed by this task.

## What to do

1. **`internal/recover/recover.go`** — add four new `Kind` constants: `KindPermissionDenied`,
   `KindCapabilityForbidden`, `KindPolicyRefused`, `KindResultTooLarge` (matching doc 23's
   illustrative names, mapped 1:1 onto tasks `02`-`05` — see this batch's README for the
   mapping and its rationale; if a name proves awkward once a task is actually implemented,
   document the deviation in that task's Work Log rather than silently diverging). Each gets
   a doc comment stating which surface produces it and, critically, that it is
   **auto-repair-ineligible** (cross-reference `AutoRepairEligible`, below) — mirror the
   existing constants' comment style (e.g. `KindSchemaValidation`'s comment at lines 48-53).
2. **Add `Source string` to `RecoverableError`** (`json:"source,omitempty"`) — the
   provenance tag doc 23 asks for ("each denial envelope carries a Source/origin tag (which
   policy rule, plugin, or subsystem produced it)"). Leave it optional/empty for the existing
   five schema-repair Kinds (nothing sets it today, nothing must start doing so as part of
   this task) — it's populated only by tasks `02`-`05`'s new call sites.
3. **Add `func (k Kind) AutoRepairEligible() bool`** — returns `false` for the four new Kinds,
   and for every other Kind, returns `k.IsRecoverable()`. Doc comment states plainly: this
   method is about *repair-eligibility*; `IsRecoverable()` keeps its existing meaning
   ("has a structured envelope") unchanged for every existing caller and every existing Kind.
   Do not change `IsRecoverable()`'s own logic or callers other than the one added below —
   its current two callers (`tool.go:414`, `tool.go:539`) both keep gating envelope-vs-flat-
   string on `IsRecoverable()` exactly as today.
4. **`internal/service/tool.go`'s `attemptRepair`** — after `rec` is obtained (right after
   the `errors.As` check at line 419-421, and after the existing "recoverable tool error
   classified" info log at lines 423-429 — keep that log for every recoverable Kind including
   the four new ones; operator-visibility of the classification is valuable regardless of
   repair-eligibility), insert a new check **before** the existing "Gate 1: env var bypass"
   check at line 432:
   ```go
   if !rec.Kind.AutoRepairEligible() {
       return &ToolResult{Output: buildAgentErrorEnvelope(rec), IsError: true}
   }
   ```
   This is the one functional change in this task: a policy-class `Kind` now gets the rich
   envelope (reason, suggestion, source) but is returned immediately, before any of the three
   existing repair gates (env var, `RepairConfig` wiring, `auto_repair_pref`) are even
   checked — no LLM repair call is ever attempted for it. Every existing Kind's behavior is
   byte-for-byte unchanged (their `AutoRepairEligible()` returns match their existing
   `IsRecoverable()` return, so this new check is always `false` — never trips — for them).
5. **`buildAgentErrorEnvelope`** (`tool.go:573-599`) — add one more optional field:
   `if rec.Source != "" { payload["source"] = rec.Source }`, mirroring the existing
   `reason`/`suggestion`/`path`/`schema_uri` optional-field pattern exactly.
6. **`docs/engineering/GLOSSARY.md`** — add one entry (near the existing **Recovery** entry,
   or as its own alphabetically-placed entry) disambiguating `RecoverableError.Source` (which
   subsystem/rule/plugin produced a denial envelope — e.g. `"permission_rule:no-write-glob"`,
   `"human_approval_reject"`, `"plugin:<plugin_id>"`, `"mcp_trust_tier"`) from
   `AgentReflex.ProvenanceTier` (`internal/agent/reflexes/telemetry.go:109` — a reflex-row
   trust/authorship tier gating the combining algorithm, a structurally unrelated concept
   that happens to share the word "provenance"). Follow this file's own stated discipline:
   "Prefer namespacing over inventing a new word" — both terms stay as-is (`Source`,
   `ProvenanceTier`), the entry just states plainly that they don't mean the same thing.
7. Unit tests: `AutoRepairEligible()` returns `false` for all four new Kinds and matches
   `IsRecoverable()` for every pre-existing Kind (including `KindNone`); a table test is
   the natural shape, mirroring `recover_test.go`'s existing style. Add/extend a test in
   `internal/service/tool_test.go` (or wherever `attemptRepair`'s existing gate tests live)
   asserting that a `*RecoverableError` with a new policy `Kind` returns
   `buildAgentErrorEnvelope`'s output verbatim from `attemptRepair` with **zero** calls into
   `recoverpkg.Repair` (a stub/mock `RepairConfig.Provider` that fails the test if invoked is
   the simplest way to assert "never attempted").

## Done means

- Four new `Kind` constants exist with doc comments; `RecoverableError.Source` field exists;
  `Kind.AutoRepairEligible()` exists and is tested against every Kind (old and new).
- `attemptRepair` short-circuits to `buildAgentErrorEnvelope(rec)` for any Kind where
  `AutoRepairEligible()` is `false`, before any of the three pre-existing repair gates run —
  verified by a test that would fail if a repair attempt were made.
- `buildAgentErrorEnvelope` includes `"source"` in its JSON payload when `rec.Source != ""`.
- Every pre-existing Kind's behavior in `attemptRepair`/`classifyAndFormatToolError` is
  unchanged — the existing test suite for both (`tool_recover_test.go` and whatever covers
  `attemptRepair` today) passes with zero modifications required to existing test
  expectations.
- One new GLOSSARY.md entry lands, disambiguating `Source` from `ProvenanceTier`.
- `go build ./cmd/nanite/`, `go vet ./...`, `go test ./...` pass.

## Work log
<Worker fills this in as it goes: what was actually done, any deviation from plan and why,
anything escalated.>

## Review notes
<Reviewer fills this in: pass/fail, what was checked, anything fixed and how.>
