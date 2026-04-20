# Interaction Quality Research - 2026-04-19

## 1. Deterministic Grounding

### Finding: MCP now has first-class structured tool output

- Source: [MCP Tools spec: `outputSchema` and `structuredContent`](https://modelcontextprotocol.io/specification/2025-06-18/server/tools), [MCP schema reference](https://modelcontextprotocol.io/specification/2025-06-18/schema)
- Synopsis: The MCP 2025-06-18 spec added optional `outputSchema` on tools and `structuredContent` on tool results. If a tool declares an output schema, the server must return structured results conforming to it, and clients should validate the result. The spec also advises returning serialized JSON in text content for backward compatibility.
- Relevance: This maps directly to Nanite's goal of making structured fields in cards come from MCP responses, not LLM-authored markdown. Treat `structuredContent` as the authoritative data plane and LLM prose as commentary.
- Caveat: The spec explicitly says annotations are untrusted unless the server is trusted. Implementation consistency is still uneven across SDKs and clients, so Nanite should validate schema conformance itself and not assume all MCP servers implement structured output correctly.

### Finding: Anthropic emphasizes "agent-computer interface" design more than prompt rules

- Source: [Anthropic: Building effective agents](https://www.anthropic.com/engineering/building-effective-agents), [Anthropic: Writing effective tools for AI agents](https://www.anthropic.com/engineering/writing-tools-for-agents)
- Synopsis: Anthropic argues tool definitions need the same design effort as user interfaces: clear parameters, examples, boundaries between similar tools, and formats that are easy for models to use. Their SWE-bench agent improved when they changed file tools to require absolute paths, a deterministic constraint that made the right behavior easier than the wrong one.
- Relevance: This supports pushing "do not fabricate rows" out of prose rules and into interfaces that make fabrication structurally hard: typed rows, pagination cursors, counts, provenance, and renderer-owned fields.
- Caveat: Tool ergonomics reduces failures but does not eliminate model misinterpretation of outputs. Anthropic still recommends evaluation and guardrails around the loop.

### Finding: Citation and quote grounding are still recommended even with tools

- Source: [Anthropic: Reduce hallucinations](https://platform.claude.com/docs/en/test-and-evaluate/strengthen-guardrails/reduce-hallucinations), [Anthropic: Search result content blocks](https://docs.anthropic.com/en/docs/build-with-claude/search-results)
- Synopsis: Anthropic's hallucination guide recommends allowing "I don't know," extracting direct quotes before synthesis, and requiring claims to be traceable to supporting context. Search result content blocks let applications pass source/title metadata to Claude and receive natural citations.
- Relevance: For summary cards and prose sections, Nanite should make evidence availability explicit: if the rendered card has rows A/B/C, prose claims should cite those rows or source ids, and unsupported claims should be blocked or demoted.
- Caveat: Citations are not proof of correctness unless the system verifies claim-to-source alignment. The model can still cite weak or irrelevant evidence unless the broker or renderer audits the mapping.

## 2. Context-Aware Rule Selection

### Finding: Claude Code defers MCP tool schemas with Tool Search

- Source: [Claude Code MCP docs: Scale with MCP Tool Search](https://code.claude.com/docs/en/mcp), [Claude API tool reference: `defer_loading`](https://platform.claude.com/docs/en/agents-and-tools/tool-use/tool-reference)
- Synopsis: Claude Code loads only MCP tool names at session start and lets Claude use a search tool to discover full tool definitions when needed. Tool descriptions and server instructions are truncated at 2KB, and `defer_loading` preserves prompt cache entries by keeping unused tool definitions out of the prompt prefix.
- Relevance: This is the closest public Anthropic analogue to Nanite's "ship only relevant rules per turn." Treat rule packs like tool schemas: index metadata up front, retrieve detailed rules only after classifying intent and likely tool family.
- Caveat: Tool search needs model support for `tool_reference` blocks and can be disabled behind non-first-party proxies. Nanite should have a fallback threshold mode and observable misses.

### Finding: Cursor, Windsurf, Copilot, and Continue all separate modes/rules by task type

- Source: [Cursor modes docs](https://docs.cursor.com/en/chat/agent), [Cursor Plan Mode blog](https://cursor.com/blog/plan-mode), [Windsurf Cascade modes](https://docs.windsurf.com/windsurf/cascade/modes), [GitHub Copilot Ask/Edit/Agent modes](https://github.blog/ai-and-ml/copilot-ask-edit-and-agent-modes-what-they-do-and-when-to-use-them/), [Continue rules/context docs](https://docs.continue.dev/reference)
- Synopsis: Peer coding harnesses expose different operating modes: Ask/read-only, Plan/research, Edit/targeted changes, Agent/autonomous execution. These modes change available tools, instructions, and user expectations. Continue and Windsurf also support project rules and context providers rather than one universal prompt.
- Relevance: Nanite should classify turns into operating modes before model invocation, then select rules, tool permissions, and narration policy from that mode. This is stronger than relying on a large universal prompt.
- Caveat: Modes can surprise users if the harness switches silently. Cursor and Windsurf expose mode UI; Nanite should surface the chosen strategy enough that users understand why the agent asks, acts, or delegates.

## 3. Tool Broker Enrichment Pipeline

### Finding: Anthropic recommends evaluating tools with agentic loops and refining descriptions from failures

- Source: [Anthropic: Writing effective tools for AI agents](https://www.anthropic.com/engineering/writing-tools-for-agents)
- Synopsis: Anthropic suggests testing tools programmatically with direct API calls and simple loops that alternate model calls and tool results. Tool builders should observe mistakes, rewrite descriptions, add examples, refine schemas, and design tools that are easy for agents to combine.
- Relevance: Nanite's probe agent should generate an enrichment record from empirical behavior, not just static metadata. It can classify pagination style, required identifiers, destructive behavior, typical errors, and "must inspect next page" triggers after sandboxed probes.
- Caveat: Probing can be unsafe or expensive for open-world/destructive tools. Tool annotations and permission gating should determine whether the probe can execute, dry-run, call `--help`, or only inspect docs.

### Finding: MCP tool annotations give the broker a shared risk vocabulary

- Source: [MCP schema reference: `ToolAnnotations`](https://modelcontextprotocol.io/specification/2025-06-18/schema), [MCP Tools spec](https://modelcontextprotocol.io/specification/2025-06-18/server/tools)
- Synopsis: MCP annotations include hints such as `readOnlyHint`, `destructiveHint`, `idempotentHint`, and `openWorldHint`. They are designed to help clients and models understand tool behavior, especially trust and safety properties.
- Relevance: Nanite can seed enrichment records from annotations, then override or supplement them after local probing. For example, "read-only list endpoint with cursor pagination" can activate pagination and no-fabrication playbooks, while "open-world search" can activate freshness and citation rules.
- Caveat: MCP says clients must not blindly trust annotations from untrusted servers. The enrichment pipeline should record provenance: server-declared, broker-inferred, user-approved, or verified-by-probe.

### Finding: Recent research treats tool descriptions as a measurable reliability surface

- Source: [Model Context Protocol Tool Descriptions Are Smelly!](https://arxiv.org/abs/2602.14878), [Dynamic ReAct: Scalable Tool Selection for Large-Scale MCP Environments](https://arxiv.org/abs/2509.20386)
- Synopsis: Tool-description quality and scalable tool selection are emerging research topics. The papers frame tool descriptors as operational inputs that affect tool selection, argument quality, and cost when large MCP environments exceed context limits.
- Relevance: Nanite should version and lint enrichment records the same way it versions prompts. Bad tool descriptions are not just documentation debt; they are behavior-control bugs.
- Caveat: Most research here is recent and not yet a stable consensus. Keep the enrichment pipeline measurable with regression traces rather than relying on proposed metrics alone.

## 4. Strategy Loop

### Finding: Anthropic's public agent pattern is a loop with checkpoints, ground truth, and human feedback

- Source: [Anthropic: Building effective agents](https://www.anthropic.com/engineering/building-effective-agents), [Anthropic: Computer use tool](https://docs.anthropic.com/en/docs/build-with-claude/computer-use)
- Synopsis: Anthropic defines agents as systems where the LLM dynamically directs process and tool usage. During execution, agents should gain ground truth from the environment at each step, pause for human feedback at checkpoints or blockers, and stop based on completion or iteration limits. Computer use is presented as an agent loop: model requests action, app executes, result returns to model.
- Relevance: Nanite's strategy loop should be an explicit controller between intent and tools: choose direct answer, retrieval chain, subagent, clarification, or background job; inspect evidence at checkpoints; and prevent silent continuation when the data is thin.
- Caveat: Anthropic also warns that autonomous agents have higher cost and compounding-error risk. The strategy loop should default to simpler workflows unless complexity is justified by the ask.

### Finding: LangGraph has made interrupt/resume and durable state first-class

- Source: [LangGraph interrupts docs](https://docs.langchain.com/oss/python/langgraph/human-in-the-loop), [LangChain: Building LangGraph runtime](https://www.blog.langchain.com/building-langgraph/)
- Synopsis: LangGraph's `interrupt` pauses execution, saves graph state through a checkpointer, and resumes later with human input. Its runtime exposes streaming modes for values, updates, messages, tasks, checkpoints, and custom events.
- Relevance: Nanite can treat "ask-to-clarify" and "budget exhausted, ask to continue" as runtime states, not as final-answer failures. Checkpointed strategy state also makes progress review possible before additional tool calls.
- Caveat: LangGraph interrupts restart the node from the beginning on resume, so side effects before interruption must be idempotent. Nanite should separate pure planning/checkpoint nodes from side-effecting tool execution.

### Finding: OpenAI Agents SDK formalizes handoffs, guardrails, and tracing

- Source: [OpenAI Agents SDK tracing](https://openai.github.io/openai-agents-js/guides/tracing/), [OpenAI Agents SDK guardrails](https://openai.github.io/openai-agents-python/guardrails/)
- Synopsis: OpenAI's SDK records LLM generations, tool calls, handoffs, guardrails, and custom events. Guardrails are colocated with agents and handoffs use a separate pipeline from ordinary function tools.
- Relevance: Nanite's strategy loop should produce a structured trace: classification, selected playbook, selected rules, tool calls, evidence sufficiency checks, and final response contract. This is essential for diagnosing "appeared useful" failures.
- Caveat: Tracing does not prevent bad behavior; it makes it inspectable. Nanite still needs broker-enforced gates where failures are predictable.

## 5. Playbook System

### Finding: Anthropic's agent patterns are effectively named recipes

- Source: [Anthropic: Building effective agents](https://www.anthropic.com/engineering/building-effective-agents)
- Synopsis: Anthropic names reusable patterns: prompt chaining, routing, parallelization, orchestrator-workers, evaluator-optimizer, and autonomous agents. Each pattern has a "when to use" section and examples.
- Relevance: Nanite's playbooks can encode the same decision shape: "large dataset exploration -> orchestrator-workers or delegated probe," "policy-heavy tool chain -> think/checkpoint," "ambiguous request -> clarify before action."
- Caveat: Anthropic explicitly says these are not prescriptive. Playbooks should be overrideable and versioned, with eval traces showing when a recipe helps or hurts.

### Finding: Windsurf uses durable plan files and a planning agent

- Source: [Windsurf Cascade overview/planning](https://docs.windsurf.com/plugins/cascade/planning-mode), [Windsurf Cascade modes](https://docs.windsurf.com/windsurf/cascade/modes)
- Synopsis: Windsurf Plan Mode explores the codebase, asks clarifying questions, presents options, and writes a plan to a markdown file. Its docs describe a specialized planning agent that refines a long-term plan while the selected model handles short-term actions.
- Relevance: Nanite playbooks should produce durable artifacts for complex tasks: plan, assumptions, evidence needed, current budget, and continuation criteria. That gives the strategy loop something concrete to review.
- Caveat: Durable plans can become stale. Windsurf notes plans are useful across sessions, but Nanite should require revalidation when tool state, filters, or user constraints change.

### Finding: Claude Code subagents are specialized isolated contexts

- Source: [Claude Code subagents docs](https://docs.claude.com/en/docs/claude-code/subagents), [Anthropic blog: Subagents in Claude Code](https://claude.com/blog/subagents-in-claude-code)
- Synopsis: Claude Code can delegate work to specialized subagents with their own context windows. Public guidance emphasizes using subagents for research, review, domain expertise, and parallel work while keeping the main thread focused.
- Relevance: A playbook can choose subagents based on ask type and data size, not model whim. For example, "medium-to-large dataset exploration" can spawn a probe with a fixed N-turn budget and require a result schema.
- Caveat: Subagents add coordination overhead and can hide evidence if they only return summaries. Nanite should require subagents to return structured findings, sources, and unresolved gaps.

## 6. Memory-as-Grounding

### Finding: Claude Code loads memory before the conversation and uses on-demand topic files

- Source: [Claude Code memory docs](https://code.claude.com/docs/en/memory), [Anthropic Claude Code memory docs](https://docs.anthropic.com/en/docs/claude-code/memory)
- Synopsis: Claude Code loads project/user memory files at session start. Newer docs describe `MEMORY.md` as a concise index whose first 200 lines or 25KB are loaded, while topic files are read on demand. Auto memory is local to a machine and repository.
- Relevance: This supports Nanite's proposal that memory should feed the strategy/rule layer before the main LLM runs. Memory should not only be a retrieval tool; it should influence playbook selection, rule selection, and default budget.
- Caveat: Public user reports and the docs both imply memory adherence is imperfect. Nanite should distinguish "memory loaded" from "memory applied" and make strategy decisions traceable.

### Finding: CrewAI injects recalled memory before each task

- Source: [CrewAI memory docs](https://docs.crewai.com/en/concepts/memory)
- Synopsis: CrewAI's unified memory can be shared by a crew or scoped to agents. After each task it extracts discrete facts, and before each task it recalls relevant context and injects it into the prompt.
- Relevance: This is an example of memory-as-grounding rather than memory-as-an-optional-tool. Nanite can do the same at the strategy layer: "last successful approach for this user/request type" becomes an input to routing.
- Caveat: Automatic memory extraction can store wrong assumptions. Nanite should keep memory writes typed, attributed, and preferably confirmed or derived from successful traces.

### Finding: ChatGPT has product-level memory and project scoping

- Source: [OpenAI: Memory and new controls for ChatGPT](https://openai.com/index/memory-and-new-controls-for-chatgpt), [OpenAI Help: Memory FAQ](https://help.openai.com/en/articles/8590148-memory-faq)
- Synopsis: ChatGPT memory includes saved memories and reference chat history, with user controls and project/context scoping. OpenAI presents memory as a personalization layer that affects future responses without requiring the user to restate preferences.
- Relevance: For Nanite, memory should be scoped by session, project, user, and tool environment. Strategy memories should be separate from user facts: "delegation worked for portfolio summary" is different from "user's preferred format."
- Caveat: Product memory is partly opaque to users. Nanite should expose memory provenance and allow deletion, because incorrect strategic memories could repeatedly select the wrong playbook.

## 7. Budget Negotiation

### Finding: Claude Code hooks can block stopping and force continuation

- Source: [Claude Code hooks reference](https://code.claude.com/docs/en/hooks)
- Synopsis: Claude Code has `Stop` and `SubagentStop` hooks that can block the model from stopping. Hooks can also inject context, deny actions, or escalate permission decisions. Prompt-based and agent hooks can evaluate whether an action should proceed.
- Relevance: Nanite can implement a budget governor as a Stop-like checkpoint: when turn/tool budget is exhausted, require a graceful wrap-up or explicit continuation request rather than silent termination.
- Caveat: A Stop hook can create loops if not guarded. Claude Code includes `stop_hook_active` to prevent repeated stop-blocking; Nanite needs an equivalent reentrancy guard.

### Finding: Anthropic recommends iteration limits as control boundaries

- Source: [Anthropic: Building effective agents](https://www.anthropic.com/engineering/building-effective-agents)
- Synopsis: Anthropic says agents often terminate on completion but should also include stopping conditions such as maximum iterations to maintain control.
- Relevance: Nanite should define budgets per playbook: tool calls, wall time, rows/pages, tokens, and subagent turns. On exhaustion, the final response should state what was checked, what remains, and whether continuing is likely to change the answer.
- Caveat: Too-strict budgets can encourage premature summaries. The key is not just limiting; it is forcing honest negotiation when the limit is reached.

### Finding: OpenAI Responses API supports background mode for long work

- Source: [OpenAI: New tools and features in the Responses API](https://openai.com/index/new-tools-and-features-in-the-responses-api/), [OpenAI API model docs noting background mode for long pro runs](https://developers.openai.com/api/docs/models/gpt-5.2-pro)
- Synopsis: OpenAI added background mode for requests that may take several minutes, and Responses API reasoning summaries expose a user-visible digest of internal reasoning without showing raw chain of thought.
- Relevance: Nanite's budget negotiation can offer "continue in background" for high-value tasks instead of forcing the main chat turn to stretch until failure.
- Caveat: Background mode changes UX obligations: progress, cancellation, partial results, and notifications become product requirements.

## 8. Per-Turn Writable Scratchpad

### Finding: Anthropic's "think" tool improves long tool-call chains when used selectively

- Source: [Anthropic: The "think" tool](https://www.anthropic.com/engineering/claude-think-tool)
- Synopsis: Anthropic found a tool that lets Claude write private reasoning before acting can improve performance in tool-output analysis, policy-heavy environments, and sequential decision making. They recommend examples and system-prompt guidance for when to use it, and warn it adds tokens and is not useful for simple or parallel calls.
- Relevance: Nanite's scratchpad should not be generic chain-of-thought storage. It should be a typed, per-turn working memory for facts worth reusing: seen ids, cursors, pending uncertainties, user constraints, evidence gaps, and planned next checks.
- Caveat: The scratchpad can become another place for hallucinated state unless writes are structured and distinguish tool-derived facts from model hypotheses.

### Finding: LangGraph state functions as explicit working memory

- Source: [LangGraph interrupts docs](https://docs.langchain.com/oss/python/langgraph/human-in-the-loop), [LangChain: Building LangGraph runtime](https://www.blog.langchain.com/building-langgraph/)
- Synopsis: LangGraph nodes read and modify graph state, and checkpoints preserve that state across interrupts and resumes. Its runtime streams state updates separately from messages.
- Relevance: Nanite can model scratchpad as a state object owned by the strategy loop, not as prose in conversation history. O(1) lookup fields beat scanning prior tokens for "what did I learn?"
- Caveat: State must be designed for idempotency and replay. If a node resumes, scratchpad writes need stable operation ids or append-only semantics.

## 9. Structured-Data-Backed Card Rendering

### Finding: MCP resource links and structured content support data/UI separation

- Source: [MCP Tools spec: Tool Result, resource links, structured content](https://modelcontextprotocol.io/specification/2025-06-18/server/tools), [MCP Apps / UI resource rendering examples](https://mcpui.dev/guide/client/resource-renderer)
- Synopsis: MCP tool results can include structured content, text/image/audio, embedded resources, and resource links. UI-capable MCP clients can render resources returned by tools while using structured data to hydrate the view.
- Relevance: Nanite's envelope-as-pointer pattern fits the direction of MCP: tool returns data and resource references; renderer fetches authoritative data and paints rows/charts/cards. The LLM should not produce table rows that the UI treats as facts.
- Caveat: Host support for rich UI resources varies. Provide graceful degradation: a structured result plus compact textual summary, with the card renderer preferred when available.

### Finding: ChatGPT Apps SDK uses structured tool output to hydrate widgets

- Source: [OpenAI Developers: Apps SDK overview](https://developers.openai.com/), [OpenAI Apps SDK examples repository](https://github.com/openai/openai-apps-sdk-examples), [OpenAI Apps SDK community examples of `structuredContent` and `openai/outputTemplate`](https://community.openai.com/t/are-apps-sdk-ui-features-be-available-for-development-mode/1361608)
- Synopsis: ChatGPT apps are MCP-backed and can return `structuredContent` plus metadata pointing to an output template/widget. The widget renders inside ChatGPT and reads tool output rather than relying on model-written markdown.
- Relevance: This is a peer-product validation of Nanite's structured-data-backed cards. The model invokes a tool; the tool returns data; the component renders the authoritative view.
- Caveat: Some Apps SDK metadata is OpenAI-specific and not pure MCP. Nanite should keep its card contract portable: MCP-native structured content first, host-specific rendering metadata as an adapter layer.

## 10. Inter-Iter Narration vs. Final Reply

### Finding: Anthropic says planning steps should be transparent, but final answers need not be transcripts

- Source: [Anthropic: Building effective agents](https://www.anthropic.com/engineering/building-effective-agents), [Claude Code output styles](https://docs.claude.com/en/docs/claude-code/output-styles), [Claude Code status line](https://code.claude.com/docs/en/statusline)
- Synopsis: Anthropic recommends transparency by explicitly showing agent planning steps. Claude Code also separates user-facing output style and status-line metadata from the underlying agent loop.
- Relevance: Nanite should preserve live narration as a progress channel, then collapse it into "steps taken" after turn end. The final response should be a clean answer plus evidence gaps, not a concatenation of every interim utterance.
- Caveat: Hiding too much progress can reduce trust in long tasks. The UI should retain an expandable trace with tool calls, not delete interim narration entirely.

### Finding: LangGraph and OpenAI expose multiple stream event types

- Source: [LangChain: Building LangGraph runtime](https://www.blog.langchain.com/building-langgraph/), [OpenAI: New Responses API tools and reasoning summaries](https://openai.com/index/new-tools-and-features-in-the-responses-api/)
- Synopsis: LangGraph has distinct stream modes for messages, tasks, checkpoints, custom events, and state updates. OpenAI Responses API exposes reasoning summaries similar to what ChatGPT shows, separate from final answer text.
- Relevance: Nanite should stream event-typed records: `interim_narration`, `tool_call`, `tool_result`, `strategy_checkpoint`, `final`. The final composer should consume the trace but not simply append all prior model text.
- Caveat: Once stream types exist, clients must handle backpressure, reconnection, and persistence. The final rendering contract should be independent of transient stream delivery.

## Ideas We Haven't Considered

### Executable behavioral assertions for agents

- Source: [An Approach to Checking Correctness for Agentic Systems](https://arxiv.org/abs/2509.20364), [ContextCov: executable constraints from agent instruction files](https://arxiv.org/abs/2603.00822)
- Synopsis: Recent work applies temporal-logic-style assertions and converts natural-language agent instructions into executable guardrails over traces.
- Relevance: Nanite can encode interaction-quality rules as testable trace assertions: "if list result has `has_more=true`, final summary cannot claim complete coverage unless another page was fetched or limitation stated."
- Caveat: Turning qualitative behavior into assertions takes engineering time, but it is likely the only way to prevent regressions as prompts and models change.

### Benchmarks for "ask vs. assume" are now directly relevant

- Source: [Learning to Ask / NoisyToolBench](https://aclanthology.org/2025.emnlp-main.1104/), [CAR-bench](https://github.com/CAR-bench/car-bench), [ClarifyMT-Bench](https://arxiv.org/abs/2512.21120), [Ask or Assume?](https://arxiv.org/abs/2603.26233)
- Synopsis: These benchmarks evaluate agents under missing arguments, ambiguity, missing tools, and uncertainty. NoisyToolBench finds models often invent missing arguments rather than ask. CAR-bench includes hallucination tasks where required data/tool/result is removed and disambiguation tasks where the agent must resolve ambiguity.
- Relevance: Nanite should build UAT cases in this style: remove a page, omit a required filter, create two plausible entity IDs, or return a tool limit error; pass only if the agent asks, paginates, or states the gap.
- Caveat: Benchmarks can overfit. Use them as regression suites for failure classes, not as the only measure of user trust.

### Reliability alignment can expand the action space beyond "answer or call tool"

- Source: [Reducing Tool Hallucination via Reliability Alignment](https://github.com/X-LANCE/ToolHallucination), [ToolBeHonest](https://arxiv.org/abs/2406.20015)
- Synopsis: Tool-hallucination work adds alternatives such as changing tools, talking to the user, or refusing ill-posed calls, then trains or ranks outputs to prefer non-hallucinating behavior.
- Relevance: Nanite's strategy loop should explicitly include actions like `ask_user`, `fetch_more`, `narrow_scope`, `delegate_probe`, `state_insufficient_data`, and `stop_with_gap`, not just `call_tool` and `final_answer`.
- Caveat: If "ask_user" is overused, the agent feels timid. The playbook needs thresholds for when asking is better than a cheap deterministic probe.

### Permission classifiers and policy gates are becoming product features

- Source: [Claude Code permissions](https://code.claude.com/docs/en/permissions), [Claude Agent SDK permissions](https://platform.claude.com/docs/en/agent-sdk/permissions), [Claude Code hooks reference](https://code.claude.com/docs/en/hooks)
- Synopsis: Claude Code has permission modes, allow/deny rules, hooks, and auto mode with safety checks. This makes action gating a harness responsibility, not a model-only behavior.
- Relevance: Nanite can use the same pattern for interaction quality: broker gates should allow/deny/escalate based on data sufficiency, not only safety.
- Caveat: Classifier-based gates can false-positive and block useful work. Keep them explainable and overrideable.

## What Anthropic's Direction Implies

Anthropic's public direction is moving toward a thinner always-on prompt and a richer harness: deferred tool loading, typed tool contracts, MCP annotations, hookable permission gates, subagents, memory files, skills, and evented agent loops. Their repeated message is that reliability comes from the agent-computer interface and evaluation, not from one giant instruction block.

For Nanite, the strongest implication is to move "interaction quality" out of prompt prose and into broker-owned state machines:

- Use MCP `outputSchema`/`structuredContent` as the source of truth for cards and factual fields.
- Treat rule selection like Tool Search: classify first, retrieve compact relevant rules second.
- Maintain per-tool enrichment records with provenance, risk, pagination, output shape, examples, and known failure modes.
- Add a strategy loop that can choose clarification, pagination, delegation, background work, or honest wrap-up before the main model writes the final answer.
- Make scratchpad, memory, and playbook selection structured harness state, not hidden model text.
- Split stream events into progress, tool activity, checkpoints, and final answer.

The caveat is also clear from Anthropic's docs: they still recommend starting simple, measuring, and adding complexity only when it improves outcomes. The architectural sprint should therefore ship with trace-level evals for the exact UAT failures: fabricated rows, relabeled unfiltered lists, thin-data summaries, extrapolated IDs, missed pagination, and failure to ask under ambiguity.
