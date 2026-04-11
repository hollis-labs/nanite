# Deep Review — Plugin System Execution Plan Evaluation

**Date:** 2026-04-10
**Reviewer:** `nanite-reviewer-backend` (code-review + go roles, deep-review skill)
**Scope slug:** `plugin-system-plan-eval`
**Release context:** first beta for developer friends. Severities as-written per the skill's beta calibration.

## Scope

Audit of the plugin-system execution plan at `docs/architecture/plugin-execution-plan-2026-04-10.md` (1108 lines). The plan claims to describe the current plugin system state, diagnose problems, and prescribe a re-architecture across ten tracks.

This review evaluates the plan along three dimensions:

1. **Plan accuracy** — does the plan correctly describe the current code?
2. **Plan soundness** — are the proposed solutions correct, idiomatic, and free of antipatterns?
3. **Plan completeness** — what edge cases, subsystems, and lifecycle scenarios does the plan miss?

Plus a secondary "collateral" category for plugin-system code issues noticed while verifying plan claims.

### Plan doc verified

- `docs/architecture/plugin-execution-plan-2026-04-10.md` — read end to end (1108 lines)

### Nanite code read in full (to verify plan claims)

- `internal/plugin/host.go` — full (1188 lines)
- `internal/plugin/loader.go` — full (270 lines)
- `internal/plugin/triggers.go` — full (195 lines)
- `internal/plugin/events.go` — partial (focus on EmitEvent and dispatcher)
- `internal/plugin/subprocess/plugin.go` — full (431 lines)
- `internal/plugin/subprocess/manager.go` — full (378 lines)
- `internal/plugin/subprocess/protocol.go` — full (232 lines)
- `internal/plugin/subprocess/transport.go` — full (152 lines)
- `internal/plugin/catalog.go` — first 40 lines + grep for callers
- `internal/plugin/signature.go` — first 30 lines
- `internal/plugin/manage.go` — full (73 lines)
- `internal/plugin/allplugins/allplugins.go` — full
- `internal/plugin/scaffold/templates/plugin.go.tmpl` — full
- `internal/plugin/builtin/giphy/plugin.go` — full (165 lines)
- `internal/chat/commands.go` — partial (Register/Execute path)

### Files sampled

- `internal/mcp/self_tools_transport.go` — grepped for fragments-engine envelope emission sites (plan cite verified at L578, L701)
- `plugins/` tree — `ls` only, to verify drift claims
- `internal/plugin/builtin/` tree — `ls` only

### Files NOT read (explicit blind spots)

- `ui/src/lib/plugin-loader.ts` — Track D rewrites this; plan accuracy for frontend is assumed
- `scripts/generate-plugin-imports.mjs` — Track D.3 changes; not verified
- `cmd/nanite/plugin_cmd.go` — Track G.6 additions; not read
- `framework/libs/go-plugin/` — the SDK being replaced; not inventoried
- `internal/api/plugins.go` — install API surface; not read
- `docs/architecture/plugin-audit-2026-04-10.md` (the superseded audit) and `plugin-envelope-emission-findings-2026-04-10.md` — read-for-context, not audited

## Methodology

Followed the six-step methodology in `~/.nanite/skills/deep-review.md`, adapted for a plan audit:

1. **Understand the scope.** Read the plan end to end. Mapped each load-bearing claim (file:line cites, sharp-edges, track dependencies) for verification against the code.
2. **Enumerate concerns by category.** Primary categories were plan accuracy / plan soundness / plan completeness / plan sequencing, plus a "collateral" category for non-plan issues surfaced during verification. Standard Go categories (concurrency, error handling, resource management) applied as sub-filters.
3. **Investigate with evidence.** For every plan claim that cited `file:line`, I opened the file and verified. For every proposed change, I asked "does this actually solve the identified problem?" and looked for better/simpler/safer alternatives. For every "something is missing" concern, I verified the gap in code before filing.
4. **Classify severity strictly** using the skill's rubric, beta-calibrated. When between two levels, erred one step lower per reviewer-context guidance ("prioritize ship blockers, not nitpicks").
5. **Write findings** — one file per topic cluster. Collateral items grouped into `12-low-collateral-observations.md`. Info/praise grouped into `13-info-observations-and-praise.md`. Both grouped files stay under the 300-line soft cap.
6. **Assemble the index** — you're reading it.

