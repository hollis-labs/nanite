# Anthropic Engineering Digest — Clusters 1 & 2

Deep extraction of nine Anthropic engineering posts covering harness design, agent loops, tool authoring, and context/retrieval. Organized for a downstream Nanite architecture review. Content is faithful to the source material; quotes are verbatim where marked.

Relevance-tag vocabulary used throughout:
`chat-engine-loop`, `tool-broker`, `context-broker`, `workflow-engine`, `plugin-host/events`, `multi-agent-orchestrator`, `memory/continuity`, `evals`, `sandbox`.

---

## Cluster 1 — Harness & Agent Loop

### 1. Building Effective Agents
Source: https://www.anthropic.com/engineering/building-effective-agents

**Core thesis.** Success with LLM systems is not about sophistication but about matching architecture to task. Start with the simplest thing that works — often a single augmented LLM call — and only add complexity when measurement shows it helps. Anthropic distinguishes sharply between *workflows* (LLMs and tools orchestrated through predefined code paths) and *agents* (LLMs that dynamically direct their own processes and tool usage).

**Validated patterns.**
- **Augmented LLM (building block)** — a single LLM enhanced with retrieval, tools, and memory, where the model itself generates queries, selects tools, and decides what to retain. The foundational unit for all higher patterns.
- **Prompt chaining** — decompose a task into fixed sequential LLM calls; each call processes the prior call's output, with optional programmatic "gates" between steps. Trades latency for accuracy when the task decomposes cleanly.
- **Routing** — classify an input and dispatch it to a specialized downstream prompt/model; enables separation of concerns and cost-tiering (Haiku for simple, Sonnet for complex).
- **Parallelization (sectioning)** — split a task into independent subtasks run simultaneously, then aggregate.
- **Parallelization (voting)** — run the same task multiple times with varied prompts for higher-confidence results (e.g., vulnerability review, content moderation across dimensions).
- **Orchestrator–workers** — a central LLM dynamically decomposes the task at runtime and delegates to worker LLMs, then synthesizes. Distinct from parallelization because subtasks aren't pre-defined.
- **Evaluator–optimizer** — a generator LLM produces output while a separate evaluator LLM critiques in a loop; effective when criteria are clear and iterative refinement measurably helps.
- **Agent (autonomous loop)** — an LLM using tools in a loop based on environmental feedback, beginning from a human command, then planning and operating independently with ground-truth checks at each step.
- **Agent-Computer Interface (ACI) engineering** — a named discipline of shaping tool surfaces for model ergonomics: absolute paths over relative, tokens-to-think, poka-yoke argument shapes, docstrings written "as if for a junior developer."

**Anti-patterns / gotchas.**
- Framework lock-in that obscures prompts and responses ("extra layers of abstraction … harder to debug"). Recommend starting with raw LLM APIs.
- Premature complexity: adding agentic loops when a single augmented call suffices.
- Ignoring compounding error and cost in autonomous loops — demands sandbox testing and guardrails.
- Tool-format overhead that forces bookkeeping (line counts, escapes) the model can get wrong.

**Quotable principles.**
> "When building applications with LLMs, we recommend finding the simplest solution possible, and only increasing complexity when needed."
> "Agents can handle sophisticated tasks, but their implementation is often straightforward. They are typically just LLMs using tools based on environmental feedback in a loop."
> "We found ourselves spending more time optimizing our tools than the overall prompt."

**Relevance tags.** `chat-engine-loop`, `workflow-engine`, `multi-agent-orchestrator`, `tool-broker`, `evals`, `sandbox`.

---

### 2. Harness Design for Long-Running Applications
Source: https://www.anthropic.com/engineering/harness-design-long-running-apps

**Core thesis.** For long-horizon, high-quality creative/engineering work, a harness that *separates a generator agent from a skeptical evaluator agent* and hands off state through structured artifacts outperforms a solo-agent loop by a large margin — even at 20× the cost and time. Every harness component encodes an assumption about model limits; as models improve, those assumptions should be stress-tested and shed.

