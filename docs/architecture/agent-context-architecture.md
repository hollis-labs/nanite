# Agent Context Architecture — A Portable Lens

> **Audience:** anyone designing prompts, gates, tool descriptions, classifier rules, or sprint tasks that touch the chat harness or its agentic substrate.
>
> **Use:** before adding a rule, gate, or enforcement layer, run the decision rules at the bottom of this doc against the proposed change. If the change would violate them, redesign or escalate.
>
> **Co-lens:** [agentic-error-recovery.md](./agentic-error-recovery.md) — the recovery layer this doc complements.

## The shape

Agent context in Nanite has **three substrates** with different lifecycle and ownership:

| Substrate | Lifecycle | Owns |
|---|---|---|
| **Static substrate** (system prompt) | Stable across all turns | Identity, values, grounding disposition |
| **Dynamic surface** (tool descriptions + skills + classifier injection) | Per-turn, intent-shaped | Tool gist + on-demand depth, situational framing |
| **Reactive recovery** (lens tools: describe / validate / repair / remember) | When something fails | Recovery capabilities the agent reaches for |

The architectural mistake we made in 2026-04 was leaking content from the dynamic surface and reactive recovery layers back into the static substrate, which over six iterations turned the system prompt from a 250-word identity into a 1076-word rule list.

## The diagnosis (originating evidence)

Across chat sessions c107 → c117 (2026-04-28 → 2026-04-29), the chat agent's behavior degraded from "problem-solver who tries things and recovers" to "rule-following defendant who dodges anything risky." The trace is well-documented:

- **c107**: agent made 10 failed `nanite_show_card` attempts, no recovery surface available. *Lens designed.*
- **c109–c113**: lens shipped, fixed individual incidents. Each fix added a defensive rule.
- **c114**: describe gate forced into hot path. Agent burned all 10 turns navigating cached describe responses.
- **c115**: agent literally couldn't satisfy the gate because `nanite_tool_describe` wasn't on its tool surface.
- **c117**: with all fixes in place, agent still dodged the user's "make a demo report" request. It chose `info-card` + `metric-card` instead of `report-card`, citing the grounding rule's prohibition on "render a card from data you don't have." It read the rule literally and avoided.

The pattern across the sequence:

| Window | default.md word count | Bullets | Negative phrasings ("don't / never") | Mechanical gates |
|---|---|---|---|---|
| Pre-c107 | ~250 | ~6 | 2 | 0 |
| Post-c117 | **1076** | **24** | **14** | **3** |

Each individual rule was justified by an observed incident. The cumulative effect was an agent that read the prompt as a constraint document rather than a capability framing.

This is recoverable but not by adding more rules.

## Three principles

### 1. Static prompts are for who you are, not what to do

The system prompt's job is to set **identity, values, and disposition**. Everything else is more durable somewhere else:

- **Identity / role** ("you are an assistant in the Nanite chat harness")
- **Disposition** ("be honest about real vs synthesized")
- **Capability framing** ("you have meta-tools for discovery and recovery")

What does **not** belong in the system prompt:

- Per-tool usage rules → tool description
- Per-task framing → classifier-driven runtime injection
- Error-recovery procedures → lens tools the agent reaches for
- Schema constraints → per-type validator at the handler boundary
- Honesty enforcement → mechanical (failure footer) + identity (you-are-honest)

Anthropic's own published Claude system prompts and the *Building Effective Agents* (Schluntz/Zhang, 2024-12) guidance both lean heavy on identity + values and light on prescriptive process. Constitutional AI literature backs this: identity-level framing is more durable across context than rule-level framing.

### 2. Tool knowledge has three natural homes

Tool knowledge is not one thing. It has different shapes that load at different times:

- **Tool description** = invariant gist. What the tool does, when to use it, the contract. ~one paragraph. Loaded with the tool when the agent considers calling it.
- **Tool skill / README** = depth. Golden examples, patterns, gotchas, edge cases. Multi-page. Loaded on-demand by the Tool Broker, by the agent calling describe, or by classifier-driven injection.
- **Tool Broker** = routing intelligence. Knows which tools to load for what intent. Doesn't own the knowledge; selects which knowledge to surface.

Tool descriptions should be **elevator pitches with skill pointers**: enough to know whether to call the tool and roughly how, with `see skill <slug>` for the deep dive. This keeps descriptions scannable and the deep content out of the always-loaded surface.

### 3. Recovery posture should be reactive, not preemptive

The error-recovery lens (described in `agentic-error-recovery.md`) is a set of tools the agent reaches for **when something fails or is genuinely unfamiliar**. It should never be a gate the agent passes through before normal action.

The describe-required gate (CW-20260429-0025/0027) was the canonical failure of this principle. Its job was to prevent invented fields by forcing `nanite_tool_describe` before every `nanite_show_card`. But the per-type schema validator already prevents invented fields. The gate added a turn of friction to every card emission and trained the agent to expect failure on every call. **Removed.**

The shape we want:

- Agent attempts the action it thinks is right.
- If it succeeds, great.
- If it fails, the structured error tells the agent what tools recover (describe / validate). The agent reaches for them.
- If it succeeds with repair, the `repair_note` tells the agent what got reshaped, and `nanite_remember` captures the lesson.

This is `reactive` recovery. The lens is *available*, not *required*.

## Layer responsibility map

