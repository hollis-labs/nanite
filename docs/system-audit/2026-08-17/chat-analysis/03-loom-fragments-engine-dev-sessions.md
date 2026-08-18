# Loom & Fragments Engine Dev-Session Evidence: Nanite Agent-System Problems

Evidence-gathering only. Not a code review or a set of recommendations. All incidents below are drawn from Claude Code session transcripts recorded while working *in* the Loom and Fragments Engine (FE) repos — i.e. from the perspective of two consumers of Nanite's durable-agent system, not from work done inside the Nanite repo itself.

**Date range covered:** 2026-08-16 14:57 UTC through 2026-08-17 22:57 UTC.

**Apps:** Loom (`/Users/chrispian/dev/hollis-labs/apps/loom`) and Fragments Engine (`/Users/chrispian/dev/hollis-labs/apps/fragments-engine`).

## Session files reviewed

**Loom** (`~/.claude/projects/-Users-chrispian-dev-hollis-labs-apps-loom/`):
- `629d4e19-6417-41b2-8f94-14ac7c5d183e.jsonl` — 2026-08-16 16:13–17:26 UTC (initial Ion→Loom gap analysis + sprint planning)
- `937bab53-deeb-4dd5-9478-d0d5bb532a33.jsonl` — 2026-08-16 17:24 through 2026-08-17 22:57 UTC (main session; backend sprint execution + the full Fragments Engine → Nanite Curator → Loom pilot-integration debugging saga)
- Plus ~20 `subagents/agent-*.jsonl` Task-tool transcripts spawned from the two sessions above (implementer/verifier/research subagents for individual Torque tasks; consulted for corroborating detail, not cited independently unless they add new information)

**Fragments Engine** (`~/.claude/projects/-Users-chrispian-dev-hollis-labs-apps-fragments-engine/`):
- `7d748c54-2598-4a4b-a842-d862aa746d52.jsonl` — 2026-08-16 14:57–2026-08-17 01:09 UTC
- `b255665b-52cc-4073-8f11-fdb5f03a90fa.jsonl` — 2026-08-16 15:47–17:28 UTC
- `047f9262-f983-428d-9128-33056aa21a79.jsonl` — 2026-08-16 21:23–22:23 UTC
- `79d59e35-1100-4439-a022-495822af1821.jsonl` — 2026-08-16 22:47–23:21 UTC
- `e66eb21a-e8f6-4118-9439-b7926c95d7dd.jsonl` — 2026-08-16 23:21–2026-08-17 00:47 UTC
- `19106f2f-f27b-454e-a53e-4863f3e26a0f.jsonl` — 2026-08-17 16:31–22:57 UTC (MCP catalog setup + FE-side path bug + stale-proxy debugging, overlapping the Loom saga above)
- Plus `subagents/agent-*.jsonl` Task-tool transcripts under several of the above.

Both apps were, during this window, jointly building the "Loom Wiki Pilot": Fragments Engine ingests/routes/dispatches, Loom hosts wiki content + a compile API, and a new pair of Nanite durable agents (**Curator**, a `class: process` compile-on-wake agent, and **Weaver**, a `class: advisor` query agent) were meant to sit between them. This made both repos unusually direct, sustained consumers of Nanite's durable-agent runtime, MCP tool surface, and Cerberus/agent-mux deployment machinery — which is the source of nearly all findings below.

---

## Setup / config friction

### Nanite MCP server failed to connect during FE's own catalog setup
**App:** Fragments Engine · **File:** `19106f2f-f27b-454e-a53e-4863f3e26a0f.jsonl` · **~2026-08-17T16:32–16:35 UTC**

While registering FE/Loom into the shared MCP catalog, the `nanite` upstream server itself came back `"status":"failed","error":"initialize: transport error: transport closed"`, and on a subsequent refresh, `"errors":{"nanite":"client not initialized"}` / `"error":"refresh list tools: client not initialized"`. This was Nanite's own MCP stdio server failing to come up cleanly during a routine catalog refresh triggered from a consumer session, not an error in FE/Loom's own config.

### `native_flat` vs `proxy_only` is a per-consumer, hard-to-locate setting
**App:** Loom · **File:** `937bab53-deeb-4dd5-9478-d0d5bb532a33.jsonl` · **~2026-08-17T16:48–16:58 UTC**

