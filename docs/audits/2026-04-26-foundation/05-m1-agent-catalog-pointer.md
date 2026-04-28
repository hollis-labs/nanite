# M1 Audit — Agent + Role + Prompt Catalog Pointer

**Ticket:** CW-20260426-0015 (Arc M1)
**Date:** 2026-04-26
**Auditor:** m1-audit sub-agent
**Canonical doc:** `docs/agent-role-prompt-catalog.md`

---

## Summary

Full catalog of every location agent profiles, roles, and prompts live in the Nanite repo and user-home framework. Reconciled against the three-role spec (Chat / Worker / Planner). Feeds B5-DF (PlatformPromptTemplate retire / narrow / repurpose).

---

## Top-Line Counts

| Category | Count |
|----------|-------|
| Locations audited (dirs + files) | 25+ |
| Roles defined (user-home) | 11 (6 domain, 2 meta, 3 stack) |
| Agent profiles (project config.yaml) | 7 |
| Agent profiles (built-in runtime) | 2 (default Chat, worker YAML) |
| Prompts — built-in Go var | 1 (`PlatformPromptTemplate`) |
| Prompts — built-in DB templates | 5 (`BuiltinPromptTemplates`) |
| Prompts — built-in embedded | 1 (Base Chat Agent `default.md`) |
| Prompts — user-authored (DB) | Unknown at runtime (0 by default) |
| Gaps identified | 6 |

---

## Gap List

1. **No Chat-role harness prompt in DB** — `default.md` covers built-in path; no DB template for Chat role; no tool surface locking.
2. **No Planner-role system prompt** — `strategic-planner` role covers behavior but no dedicated DB template for the dispatched Planner harness role.
3. **Worker system prompt is Mentat-era** — `config/agents/worker.yaml` still says "Fragments Engine Operator."
4. **`PlatformPromptTemplate` has 0 active consumers by default** — only fires when an agent has ≥1 template assigned (no agent does by default); contains stale Mentat identity text.
5. **`BuiltinPromptTemplates` (5) not auto-assigned** — seeded to DB but no `agent_prompt_templates` rows; zero consumers until user assigns manually.
6. **`~/.nanite/agents/` directory does not exist** — spec reserves it for folder-drop; absent.

---

## Most Consequential B5-DF Inputs

1. **`PlatformPromptTemplate` is a ghost.** It is a Go var (not in DB), contains Mentat-era identity text, and fires only when templates are explicitly assigned to an agent — which never happens by default. Functionally, it is inactive. Retiring it does not break any current session.

2. **Two divergent identity paths exist.** Sessions using the Base Chat Agent (`default.md`) get Nanite identity. Sessions with templates assigned get Mentat identity prepended. The bifurcation is invisible to the user and inconsistent.

3. **The composition architecture is sound; the content is stale.** The `ComposePromptForAgent()` pipeline, `BuiltinPromptTemplates`, and variable interpolation system are good foundations for the three-role spec's prompt assembly. B5-DF should separate "retire Mentat content" from "retire the composition system" — only the former is indicated by the evidence.