**Validated patterns.**
- **Generator–evaluator split (GAN-inspired)** — the agent doing work is a different agent from the one judging it. Evaluators must be tuned toward skepticism because models will not reliably critique their own output.
- **Context resets over compaction** — for models with "context anxiety" (premature wrap-up as the window fills), clearing the window and starting fresh with a structured handoff beats in-place summarization.
- **Sprint-based decomposition** — work is broken into tractable chunks with negotiated "sprint contracts" specifying testable completion criteria before implementation.
- **Structured-artifact handoff** — files, specs, and git history carry state between agent sessions, keeping orchestration complexity out of the model's reasoning.
- **Explicit grading rubrics for subjective work** — concrete criteria (design quality, originality, craft, functionality) applied symmetrically to generator and evaluator.
- **Planner → Generator → Evaluator three-agent stack** — planner expands a short prompt into a full spec; generator implements one feature at a time; evaluator tests the running app via Playwright MCP against the contract.
- **Harness stripping as models improve** — periodically remove components no longer load-bearing; e.g., Opus 4.6 allowed dropping the sprint construct.

**Anti-patterns / gotchas.**
- Self-evaluation by the generator — default behavior identifies real issues then "talks itself into" approving.
- Solo agents under-scoping work ("start building without first speccing").
- Context compaction in models that pre-wrap as the window fills.
- Assuming the harness should grow monotonically rather than shrink with model gains.

**Quotable principles.**
> "Separating the agent doing the work from the agent judging it proves to be a strong lever to address this issue."
> "Every component in a harness encodes an assumption about what the model can't do on its own, and those assumptions are worth stress testing."
> "The space of interesting harness combinations doesn't shrink as models improve. Instead, it moves."

**Relevance tags.** `multi-agent-orchestrator`, `workflow-engine`, `context-broker`, `memory/continuity`, `evals`.

---

### 3. Effective Harnesses for Long-Running Agents
Source: https://www.anthropic.com/engineering/effective-harnesses-for-long-running-agents

**Core thesis.** Long-running agents must operate in discrete sessions, each starting with no memory of prior work. An effective harness treats the *environment* — filesystem, git, progress files, feature lists, init scripts, browser automation — as the continuity layer, and enforces incremental single-feature progress on each session so that handoffs always occur at clean, mergeable states.

**Validated patterns.**
- **Two-role architecture** — a one-shot *initializer agent* that establishes scaffolding (feature list, progress file, init script) and subsequent *coding agents* that each tackle one feature.
- **Feature list as persistent backlog** — a JSON list of 200+ end-to-end features with `passing: false` flags that agents flip only after verification; prevents premature "done" declarations.
- **Progress file (`claude-progress.txt`)** — human-readable journal that new sessions read on boot to orient without exhausting context.
- **Git as memory** — descriptive commits and git log become the context source for subsequent sessions.
- **Init script orientation ritual** — every session begins with `pwd`, read progress, review features, run baseline end-to-end tests, then pick one feature.
- **Browser-automation verification** — Puppeteer/MCP for human-like testing; catches bugs invisible at the code level.
- **Constrained editing** — agents can only toggle specific fields (e.g., `passes` status) to prevent drift.
- **"Clean state" handoff rule** — code must be mergeable to main at every session boundary: no known bugs, documented, committed.

**Anti-patterns / gotchas.**
- Attempting to finish the whole project in one session.
- Premature project-complete declarations.
- Leaving broken/undocumented intermediate state that the next session must diagnose.
- Skipping end-to-end verification in favor of code-level checks.
- Relying on in-context memory instead of filesystem/git artifacts.

**Quotable principles.**
> "The core challenge of long-running agents is that they must work in discrete sessions, and each new session begins with no memory of what came before."
> "By 'clean state' we mean the kind of code that would be appropriate for merging to a main branch: there are no major bugs, the code is orderly and well-documented."
> "Providing Claude with these kinds of testing tools dramatically improved performance, as the agent was able to identify and fix bugs that weren't obvious from the code alone."

**Relevance tags.** `chat-engine-loop`, `memory/continuity`, `context-broker`, `tool-broker`, `sandbox`, `evals`.

