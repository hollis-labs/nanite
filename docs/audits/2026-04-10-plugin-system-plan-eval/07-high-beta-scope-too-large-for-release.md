# [High] Plan scope is far too large for the beta release window — significant risk of shipping nothing

**Scope:** plan sequencing and risk
**Topic:** plan-soundness / release context
**Date:** 2026-04-10

## Problem

The plan proposes a complete re-architecture of the plugin system — new SDK module, new public plugin types package, yaml-authoritative loader rewrite, concurrent transport expansion (implied), frontend dynamic import with importmap + es-module-shims, Cloudflare R2 + Pages catalog infra, Ed25519 signing pipeline, shared shadcn primitives via importmap, hot install/uninstall with atomic rollback, plus migrating three existing plugins (giphy, oembed, support-ticket) to the new architecture — all framed as "what needs to ship for beta." The reviewer-context file says "this is not a 'clean up all the lint' pass" and "prioritize ship blockers." This plan is the opposite: it's a clean-up-everything pass. Each individual track is defensible; the aggregate is at least 8–10 full execution sessions per the plan's own estimate (§17), plus integration debugging, plus the findings this audit is filing. Shipping all of it before beta is high-risk, and even finishing half of it will leave the system in a broken intermediate state because the tracks have hard dependencies.

## Evidence

Track dependency graph from plan §2:

```
A → B, C, F (parallel)
B+C → D → E → G (install flow) → H (other plugins)
E → H
H → I → J
```

Every track is a blocker for a later track. There is no "minimum viable subset" called out. If any track is incomplete, the whole thing is incomplete, because:

- B (host core changes) rewrites the loader to be yaml-authoritative; builtin plugins then **depend on** the new loader and break if you roll back. Every plugin has to be updated simultaneously.
- C (SDK) requires a coordinated flip of the wire types (B-3, plan §C.3 "Sharp edge: this is a coordinated flip"). If C isn't done, B.10's new method constants can't land.
- D (frontend rewrite) deletes the current `plugin-loader.ts` build-time codegen path. Rolling back D means rolling back B.7's registry endpoint and B.8's events endpoint, because frontend consumers are downstream of both.
- E is the first end-to-end integration. Its gate is the first "proof of architecture" per the plan. Expect it to surface bugs that feed back into B, C, D — the plan acknowledges this ("Expect to find bugs here that weren't visible in isolated work").
- G depends on F's Cloudflare infra, which depends on owning a domain, generating keys, setting up CI — work that isn't purely coding.
- H migrates three plugins, each of which the plan calls substantial. Support-ticket alone is estimated at 2–3 execution sessions.

The plan's own session estimate (§17): "Total: ~8–10 focused sessions, some in parallel." In practice, software plans of this size routinely 2× their estimate. Call it 16–20 sessions with integration debugging. At one focused session per day, that's 3–4 weeks of uninterrupted work to ship the plugin rearchitecture. Even assuming the user wants to slip the beta date, the scope is "major release" territory, not "beta polish."

Meanwhile, the sandbox-hardening audit filed Criticals and Highs that must ship before beta per the same reviewer-context. Those are not in this plan. The chat engine, MCP transports, store layer — none of those are in this plan either. The reviewer-context lists them as higher-priority than the plugin system for security-sensitive paths. The plugin system is priority #1 in the context file, but the same file explicitly frames it as "plugin reliability must be 100%" — meaning "no crashes" more than "perfect architecture."

## Impact

Three bad outcomes are possible:

1. **Scope creep delays beta indefinitely.** Plan eats every session until the user ships whatever exists at the deadline, with half the tracks done, and the system is in a broken intermediate state (see below).

2. **Intermediate-state breakage.** Tracks B, C, D, E all have to land for the new model to work. If the user runs out of time at, say, Track D, the host has the new yaml-authoritative loader but the frontend is still on the old model. Envelope rendering breaks for every existing plugin. Beta ships with a regression worse than the current state.

3. **Beta-blocker findings deprioritized.** This audit's findings 01 (UnloadPlugin deadlock), 02 (transport serialization), 03 (timeout kills connection), 04 (panic crashes host), 05 (unregister gaps), and the pre-existing sandbox Criticals/Highs all deserve attention before a beta for developer friends. If execution time is eaten by the rearchitecture, these ship-blockers don't get fixed.

## Recommendation

**Carve out a beta-blocker subset.** Split this document into two:

**`plugin-beta-blockers-2026-04-10.md`** — ships before beta. Scope:

1. Track A (drift cleanup + A.2 P0 fixes + A.3 fragments-engine removal). **~1 session.**
2. Finding 01 — UnloadPlugin deadlock fix. **~0.5 session.**
3. Finding 04 — panic recovery at event dispatch boundaries. **~0.5 session.**
4. Finding 02 + 03 — concurrent transport + timeout-doesn't-kill-connection. Can be scoped tighter as "transport hardening" without the full RPC-proliferation rework. **~1–2 sessions.**
5. Envelope emission fix for giphy/oembed (the current reason `!giphy` ships broken) — smallest surgical fix that unblocks the demo, NOT the full "envelope types are yaml-authoritative" rework. **~1 session.**
6. Sandbox Criticals/Highs from the sibling audit. **Per sibling audit's estimate.**

**`plugin-rearchitecture-2026-post-beta.md`** — everything else in the current plan. Track B's yaml-authoritative loader, C's SDK module, D's frontend rewrite, E's giphy-as-subprocess integration, F's catalog infra, G's hot install/uninstall, H's plugin migrations, I's cleanup, J's polish and shared primitives. This becomes the roadmap for the release after beta (call it 0.2 or 0.3).

Reversibility note: the beta-blocker subset is individually reversible. Each fix is a localized diff. The rearchitecture plan is not reversible — once you start deleting the old loader code (Track I), you cannot roll back to the pre-rearchitecture state without reverting hundreds of files.

**Also recommend:** do NOT start Track B until the beta-blocker subset is merged. Track B rewrites the loader; if something in it breaks, you don't want to also be debugging beta-blocker fixes on top of it.

## Release-context note

The reviewer-context frames this as the release severity calibration:

> **Beta** — severities as-written. **Critical** means "exploitable in the wild by a motivated attacker" or "data loss risk." **High** means "will be hit in normal use and is hard to diagnose."

By that rubric, the rearchitecture plan itself is not a finding — plans aren't bugs. But the *decision to execute the rearchitecture before beta* is a risk that should be recorded. I'm filing it as High because it's very likely to consume the runway and leave the user in worse shape than if they'd shipped a narrower fix set.

## References

- Plan §2 — track dependency graph
- Plan §17 — session estimate (8–10)
- Plan §16 — "done-ness checklist" (17 items, most requiring multiple tracks to be complete)
- Reviewer-context `reviewer-backend.md` — "this is not a 'clean up all the lint' pass" / "ship blockers and high-impact issues"
- This audit's findings 01–05 — the minimum-viable fix set
- Sandbox audit `docs/audits/2026-04-10-sandbox-hardening/index.md` — other beta-blockers not in this plan