| Layer | Job | Lifecycle |
|---|---|---|
| **System prompt** | Identity, values, grounding disposition | Stable |
| **Classifier** | Intent → routing → tool/context selection | Per-turn |
| **Tool Broker** | Which tools to load, where to send execution | Per-turn |
| **Tool description** | Elevator pitch, when-to-use, contract | Invariant gist |
| **Tool skill / README** | Depth — examples, patterns, gotchas | On-demand |
| **Lens tools** (describe / validate / repair / remember) | Recovery capabilities | Reactive |
| **Per-type validator** | Schema enforcement at handler boundary | Mechanical |
| **Failure footer** | Surface lens activity to user | Mechanical |
| **Executor agents** (future) | Multi-step tool flows behind a clean interface | Delegate-on-demand |
| **Permission layer** | Security, blast-radius, capability gates | Orthogonal |

If a concern can be expressed at one of these layers, **it does not belong at another**. Two layers enforcing the same concern is the canonical anti-pattern.

## Anti-patterns

These are the failure modes we observed in the c107 → c117 evolution. Each one is generative — once you see one, ask whether a sister pattern lurks.

1. **Prompt accretion.** Each rule is right for the incident it addresses; the cumulative effect is wrong. Defense: every rule addition triggers an audit of what could be removed or relocated.
2. **Preemptive gates.** Forcing the recovery lens to fire before any error has occurred. The lens is for recovery; the gate punishes the natural call → fail → recover loop. Defense: gates inside the chat loop should be load-bearing security/safety boundaries, not process suggestions.
3. **Redundant enforcement layers.** Prompt + gate + handler-validator all enforcing "no invented fields." Each one was added because the previous one wasn't fully trusted. Defense: trust each layer to do its job, and remove the redundant ones.
4. **Rule-following defendant.** The agent reads the prompt as a constraint document and produces the safest interpretation, often by dodging the user's actual request. Symptom: agent picks `info-card` over `report-card` because report-card has stricter framing. Defense: identity-level disposition ("you are a problem-solver") + tool capability framing > rule lists.
5. **Mechanism-without-trust.** Layer L adds an enforcement check because layer L-1 isn't trusted to do its job. Defense: fix layer L-1 if it has a real failure, not by adding L's check.
6. **Process leaking into substrate.** Process rules ("call X before Y") leak from situational classifier injection (where they belong) into the always-on system prompt (where they handcuff every interaction). Defense: per-task framing always belongs in classifier-driven runtime injection, never in the static prompt.

## Decision rules for new work

Run these before adding any rule, gate, check, or enforcement layer.

1. **Where does this constraint actually need to be enforced?** (Handler boundary? Tool description? Permission layer? Or is this an identity-level disposition?) The answer is rarely "the system prompt."
2. **Is this preemptive or reactive?** Prefer reactive. Preemptive enforcement inside the chat loop is almost always wrong.
3. **Is another layer already doing this?** If yes, remove the addition (and possibly remove the existing layer if it's redundant). Two layers enforcing the same concern is anti-pattern (3).
4. **Could this go in a tool description instead of the system prompt?** If yes, do that. Tool descriptions are evaluated when the agent considers the tool; system prompt rules are evaluated globally.
5. **Could this be a runtime classifier injection?** If the constraint is per-task / per-intent, the classifier is the right layer.
6. **Does this take handcuffs OFF the agent or PUT them ON?** Default-off. The agent should leave a session more capable than it entered.
7. **Apply the c117 test:** would this rule cause an agent to dodge a "let's do some testing — show me X" request? If yes, the framing is too strict.
8. **Measure prompt density.** Word count, bullet count, negative phrasings. Ratcheting up over time without ratcheting down is the prompt-accretion anti-pattern.

## Metrics to watch

- **System prompt word count.** Currently ~310 (target). Anything above ~500 is a smell.
- **System prompt bullet count.** Currently ~9 (target). Anything above ~15 is a smell.
- **Negative phrasings count.** *don't / never / not allowed / cannot* — currently ~5 (target). High counts indicate fear-based framing.
- **Mechanical gates inside the chat loop.** Currently 0 (target — gate dropped). Each one needs explicit justification on safety/security grounds.
- **Redundant enforcement layers per concern.** Should be 1 in nearly all cases.

## Cross-references

- **Co-lens:** [agentic-error-recovery.md](./agentic-error-recovery.md) (the recovery substrate this doc complements).
- **Originating evidence:** chat sessions c107 (2026-04-28) through c117 (2026-04-29).
- **Sprint history:** SP-20260428-0002 (origin of the lens), CW-20260429-0017 (validator hotfix), CW-20260429-0019/0024/0025/0026/0027/0028/0029 (the c112-c114 cluster).
- **Anthropic published guidance:** *Building Effective Agents* (Schluntz/Zhang, 2024-12); tool-use guides; Constitutional AI papers.
- **Vanta key:** `decisions.nanite.architecture.agent_context_layering`
- **Cross-portfolio applicability:** this lens applies to any agentic system with a chat-style harness — Mux, Clockwork, Vanta, plugin authors. Reference by Vanta key, not by file.

## When in doubt

Ask: *"Am I taking handcuffs off the agent, or putting them on?"* If the answer is the second one, redesign. The lens primitives, the Broker, the classifier, the executor pattern all exist to give the agent better tools. They don't exist to constrain it into a smaller box.