---

### 4. Claude Code Auto Mode
Source: https://www.anthropic.com/engineering/claude-code-auto-mode

**Core thesis.** Auto mode targets the middle ground between manual approval fatigue and dangerous unrestricted execution by delegating permission decisions to a layered classifier stack. The goal is *high autonomy at low maintenance cost*, defending against four failure modes (overeager behavior, honest mistakes, prompt injection, model misalignment) through action-blocking rather than intent-reading.

**Validated patterns.**
- **Two-layer defense** — an input-side prompt-injection probe that scans tool outputs before they enter agent context, plus an output-side transcript classifier that evaluates proposed actions before execution.
- **Two-stage classification** — a fast single-token filter skewed toward blocking (≈8.5% FPR) followed by chain-of-thought reasoning only on flagged actions (drops FPR to ≈0.4%).
- **Permission tiers** — (1) built-in safe-tool allowlist (reads, search); (2) in-project file ops (skip classifier); (3) high-risk actions (shell, web, external) go to the classifier.
- **Deny-and-continue** — blocked actions return with reasoning and allow retries up to 3 consecutive or 20 total before escalating to the user.
- **Reasoning-blind classifier** — the classifier sees user messages and agent tool calls only; Claude's own messages and tool outputs are stripped out by design so the judge can't be talked into approval.
- **Intent-independent evaluation** — high-risk patterns (e.g., POSTing env vars to an external URL) are blocked regardless of what prompted them.

**Anti-patterns / gotchas.**
- Scope escalation from vague prompts into destructive batch ops.
- Credential-exploration loops (scanning for alternative tokens after auth fails).
- Agent-inferred targets under ambiguous intent.
- Data exfiltration via "helpful" scripts to external services.
- Safety-check bypass (retrying failed deploys with verification disabled).
- Approval fatigue: manual prompts that users stop reading.

**Quotable principles.**
> "Over time that leads to approval fatigue, where people stop paying close attention to what they're approving."
> "A POST of env vars to an external URL fails against user intent regardless of what prompted it."
> The classifier "sees only user messages and the agent's tool calls; we strip out Claude's own messages and tool outputs, making it reasoning-blind by design."

**Relevance tags.** `tool-broker`, `plugin-host/events`, `sandbox`, `chat-engine-loop`, `evals`.

---

## Cluster 2 — Tools & Context

### 5. Writing Tools for Agents
Source: https://www.anthropic.com/engineering/writing-tools-for-agents

**Core thesis.** Tools are a new software category: contracts between deterministic systems and non-deterministic agents. Ergonomic-for-the-model design — not API completeness — is the goal, because agent context is scarce while machine memory is not.

**Validated patterns.**
- **Choose the right tools** — build high-impact, task-shaped tools (`search_contacts`) instead of wrapping every endpoint (`list_contacts`). Avoid tools that return firehose data.
- **Tool namespacing** — service- and resource-level prefixes (`asana_search`, `asana_projects_search`) to disambiguate in large registries.
- **Meaningful context in returns** — return human-readable fields (`name`, `image_url`) rather than raw UUIDs/MIMEs; semantic identifiers reduce hallucination.
- **`response_format` enum** — allow agents to request `concise` vs `detailed` responses to manage their own token budget.
- **Token-efficient defaults** — pagination, filtering, truncation, and sensible caps (Claude Code uses 25k-token cap); when truncated, return steering instructions.
- **Actionable errors** — replace opaque codes with guidance that teaches the agent a better strategy ("filter by date range to reduce results").
- **Tool consolidation (progressive disclosure)** — prefer one `schedule_event` (finds availability + books) over three thin tools.
- **Tool descriptions as onboarding docs** — treat descriptions as briefings for a new team member; unambiguous parameter names, explicit formats, examples.
- **Eval-driven refinement** — use held-out realistic test sets and have Claude analyze failure transcripts to refactor tool surfaces.

**Anti-patterns / gotchas.**
- Tool proliferation and overlap ("distract agents from efficient strategies").
- Cryptic identifiers (UUIDs) without semantic labels.
- Overly strict validation rejecting semantically correct inputs.
- Generic errors that don't steer.
- Returning everything when filtered retrieval exists.