### Tooling NOT run in this pass (deliberate)

Per the skill's scoped-review tooling-deferral rule, this audit is a plan evaluation with code verification, not a full tooling pass. The following commands were **not** run and should be run as a follow-up scope:

- `go vet ./...`
- `go test -race ./internal/plugin/...`
- `golangci-lint run --new --timeout 30s`
- `staticcheck ./internal/plugin/...`
- `errcheck ./internal/plugin/...`
- `govulncheck ./...`

**Recommended follow-up scope:** `2026-04-1N-plugin-tooling-and-tests`. Run all the above against the plugin system, then merge findings with whatever remains of this audit's recommendations.

### Invariants honored

- All findings cite `file:L<start>-<end>` or `file:line`
- No code changes
- No plan doc changes
- Pre-existing known issues from `reviewer-backend.md` not re-flagged (scaffold imports are mentioned as a plan-accuracy confirmation, not a fresh finding; envelope emission is mentioned as context only)
- No sandbox-hardening re-flags (separate audit next door handles that)
- No frontend, chat engine, MCP tool, provider, or store findings beyond the thin plugin-system intersections

## Findings

### By severity

**Critical (1)**
- [01 — UnloadPlugin deadlock missed by plan](01-critical-unloadplugin-deadlock-missed-by-plan.md)

**High (6)**
- [02 — Subprocess transport serialization blocks RPC proliferation](02-high-transport-serialization-blocks-rpc-proliferation.md)
- [03 — Transport timeout kills plugin connection permanently](03-high-transport-timeout-kills-connection-permanently.md)
- [04 — Event hook panic crashes the host](04-high-event-hook-panic-crashes-host.md)
- [05 — Unregister systemic gap understated by plan](05-high-unregister-systemic-gap-understated.md)
- [06 — Plan ignores existing catalog/signature code](06-high-existing-catalog-signature-code-ignored.md)
- [07 — Beta scope too large for release window](07-high-beta-scope-too-large-for-release.md)

**Medium (4)**
- [08 — Track A sequencing risk (cleanup + bugfixes + removal bundled)](08-medium-sequencing-cleanup-must-precede-rearchitecture.md)
- [09 — Testing is an afterthought](09-medium-testing-is-an-afterthought.md)
- [10 — Lifecycle completeness and resource limits](10-medium-lifecycle-completeness-and-resource-limits.md)
- [11 — Trust and install security gaps](11-medium-trust-and-install-security.md)

**Low (1)**
- [12 — Grouped collateral observations (10 items)](12-low-collateral-observations.md)

**Info (1)**
- [13 — Plan praise, observations, blind spots, skill shakedown](13-info-observations-and-praise.md)

### By topic

#### Plan accuracy (does the plan match reality?)