Whether an upstream MCP server's tools appear directly in a consumer's tool list (`native_flat`) or require indirection via `mux_discover`/`mux_call` (`proxy_only`) is controlled by a **per-consumer allowlist** that is not part of the server's own registration. For interactive CLI sessions it's a `--servers` launch flag; for Nanite's durable-agent runtime specifically, it turned out to be a **database setting**, not a config file — something neither the Loom-side engineer nor (initially) the operator could locate without direct investigation:

> "What decides which mode a consumer sees is a per-session setting, not a property of the server itself... Every Mux-consuming process — including Nanite's own durable-agent runtime, which is a completely separate consumer from my session — has its own equivalent allowlist, and `loom`/`fragments-engine` simply aren't on Curator's."

Registering a new MCP server for a Nanite durable agent to consume required cross-referencing agent-mux's source directly (`cmd/mux/mcp.go`) to confirm that an *empty* `--servers` value (not an explicit list) flattens everything, and even then the fix didn't take effect until the correct process was restarted (see Integration-specific pain, below).

### agent-mux MCP catalog has no live reload
**App:** Loom · **File:** `937bab53-deeb-4dd5-9478-d0d5bb532a33.jsonl` · **~2026-08-17T01:27 UTC**

Registering Loom as a new upstream MCP server (`~/.agent-mux/catalog/mcp-servers/loom.yaml`) only took effect for *new* Mux connections — sessions already connected (including, later, Curator's own) kept running against the stale catalog snapshot from their own process start, with no indication this had happened short of manually checking tool counts.

---

## Tool-calling issues

### Curator's role-defined tool names didn't match FE/Loom's real tool names
**App:** Loom · **File:** `937bab53-deeb-4dd5-9478-d0d5bb532a33.jsonl` · **~2026-08-17T16:46–19:27 UTC**

Curator's role/procedure referenced tools by a `fragments_*`/`loom_*` prefix convention, but FE's actual registered tools are verb-first with no server prefix (`search_fragments`, `get_fragment_detail`). Nanite's own investigation initially called this mismatch "cosmetic," but the Loom-side engineer's live testing showed it was a real, blocking gap until corrected:

> "Curator's seeded tool references (`fragments_*`, etc.) don't match the real verb-first tool names (`search_fragments`, `get_fragment_detail`)... My read: the naming mismatch the Nanite agent called 'cosmetic' is probably the actual remaining blocker now that the binding regression is fixed."

### Curator's fallback exploration triggered a live, reproducible panic in Nanite's own tool
**App:** Loom · **File:** `937bab53-deeb-4dd5-9478-d0d5bb532a33.jsonl` · **2026-08-17T16:46:18Z**

Unable to resolve its intended tools, Curator fell back to generic `dev_bash`/`dev_grep` exploration and, while flailing, hit "a real, reproducible bug in Nanite's own tooling — a panic in `DevToolsTransport.callGrep` (`runtime error: integer divide by zero`, `internal/mcp/dev_tools.go:821`, during a `filepath.Walk`). Recovered gracefully (`safego`), didn't crash the daemon, but it's a genuine bug worth fixing." **This bug is still present in the current Nanite codebase**: `internal/mcp/dev_tools.go:821-822` computes `idx := (j - ringStart) % ringLen` before the `if ringLen == 0 { break }` guard on the following line, so a zero-length ring still divides by zero.

### Total tool-resolution regression after an MCP visibility fix
**App:** Loom · **File:** `937bab53-deeb-4dd5-9478-d0d5bb532a33.jsonl` · **2026-08-17T17:55Z**

After the operator changed Nanite's Mux `--servers` DB setting to flatten all upstreams (intended to fix the `loom`/`fragments` visibility gap above), the very next Curator wake regressed further: even generic tools that had previously resolved and failed on their own merits (`dev_bash`, `dev_grep`, `dev_read`) started returning `"unknown MCP tool"` outright.

> "This is actually a regression from where we were before the tool-discovery fix, not the same blocker in a new form... Whatever changed with the progressive-discovery DB setting seems to have affected *all* tool resolution for this session, not just fixed the `loom`/`fragments` visibility gap."

### `get_fragment_detail` query-construction failure
**App:** Loom · **File:** `937bab53-deeb-4dd5-9478-d0d5bb532a33.jsonl` · **2026-08-17T19:27Z (report), T21:28 (Curator's own report)**

Once tool binding and naming were both fixed, Curator's actual call to `get_fragment_detail` failed on a fragment that demonstrably existed (see Hallucinations, below) — "wrong endpoint, wrong parameter, wrong path construction, something along those lines," per the Loom-side engineer's assessment before the true cause (a stale MCP proxy subprocess) was identified.

---

## Hallucinations

### Curator conflated the old Ion Python app with the current Loom Go service
**App:** Loom · **File:** `937bab53-deeb-4dd5-9478-d0d5bb532a33.jsonl` · **2026-08-17T21:28–21:31 UTC**

Curator's own "FAILURE REPORT: Loom Curator Smoke Test 5" stated: *"Fragment ID not found in Ion/Loom database files"* and that it *"Attempted curl to localhost:8080/api - connection refused."* The Loom-side engineer, who had been directly operating the real service all session, identified both claims as factually wrong, not just unhelpful:

> "Curator is treating the **old Ion Python codebase** as 'Loom.' That's wrong — Loom is the Go rewrite, and it's a completely different, currently-running service... 8080 is the README's *dev-mode* default..., not where Loom actually runs. The real, live, Cerberus-managed instance is on **port 8092**... Curator never had the actual endpoint; it was guessing from stale documentation instead of being told the concrete URL."

The root cause was traced to `docs/architecture.md` in the Loom repo still being a verbatim copy of the pre-implementation planning doc (oriented around Ion, marked "draft, not yet implemented" despite the real backend being built and running) — apparently the only thing Curator had actually read.

### Curator's "fragment doesn't exist" diagnosis was independently verified false
**App:** Loom · **File:** `937bab53-deeb-4dd5-9478-d0d5bb532a33.jsonl` · **2026-08-17T22:20–22:21 UTC**

On "Loom Pilot Smoke Test 7," Curator reported: *"The fragment cannot be found in the Fragments Engine store... Classification: FAILED... The fragment ... does not exist in the fragment store and cannot be compiled..."* and speculated about deletion, timing, or DB connectivity. The Loom-side engineer queried the same fragment via both FE's HTTP API and its CLI within seconds and found it fully present with entities, route log, and relations intact:

> "So Curator's diagnosis is factually wrong on the 'doesn't exist' front... 'not found' isn't the same as 'doesn't exist' — this is a query-construction bug, not a data problem."

(Root cause, confirmed later: a stale Mux/Tether MCP proxy subprocess for Curator's session was still running pre-fix code against a phantom empty database — see Integration-specific pain.)

### Underlying FE bug that produced confident-looking false negatives
**App:** Fragments Engine · **File:** `19106f2f-f27b-454e-a53e-4863f3e26a0f.jsonl` · **2026-08-17T22:00–22:03 UTC**

Separately (and prior to the stale-proxy incident above), FE itself had a real bug that would make any consumer's "fragment not found" queries look plausible without any error signal: `database.path: ./data/fragments-engine.db` in `fragments.yaml` is relative, and `config.ExpandHome` only strips a leading `~`, so the effective DB path resolved against whatever CWD the spawning process happened to have. Because `store.Open()` runs `MkdirAll` + migrate before opening, a wrong CWD didn't error — it silently created a brand-new, fully-migrated, **empty** SQLite database and served from that with zero fragments in it. This was fixed in FE (`internal/config/config.go`, anchoring relative paths to the config file's directory) but is worth noting as a mechanism by which a durable agent querying through a misconfigured connection would receive confident, well-formed "not found" answers with no indication anything was wrong.

---

## Steering issues

### Explicit user acknowledgment of a recurring, unresolved pattern
**App:** Loom · **File:** `937bab53-deeb-4dd5-9478-d0d5bb532a33.jsonl` · **2026-08-17T21:31:02 UTC**

After the fourth or fifth round of "Nanite fixed X, still doesn't work" in this session, the user said directly:

> "We've had issues like this in the past and every time I think we have them fixed it pops back up. It's a deeper issue that I'll dig into once we get one success. If this next fix doesn't do it, we'll pivot and do this another way until Nanite is ready."

This frames the Curator MCP-tool-resolution class of problem as pre-existing and recurring, not novel to this session.

### Human-mediated relay between two separately-running agent sessions
**App:** Loom (with FE echoing the same pattern) · **File:** `937bab53-deeb-4dd5-9478-d0d5bb532a33.jsonl` · **throughout 2026-08-17**

Across the debugging saga, the operator ran Loom and Nanite as two independent Claude Code sessions and manually copy-pasted findings between them nine separate times (see the numbered timeline under Integration-specific pain). Phrases like "FYI, this is what eventually came from Curator:" and "Ready to test again. Nanite found the downstream issue..." recur throughout — there is no evidence of an automated channel between the durable agent under test and the session diagnosing it; every round-trip required the human operator as intermediary, and each fix had to be independently re-verified by the Loom-side engineer rather than trusted from either side's self-report.

### Wiring was fixed, but agent *behavior* still required steering
**App:** Loom · **File:** `937bab53-deeb-4dd5-9478-d0d5bb532a33.jsonl` (and `.nanite/boot-prompt.md` §5) · **2026-08-17, end of session**

Once the full pipeline mechanically worked (job ID 5 in Loom's `compile_jobs`), the output was still low quality — not a wiring bug but a case of the agent not following its own defined procedure: `generated_by: "loom-compiler"` (deterministic path — Curator never set `generation_mode: "llm"`), `title: "::draft"` (the literal directive marker, forwarded verbatim as if it were content), and `source_fragment_ids: []` (dropped, despite `fragment.id` being present in the wake payload the whole time). The write-up characterizes this as Curator's `classify_and_compile_fragment` procedure doing "something close to 'forward the raw fragment body verbatim,'" not the classify-then-author behavior its role description calls for.

---

## Taxonomy confusion

### "Nanite" name collision across unrelated Torque task batches
**App:** Fragments Engine · **File:** `7d748c54-2598-4a4b-a842-d862aa746d52.jsonl` · **2026-08-16T16:37 UTC**

While scoping new work in Torque's Nil project, the agent found an older (April 2026) task batch titled around "Nanite MCP" (vault management tools, "Add MCP stdio transport server to Nanite", embeddings/vector search, a "Conduit plugin," a "System Agent skeleton") that had nothing to do with the current, active Nanite project (durable-agent orchestration):

> "That second batch is confusing given 'Nanite' is also the name of a completely different, currently-active project in this portfolio (durable-agent orchestration). I don't know if this is: (a) a stale backlog from before Nil and Nanite were split into separate projects and this is dead weight, (b) genuinely still-live plans that happen to predate a rename, or (c) something else entirely."

The agent had to stop and ask the user via `AskUserQuestion`; resolved as "Stale/dead — ignore them, name collision from history."

### Ion vs. Loom referent confusion (see also Hallucinations)
Curator's own failure report treated "Loom" as synonymous with the old Ion Python codebase — a distinct, non-running predecessor app — rather than the current Go service. This was directly attributable to a stale planning document still living at `docs/architecture.md` and later rewritten specifically to correct it.

### Durable-agent class taxonomy defined only by analogy, not first principles
**App:** Loom · **File:** `629d4e19-6417-41b2-8f94-14ac7c5d183e.jsonl` and `937bab53-...jsonl` · **2026-08-16T16:23 UTC and after**

Curator and Weaver's Torque task descriptions define their shape purely by reference to pre-existing agents: `class: process`, `activationMode: instance`, `launch_source_type: process_tick` is described as "the exact shape Atlas Curator already runs in production, retargeted at Loom's own store," and Weaver as `class: advisor` — "the same discipline Atlas Librarian already uses." Neither the process/advisor class distinction nor the activation-mode/launch-source-type fields are explained from scratch anywhere in the material the Loom-side engineer read; understanding them required inferring behavior from a sibling agent's already-running configuration rather than documentation.

### Whether Curator/Weaver existed yet required independent verification, not trust
**App:** Loom · **File:** `937bab53-deeb-4dd5-9478-d0d5bb532a33.jsonl` · **2026-08-17T00:11 UTC**

Before attempting to provision the FE→Curator callback route, the Loom-side engineer cross-checked Torque task status against Nanite's actual git log and found they agreed (both `todo`, no trace of "Loom Curator" anywhere in the codebase) — but only after noting the ambiguity explicitly:

> "I'm seeing a mismatch worth flagging before I act on it... If I run the FE provisioning steps now, `<curator-wake-url>` would point at an endpoint that doesn't exist — the destination would be created but silently unusable, and the smoke test would just fail on a missing endpoint rather than tell us anything useful."

### "Steward" agent role retired mid-session as framework churn
**App:** Loom · **File:** `937bab53-deeb-4dd5-9478-d0d5bb532a33.jsonl` · **2026-08-17T00:37 UTC**

One of the five Nanite-side pilot tasks (`CW-20260816-0024`) is explicitly to remove a previously-shipped Nanite framework default role, "Steward," because it had "already superseded personally months ago by working directly with Tesseract and task-specific agents; its charter is now covered by Curator/Weaver where it overlaps at all" — evidence of agent-role taxonomy churn in Nanite's shipped defaults that this pilot happened to surface as cleanup work, not something raised or explained independently.

---

## Integration-specific pain (Loom Curator/Weaver wake pipeline)

This is the dominant thread across both repos for the second half of the date range: getting Fragments Engine's `callback` destination to successfully wake Nanite's Curator durable agent and have it compile a real Loom wiki page. The full, consolidated timeline (also captured by the agent itself in `apps/loom/.nanite/boot-prompt.md §4`, written at end of session) was, roughly in order:

1. **FE's four pilot tasks landed** (callback destination schema, directive-tagging ingest, async dispatch via FE's existing delivery queue, the actual pilot route) — catching and fixing a real production bug along the way (`RouteStage.Run` never matched against entities persisted earlier in the same pipeline run, so directive-based routing was structurally broken even after the tagging task landed).
2. **First smoke test**: FE's side worked end-to-end (routed, dispatched, callback POST succeeded), but Curator's wake endpoint (`POST /api/loom/curator-wake`) accepted the payload and returned in **~5ms** — too fast for real work, and no compile job appeared. Root cause: the handler validated the payload but never actually triggered a Curator turn.
3. **Nanite found and fixed three distinct wake-mechanism bugs**: the payload was folded into `WakePayload.Facts` (which nothing downstream reads) instead of `Prompt`; every `class: process` durable agent (Curator, Atlas Curator, Torque Supervisor) was wakeable **exactly once, ever**, due to a stale `active`-status skip-check that didn't account for `SessionPolicyFreshPerWake`; and a stale/removed Anthropic model snapshot (`claude-sonnet-4-20250514`) was pinned in Curator's config, 404ing every real wake attempt.
4. **Still no compile job.** Traced to Curator's `roleTools` glob patterns (`loom_*`, `fragments_*`) matching nothing in Nanite's own flat tool catalog, because those servers were registered via Mux as `proxy_only` and Nanite's toolclient doesn't resolve `proxy_only` upstreams into glob-matchable names — see Tool-calling issues.
5. **A DB-setting fix regressed tool resolution entirely** (generic dev tools started returning "unknown MCP tool") — a separate, unrelated bad state in Nanite's own progressive tool-discovery settings that happened to be adjacent to the intended fix.
6–7. **Partial recovery, then a tool-naming fix**: `dev_bash`/`dev_read` resolved again; `get_fragment_detail` became callable for the first time, but the call itself failed and Curator gave up after one attempt rather than retrying.
8. **A real FE-side path-handling bug was found and fixed** (see Hallucinations), but the very next retest still failed with Curator reporting the fragment didn't exist — this was independently verified false.
9. **Final root cause: a stale Mux/Tether MCP proxy subprocess.** MCP stdio servers are spawned once by their parent proxy and kept alive for the connection's life; reinstalling a fixed binary on disk doesn't hot-swap an already-running process. Curator's dedicated Loom-pilot connection (PID 26104/26109) had been running since before the FE path fix and was still silently serving from a phantom/wrong DB. Killing the stale child process did **not** trigger an automatic reconnect — it became a zombie with no replacement spawned; the fix required restarting the parent proxy itself, and even then reconnection depended on Nanite's own internal respawn logic, which the Loom-side engineer could not fully confirm from outside.

Only after all nine of the above were addressed did a first successful end-to-end compile occur (job ID 5) — and even that produced low-quality output requiring further steering (see Steering issues).

**Separately, during this same integration window, `nanite-api-service` itself crash-looped** after a Cerberus reload, unrelated to anything the Loom session had changed:

> "The reload triggered a real, unrelated crash — **Nanite is currently down**, crash-looping (5 failed restart attempts already)... `failed to open store: migrate: exec migration statement: constraint failed: CHECK constraint failed: status IN ('requested','approved','running','completed','failed','cancelled','rejected') (275)` ... at least one existing row has a `status` value outside that allowed set."

(File: `937bab53-deeb-4dd5-9478-d0d5bb532a33.jsonl`, 2026-08-17T16:58:43 UTC.) This corresponds to a real, same-day fix in the Nanite repo: commit `e2273f8` ("fix: crash-loop from subagent_runs migrations recreating a stale schema"), merged via PR #261 at 2026-08-17 13:05:54 -0500 (18:05:54 UTC) — roughly an hour after the Loom session observed the crash live.

---

## Other

### Portfolio-wide inconsistency in a shared library's default behavior
**App:** Loom · **File:** `937bab53-deeb-4dd5-9478-d0d5bb532a33.jsonl` · **2026-08-17T01:33–01:41 UTC**

Nanite defaults OTel tracing **on** (opt-out via `NANITE_OTEL_DISABLED=1`); Loom defaults it **off** (opt-in); Fragments Engine doesn't wire it up at all — all three built against the same shared `github.com/hollis-labs/go-otel` library, with "no portfolio-wide convention document" found governing which default is correct. Filed as a follow-up ticket, not resolved in-session.

### Nanite's own tool-catalog auto-pruning removed a first-party self-tool
**App:** Loom · **File:** `937bab53-deeb-4dd5-9478-d0d5bb532a33.jsonl` · **2026-08-17T03:55 UTC**

Pulled directly from `nanite-api-service`'s stderr logs during this session: `"msg":"mcp: auto-discover flagged removed tool","slug":"self-nanite-scratchpad-write"` alongside several Clockwork/Torque tool removals — visible evidence of Nanite's own tool-catalog churn during a routine restart, not otherwise investigated in either transcript.

### Config-example friction for Nanite-facing destinations
**App:** Fragments Engine · **Files:** `7d748c54-...jsonl`, `b255665b-...jsonl`, `e66eb21a-...jsonl`, `19106f2f-...jsonl`

FE's shipped config comments point integrators at `api.nanite_user_mailbox` / `api.nanite_messaging` as the reference examples for talking to Nanite, but these destination provider strings (`nanite_messaging`, `nanite_user_mailbox`) exist only as internal `case` branches in FE's destination-writer code — no Nanite-side documentation or contract was found or referenced from the FE side during this window; the callback-based Curator integration (the actual focus of this sprint) is a structurally different mechanism from these pre-existing providers, and neither transcript set clarifies the relationship between them.

---

## Patterns observed

- Every fix applied to the Curator wake pipeline surfaced exactly one new, different failure mode in a different layer — payload plumbing, then session-reuse semantics, then model pinning, then MCP tool-surface visibility, then a tool-binding regression, then tool-naming mismatch, then a stale-process/query-construction issue — nine distinct root causes across three codebases (Nanite, FE, Loom/Mux config) before a single successful end-to-end run.
- Several of the failures were invisible at the API-contract level: endpoints returned success, config validated, and no errors were logged, while nothing downstream actually happened (the wake handler accepting-but-no-op'ing; the MCP proxy silently serving from a phantom empty database). Both of the two hallucination-type incidents captured here trace back to exactly this shape of silent failure.
- In both incidents where Curator (the Nanite durable agent) reported a specific diagnosis of what had gone wrong, that diagnosis was independently checked by the Loom/FE-side engineer against ground truth and found to be factually wrong, in both cases pointing at a stale or misconfigured connection on Nanite's own side rather than the cause Curator named.
- Debugging this integration required continuous manual relay between two independently-run Claude Code sessions (one in Loom, one in Nanite); the transcripts show no direct communication channel between the durable agent under test and the session diagnosing its failures — every round-trip and every "Nanite fixed it, please retest" depended on the human operator.
- The user's own framing mid-session — "We've had issues like this in the past and every time I think we have them fixed it pops back up" — characterizes the MCP tool-resolution/binding failure class specifically as a known, recurring pattern predating this session, not a new discovery.
- Nanite's durable-agent class taxonomy (`class: process`/`class: advisor`, `activationMode`, `launch_source_type`) was understood by the Loom-side engineer only by analogy to already-running sibling agents ("the exact shape Atlas Curator already runs in production"), not from any first-principles reference read directly.
- A stale planning document, read literally by Curator, produced a wrong mental model of what "Loom" even referred to; the same session that discovered this rewrote the document specifically to correct it, but that correction happened only after the confusion had already produced a wrong live diagnosis.
- The one clearly reproducible code-level bug uncovered directly by Curator's own behavior (a divide-by-zero panic in Nanite's grep tool at `internal/mcp/dev_tools.go:821`) remains present in the Nanite codebase as of this review.