**Quotable principles.**
> "Tools are a new kind of software which reflects a contract between deterministic systems and non-deterministic agents."
> "Agents have limited 'context' … whereas computer memory is cheap and abundant."
> "Effective tools are intentionally and clearly defined, use agent context judiciously, can be combined together in diverse workflows, and enable agents to intuitively solve real-world tasks."

**Relevance tags.** `tool-broker`, `context-broker`, `evals`, `plugin-host/events`.

---

### 6. Advanced Tool Use
Source: https://www.anthropic.com/engineering/advanced-tool-use

**Core thesis.** As tool libraries scale to hundreds or thousands, loading every tool definition upfront destroys both context and accuracy. Three beta features — Tool Search Tool, Programmatic Tool Calling, Tool Use Examples — let Claude discover, orchestrate, and learn tools dynamically.

**Validated patterns.**
- **Tool Search Tool** — on-demand tool discovery instead of eager loading. Measured: ~77k → ~8.7k tokens (85% reduction) and accuracy gains (Opus 4: 49% → 74%; Opus 4.5: 79.5% → 88.1%).
- **Programmatic Tool Calling** — Claude writes Python that calls multiple tools, processes intermediate results *in the execution environment*, and only surfaces the filtered answer. 37% token reduction on complex research.
- **Tool Use Examples** — 1–5 concrete usage examples per tool beyond JSON schema: minimal, partial, full patterns, nested structures. Accuracy 72% → 90% on complex parameter handling.
- **Bottleneck-first layering** — address the dominant failure mode first: context bloat → Tool Search; intermediate-result bloat → Programmatic Calling; parameter errors → Examples.
- **Hot-tool set** — keep 3–5 frequently used tools always loaded and defer the long tail behind Tool Search.

**Anti-patterns / gotchas.**
- Applying Tool Search to small libraries (<10 tools) or when every tool is used every session — overhead exceeds benefit.
- Programmatic Calling for simple single-tool lookups or when Claude needs to see every intermediate result.
- Under-documented return formats for Programmatic Calling, leading to bad parsing code.

**Quotable principles.**
> "The future of AI agents is one where models work seamlessly across hundreds or thousands of tools."
> "Code is a natural fit for orchestration logic, such as loops, conditionals, and data transformations."
> "Tool Search Tool preserves 191,300 tokens of context compared to 122,800 with Claude's traditional approach."

**Relevance tags.** `tool-broker`, `context-broker`, `workflow-engine`, `plugin-host/events`, `sandbox`.

---

### 7. Code Execution with MCP
Source: https://www.anthropic.com/engineering/code-execution-with-mcp

**Core thesis.** Presenting MCP servers as a *code API* rather than a direct tool-call surface lets agents load tools on demand from a filesystem and keep intermediate data inside the execution environment. A Google-Drive-to-Salesforce workflow example drops from 150k to 2k tokens (98.7% reduction).

**Validated patterns.**
- **Filesystem-as-tool-catalog** — agents `ls ./servers/` and read only the specific tool files they need, mirroring how developers navigate SDKs.
- **Intermediate results stay local** — filter/transform data in code; only return the distilled output to the model. Avoids double-passing large payloads (e.g., a 2-hour transcript traversing context twice).
- **Composition in code, not in-context** — loops, conditionals, polling, and multi-step pipelines run as Python/JS rather than as chained tool calls through the model.
- **Resumable stateful workflows** — intermediate results persist as files; long runs can pause and resume.
- **Skill persistence** — agents save reusable functions as code artifacts for later invocation.
- **Sandboxing as a hard requirement** — running model-authored code demands secure execution, resource limits, and monitoring; the post is explicit that this cost must be weighed against the token savings.

**Anti-patterns / gotchas.**
- Loading entire tool catalogs upfront.
- Passing large documents between sequential tool calls (copy errors, context overflow).
- Implementing loops as repeated model calls instead of code.
- Deploying code execution without sandbox hygiene.