- [01 — UnloadPlugin deadlock missed by plan](01-critical-unloadplugin-deadlock-missed-by-plan.md) — plan cites Shutdown but misses UnloadPlugin with same pattern
- [06 — Plan ignores existing catalog/signature code](06-high-existing-catalog-signature-code-ignored.md) — plan treats existing infra as greenfield
- [12 L1 — Protocol version check citation off by a few lines](12-low-collateral-observations.md)
- [13 I5 — Plan references findings #1–#6 that are not in the repo](13-info-observations-and-praise.md)

#### Plan soundness (are proposed solutions correct?)

- [02 — Transport serialization bottleneck in proposed RPC model](02-high-transport-serialization-blocks-rpc-proliferation.md)
- [03 — Timeout-kills-connection is baked into current design](03-high-transport-timeout-kills-connection-permanently.md)
- [05 — Unregister work understated; interface-change approach is breaking](05-high-unregister-systemic-gap-understated.md)
- [11 — Install trust model has gaps](11-medium-trust-and-install-security.md)

#### Plan completeness (what's missed?)

- [04 — Panic recovery at event dispatch not in plan](04-high-event-hook-panic-crashes-host.md)
- [05 — 11 of 14 registration categories have no cleanup path](05-high-unregister-systemic-gap-understated.md)
- [09 — No test plan beyond "go test passes"](09-medium-testing-is-an-afterthought.md)
- [10 — Lifecycle edges (panic in Init/Unload, double-Unload, resource limits)](10-medium-lifecycle-completeness-and-resource-limits.md)
- [11 — Permission model / capability manifests / transparency log](11-medium-trust-and-install-security.md)

#### Plan sequencing and risk

- [07 — Beta scope too large; intermediate state breakage likely](07-high-beta-scope-too-large-for-release.md)
- [08 — Track A sub-ordering creates uncommittable tree](08-medium-sequencing-cleanup-must-precede-rearchitecture.md)

#### Collateral (plugin code issues surfaced during verification)

- [01 — UnloadPlugin deadlock (also a collateral code bug, plan-wise it's a gap)](01-critical-unloadplugin-deadlock-missed-by-plan.md)
- [04 — Missing panic recovery (also a collateral code bug)](04-high-event-hook-panic-crashes-host.md)
- [12 — Grouped collateral observations (10 items, L1–L10)](12-low-collateral-observations.md)
  - L1: Plan line citation drift
  - L2: Dead `connectorOwners` ownership map
  - L3: `nextID` atomic but unused
  - L4: `SetPluginConfig` public-no-consumer
  - L5: Trigger dispatch sync work before goroutine
  - L6: `time.Sleep` in restart loop blocks shutdown
  - L7: `ringBuffer` has concurrent-write race
  - L8: `parseEntrypoint` doesn't handle quoted args
  - L9: `allplugins.go` vs `core_plugins.yaml` drift risk
  - L10: No command name collision detection across plugins

## Recommended next steps

Priority order:

### Before beta ships (blocker subset)

1. **Fix finding 01** — `UnloadPlugin` deadlock. Small diff, high impact. Belongs in Track A.1/A.2 of a narrowed "beta-blocker" plan.
2. **Fix finding 04** — Add `recover()` to `EmitEvent` goroutines, `TriggerDispatcher.Dispatch`, and `sendWithRetry`. Prevents any plugin panic from crashing the host. ~30 lines.
3. **Triage finding 02 + 03 together** — decide whether to do the full concurrent-transport rework (recommended) or a smaller "don't kill the transport on timeout" fix for now. The smaller fix unblocks beta reliability; the bigger rework is prerequisite for the plan's RPC expansion.
4. **Narrow the beta scope** per finding 07. Split this plan into a beta-blocker subset (Track A + findings 01/04 + narrow transport fix + envelope emission workaround) and a post-beta rearchitecture plan (Tracks B–J).
5. **Restructure Track A** per finding 08 so each sub-track has its own gate and commit point.

### Before starting the full rearchitecture (if user decides to ship it post-beta)

6. **Implement finding 05** (ownership backfill + Unregister methods) BEFORE Track B.6 lands. The plan's current B.6 sharp-edge is wrong about which piece is hard.
7. **Write the test plan** per finding 09. Commit to concurrent transport tests, panic-recovery tests, fuzz tests on the JSON-RPC reader, and a register-then-unregister test per category.
8. **Pre-execution inventory** per finding 06. List every file in `internal/plugin/` and its disposition (keep/rewrite/delete/move) so execution agents don't rediscover the catalog and signature code mid-track.
9. **Lifecycle hardening** per finding 10. Recover in Load/Unload, bounded shutdown time, larger stderr ring buffer, basic resource limits.
10. **Trust model polish** per finding 11. Build-tag-gated dev mode, trust-downgrade warning on update, first-install confirmation UX.

### Follow-up audit passes to schedule

- **`plugin-tooling-and-tests`** — run the deferred tooling commands (go vet, -race, golangci-lint, staticcheck, errcheck, govulncheck) on the plugin system.
- **`plugin-frontend-loader`** — focused review of `ui/src/lib/plugin-loader.ts`, importmap setup, subscription patterns. Covers Track D blind spot.
- **`plugin-trust-boundary`** — red-team pass on "what can a malicious in-process plugin do" with the current host API. Companion to the sandbox-hardening audit.

## Known issues skipped

Pre-existing items in `reviewer-backend.md` "pre-existing known issues — DO NOT re-flag" list, not re-flagged here:

- Beta known issues (`docs/beta-known-issues.md`) — all P0 closed as of 2026-04-10; P1 empty
- Plugin framework limitations from `plugin-dev.md` §Known Limitations — including scaffold template imports (mentioned only as plan-accuracy verification in this audit, not as a fresh finding), `adapter-opencode` format unverified, `oembed` envelope path dead, fragments-engine 503s, `Host.Shutdown()` mutex deadlock (plan already covers; I extended to `UnloadPlugin`), missing `Host.RegisterEnvelopeType` on SDK, plugin isolation gap, HTTP route leak on unload
- Engine backlog BLG-20260410-001..004
- Deliberate architecture items: http.ServeMux no route removal, migrations DDL-only vs seed, two-binaries footgun, `.agentrc/` vs `.nanite/` drift, broken `.claude/commands` symlinks, `shadcn-ui` config typo

Sandbox-hardening audit (`docs/audits/2026-04-10-sandbox-hardening/`) findings are also skipped — separate scope, tracked next door.

## Noticed but out of scope

Observations made while traversing the plugin system that fall outside the plan-evaluation scope. Logged here so they don't vanish and can feed future scope selection.

- **`internal/plugin/auto_triggers.go` and `auto_triggers_test.go`** — the plan never mentions these files once. They define something called "auto triggers" that presumably interacts with the trigger dispatcher. Disposition in the rearchitecture is unknown. Suggested follow-up scope: include in the `plugin-tooling-and-tests` pass or as a one-hour "what does this file do and does it stay" archaeology pass.

- **`framework/libs/go-plugin/`** — the SDK module the plan wants to delete in Track I.1. I did not inventory its contents, so plan claim "contents split between plugin-sdk and nanite/pkg/plugin" is unverified. Recommended: read this module before executing Track C or I.

- **`internal/chat/commands_builtin.go`** — built-in chat commands that likely interact with `CommandRegistry`. Track B.12 requires touching this file for envelope propagation but the exact surface isn't mapped in the plan. Not a finding; just an execution-time surprise waiting.

- **`internal/api/plugins.go`** — the plugin install HTTP API. Plan §reviewer-context mentions "plugin install is a privilege boundary." I did not read the current handler. Privilege-boundary review would be a sub-scope of `plugin-trust-boundary`.

- **`internal/store/` schema for plugins** — the plan's yaml-authoritative registration implies persistence for enabled/disabled state. Current `manage.go` uses file-based `plugin.yaml.disabled` rename. If the plan is intended to move this to the store, it doesn't say so. Not a finding; a design ambiguity worth resolving before Track G.

- **`config/envelopes.yaml` existence and purpose** — the plan mentions this in Track A.3's fragments-engine sweep but I didn't inspect whether it exists or what else it holds. Worth a quick check before executing A.3.

- **`internal/plugin/catalog_test.go` and `signature_test.go`** — these tests exist for code the plan treats as greenfield (finding 06). What do they test? If they cover paths the rearchitecture will change, they need to be updated or they fail as soon as Track B lands. Not audited here.

- **`scripts/generate-plugin-imports.mjs`** — Track D.3 wants to modify it. Not read. Assumed to exist and do what the plan says.

- **Cross-platform behavior (Windows / Linux)** — I'm running on darwin. The plan's sharp edges §13.7-8 cover Windows SysProcAttr and os.Rename, but I didn't verify the current state of either on a non-darwin host. If the execution sessions are also darwin-only, Windows regressions won't be caught until much later.

- **`ui/src/components/chat/envelopes/`** — the plan's A.3 lists envelope components to delete. I didn't verify which ones currently exist. Frontend is out of scope per the reviewer-context.

## Skill shakedown notes (second real run)

Observations for the skill maintainer. This was the second real invocation of `deep-review.md` since the sandbox-hardening audit added a first round of refinements.

1. **The refined By severity / By topic link-list layout works well.** Much more readable than the wide table from the sandbox audit. Easy to eye-scan. Recommend keeping exactly as-is. (First real feedback from the new layout.)

2. **Release-context calibration clarity could improve.** The skill's "beta = as-written" is clear in intent, but several findings here sit at the edge between High and Medium because the "normal use" threshold is fuzzy for a plan-audit context (the plan isn't yet shipped code). Recommend adding a line to the beta calibration: "For plan/design audits at beta: severity reflects the risk of shipping the plan as-written, NOT the severity of a bug that already exists in code." Right now I used that interpretation implicitly but the skill doesn't sanction it.

3. **The scoped-review tooling-deferral rule is clear and useful.** I used it to defer the tooling pass and flag a follow-up scope. Worked cleanly.

4. **Plan audits are a different shape than code audits.** The skill categories (security, concurrency, idioms, antipatterns) map awkwardly to a plan doc. I ended up using plan-accuracy / plan-soundness / plan-completeness / plan-sequencing as my primary categories and the Go categories as sub-filters. Recommend the skill explicitly mention "plan/design audit" as a scope shape in §scope-parameter with a hint that the topic categories become plan-focused in that mode.

5. **Finding-file numbering is fine after the first-pass writes.** I wrote in rough severity order and ended up with 01 Critical, 02–07 High, 08–11 Medium, 12 Low grouped, 13 Info grouped. Gap-free. Didn't have to rename anything. The refined "rename ritual" section in the skill is reassuring even though I didn't need it this time.

6. **Grouped files soft cap (~300 lines).** Finding 12 (collateral observations) is 10 items at about 190 lines. Under cap. Good rule. Would have needed splitting if I'd captured all the observations I noticed but chose not to file.

7. **Missing: "auditing a plan that references external context the reviewer doesn't have."** The Nanite plan explicitly says "Read those findings in the conversation history for the reasoning trail." I couldn't. The skill should have a line telling reviewers how to handle this: verify what you can, flag the rest as an I-finding ("plan references material not in the repo"), don't guess at the missing content. I did this in finding 13 I5 but it felt ad hoc.

8. **"Noticed but out of scope" section filled up fast in this audit.** About 10 items. Still under the 15-item threshold the skill sets for moving to an appendix. Worked well in the index. If a future audit exceeds 15, appendix file would be the right call — agreed with the skill's guidance.

9. **"Things the user should verify" is not a section in the skill's index contract, but the invoker's prompt asked for it.** I put it in the report, not the index. The skill might want to add "reviewer confidence markers / verification requests" as an optional section for audits where the reviewer couldn't confirm every claim. Useful here because several findings (especially 09's test recommendations) depend on assumptions the user needs to confirm.

10. **One more: the invoker's prompt and the skill have some redundancy about methodology and guardrails.** Not a skill problem per se, but the invoker/skill overlap made it slightly ambiguous whose instructions to follow when they differed (they didn't really differ here, but they could). Suggest the skill include a "if the invoker's prompt includes methodology, the invoker wins" clause for clarity.