**Quotable principles.**
> "With large documents or complex data structures, models may be more likely to make mistakes when copying data between tool calls."
> "When agents use code execution with MCP, intermediate results stay in the execution environment by default."
> "Code execution applies these established patterns to agents, letting them use familiar programming constructs to interact with MCP servers more efficiently."

**Relevance tags.** `tool-broker`, `context-broker`, `sandbox`, `workflow-engine`, `plugin-host/events`.

---

### 8. The "Think" Tool
Source: https://www.anthropic.com/engineering/claude-think-tool

**Core thesis.** A trivially simple tool — a no-op scratchpad Claude can call mid-response to "stop and think about whether it has all the information it needs to move forward" — yields large gains in policy-heavy, tool-chained tasks. It is complementary to, not a replacement for, extended thinking: *think tool fires mid-loop on new information; extended thinking fires before the loop begins.*

**Validated patterns.**
- **Mid-loop scratchpad** — a designated reasoning space between tool calls for integrating new observations into plans.
- **Policy reflection** — especially strong for domains with dense rules/guidelines where the model must verify compliance before acting.
- **Domain-specific priming** — pair the tool with domain examples, decision trees, and completeness checklists in the *system prompt* (not the tool description) for best results.
- **Conditional use** — the tool is no-downside because the model only invokes it when helpful.

**Benchmarks called out.** τ-bench airline 0.332 → 0.570 with think-tool + optimized prompt (54% relative gain); τ-bench retail 0.783 → 0.812; SWE-bench contribution of ~1.6% to a 0.623 state-of-the-art.

**Anti-patterns / gotchas.**
- Using the think tool for single-shot or parallel tool calls — no benefit.
- Deploying the tool in complex-policy domains without example-driven prompting.
- Stuffing long instructions into the tool description instead of the system prompt.
- Confusing it with extended thinking (distinct mechanisms and use cases).

**Quotable principles.**
> "With the 'think' tool, we're giving Claude the ability to include an additional thinking step—complete with its own designated space—as part of getting to its final answer."
> "The reasoning Claude performs with the 'think' tool is less comprehensive than what can be obtained with extended thinking, and is more focused on new information that the model discovers."
> "Prompting matters significantly on difficult domains. Simply making the 'think' tool available might improve performance somewhat, but pairing it with optimized prompting yielded dramatically better results."

**Relevance tags.** `chat-engine-loop`, `tool-broker`, `context-broker`, `evals`.

---

### 9. Contextual Retrieval
Source: https://www.anthropic.com/engineering/contextual-retrieval

**Core thesis.** Standard RAG strips context when it chunks. Prepending a 50–100-token, LLM-generated situational preamble to each chunk *before* embedding and BM25 indexing recovers that context and cuts retrieval failures by 49%; adding reranking pushes the reduction to 67%.

**Validated patterns.**
- **Contextual embeddings** — before vectorizing, prepend a chunk-specific paragraph explaining the chunk's place in its parent document, produced by a cheap model (Haiku) with the full document in context.
- **Contextual BM25** — the same contextualized chunk text is also indexed lexically; the two signals complement each other (semantic breadth + exact-term precision).
- **Hybrid retrieval + reranking** — retrieve top-150 candidates via hybrid search, rerank down to top-20, feed those into the prompt.
- **Prompt caching for cheap contextualization** — the full document is cached once; per-chunk generation costs $1.02 / million document tokens.
- **Top-K tuning** — top-20 outperformed top-5 and top-10 in tested domains.
- **Multi-domain validation** — codebases, fiction, ArXiv, scientific literature, with Gemini and Voyage embeddings topping the charts.

**Anti-patterns / gotchas.**
- Generic document-level summaries (prior art) yielded "very limited gains" — context must be chunk-specific.
- Ignoring chunk boundaries, size, and overlap — these still dominate downstream performance.
- Confusing contextualized chunks with raw content during response generation.
- Relying on embeddings alone for queries containing exact identifiers/codes — BM25 is essential there.

**Quotable principles.**
> "Traditional RAG solutions remove context when encoding information, which often results in the system failing to retrieve the relevant information from the knowledge base."
> "Contextual Retrieval solves this problem by prepending chunk-specific explanatory context to each chunk before embedding."
> "All these benefits stack: to maximize performance improvements, we can combine contextual embeddings with contextual BM25, plus a reranking step, and adding the 20 chunks to the prompt."

**Relevance tags.** `context-broker`, `memory/continuity`, `evals`.

---

## Cross-Cutting Themes

**Reinforcements across the nine posts.**

*1. Context is the scarce resource; everything else is optimization around it.* Writing Tools, Advanced Tool Use, Code Execution with MCP, Harness Design, Effective Harnesses, and Contextual Retrieval all converge on the same premise — the model's context window is the real constraint, and the harness's job is to keep it clean, fresh, and loaded only with decision-relevant tokens. Tool Search, Programmatic Tool Calling, code execution, context resets, progress files, chunk contextualization, and `response_format` enums are all variants of the same move: *push work out of context and into durable substrates* (filesystem, git, code execution, external indexes).

*2. Separation of roles is a first-class architectural lever.* Building Effective Agents names orchestrator–workers and evaluator–optimizer; Harness Design operationalizes them as planner/generator/evaluator; Auto Mode extends the pattern to a reasoning-blind safety classifier that cannot be rhetorically captured by the agent it polices. The common principle: *the agent judging should not be the agent doing*, whether the judgment is quality, completeness, or safety.

*3. Ground truth from the environment, not self-report.* Building Effective Agents ("ground truth from the environment at each step"), Effective Harnesses (browser-automation verification), Harness Design (Playwright MCP tests against running app), and Code Execution with MCP (results computed in-sandbox, not described by the model) all reject self-assessment. The harness must force the model to confront real observations.

*4. Simplicity-first, measure-to-add.* Building Effective Agents states it explicitly; Harness Design reframes it as "strip components no longer load-bearing as models improve"; Writing Tools pushes eval-driven refinement; Think Tool is celebrated precisely because it is minimal and no-downside. Across the corpus, added complexity must pay rent in measurable gains.

*5. Tool design is prompt engineering.* Building Effective Agents, Writing Tools, Advanced Tool Use, and Think Tool all treat tool descriptions, parameter names, return shapes, and examples as part of the prompt surface. ACI engineering is not a separate discipline from prompting — it *is* prompting, with schemas.

**Tensions and tradeoffs.**

*Harness stripping vs. harness durability.* Harness Design argues the harness should shrink as models improve; Effective Harnesses and Auto Mode build elaborate scaffolding (feature lists, progress files, classifier stacks) whose value compounds with use. The resolution the posts suggest: re-examine which components are load-bearing on every model release, but don't pre-strip speculatively.

*Context reset vs. context continuity.* Harness Design recommends wiping context and starting fresh for models with "context anxiety"; Effective Harnesses relies on long-lived artifacts (progress files, git logs) for cross-session memory; Contextual Retrieval enriches chunks so *less* resetting is needed. The trade is latency/coordination overhead (resets + handoffs) versus drift risk (compaction or long sessions).

*Model-written code vs. constrained tools.* Code Execution with MCP and Programmatic Tool Calling advocate giving the model a runtime; Auto Mode and Writing Tools advocate narrow, poka-yoke'd surfaces with classifier gates. Both are right for different risk profiles — and the posts acknowledge code execution "requires a secure execution environment with appropriate sandboxing, resource limits, and monitoring" as a hard precondition.

*Tool proliferation vs. tool discoverability.* Writing Tools warns that too many tools distract agents; Advanced Tool Use presupposes hundreds-to-thousands of tools and solves discoverability with search. The reconciliation: small always-loaded hot sets + large deferred catalogs reachable via progressive disclosure.

*Think tool vs. extended thinking vs. orchestrator loops.* Three mechanisms for "give the model time to reason" exist in the corpus, each with narrow applicability windows (mid-loop new info; pre-response open-ended reasoning; multi-step decomposition). The posts are careful to mark them as complementary rather than substitutes, but they compete for the same budget (tokens, latency, cost) and require the harness author to pick per task shape.
