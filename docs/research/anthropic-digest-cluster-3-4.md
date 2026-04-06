# Anthropic Engineering Digest — Clusters 3 & 4

Source digest of four Anthropic engineering posts, compiled for downstream architecture review of Nanite. Faithful extraction only; no Nanite-specific recommendations.

Posts covered:
- Cluster 3: multi-agent research system; Claude Code sandboxing; infrastructure noise in agentic coding evals
- Cluster 4: demystifying evals for AI agents

---

## 1. How we built our multi-agent research system

### Core thesis
Multi-agent systems outperform single agents on open-ended research because parallel exploration spends more tokens across independent context windows — but the 15x token overhead only pays off for high-value, inherently parallelizable tasks, and emergent coordination failures must be engineered around deliberately.

### Validated patterns
- **Orchestrator-worker (lead + subagent) architecture.** A lead agent (Claude Opus 4) plans the strategy and spawns 3–5 parallel subagents (Claude Sonnet 4). Each subagent runs in its own context window with a distinct research task. Opus-lead + Sonnet-subagents beat single-agent Opus 4 by 90.2% on their internal research eval.
- **Extended thinking in the lead; interleaved thinking in subagents.** Lead uses extended thinking to plan, assess tool fit, determine complexity, and decide subagent count and roles. Subagents use interleaved thinking after each tool call to evaluate result quality and refine the next query.
- **Effort scaling baked into the prompt.** Explicit rules: simple fact-finding = 1 agent, 3–10 tool calls; direct comparisons = 2–4 subagents, 10–15 calls each; complex research = 10+ subagents with clearly divided responsibilities. This prevents the lead from spawning 50 subagents for trivial questions.
- **Parallel tool calls at two levels.** Lead spawns subagents in parallel (not serially); each subagent issues 3+ tool calls in parallel. Combined, this cuts complex-query research time by up to 90%.
- **Explicit task decomposition by the lead.** Subtasks include objective, output format, tool/source guidance, and hard task boundaries to eliminate duplicated work.
- **Memory externalization across context limits.** When approaching the 200K-token limit, the lead agent summarizes completed work and writes the plan to external memory; subagents retrieve stored context rather than losing it through truncation.
- **Subagent outputs written directly to filesystem.** Large artifacts (code, reports, visualizations) bypass the lead's conversation; only lightweight references are passed back. This avoids copying large payloads through the orchestrator's context.
- **Tool-description hardening via a tool-testing agent.** A separate Claude 4 agent exercises tools dozens of times, rewrites flawed descriptions, and surfaces bugs. This produced a 40% decrease in task completion time.
- **Source-quality heuristics encoded in prompts.** Prefer specialized tools over generic; prefer authoritative sources (academic PDFs, primary sources) over SEO content farms.
- **Progressive search broadening.** Start short and broad, evaluate, then narrow — not start specific and fail.
- **LLM-as-judge with a single grader.** One LLM call, one prompt, 0.0–1.0 score + pass/fail, across factual accuracy, citation accuracy, completeness, source quality, and tool efficiency. A single judge outperformed multi-judge ensembles.
- **End-state evaluation over turn-by-turn.** Stateful agents take many valid paths; grade the final state achieved, not the trajectory steps.
- **Rainbow deployments.** Traffic gradually shifts old → new while both versions run concurrently so deploys don't break in-flight agents.
- **Privacy-preserving production tracing.** High-level tracing of decisions and interaction structures without recording conversation contents.

### Anti-patterns / gotchas (exact failure modes and mitigations)
- **Token cost.** Agents use ~4x more tokens than chat; multi-agent systems ~15x. Token usage alone explains 80% of performance variance on BrowseComp; tool calls and model choice cover another 15%. Mitigation: restrict multi-agent patterns to high-value parallelizable work; do not use for tasks requiring shared context (most coding tasks).
- **Over-spawning.** Early versions spawned 50 subagents for simple queries. Mitigation: embedded scaling rules in the orchestrator prompt.
- **Coordination overhead / excessive updates.** Agents chattered at each other and distracted themselves. Mitigation: define clear division of labor in the task description.
- **Subagent drift and duplicated work.** Vague instructions ("research the semiconductor shortage") produced three subagents overlapping on 2021 vs 2025 searches. Mitigation: lead must produce explicit, non-overlapping subtask descriptions with boundaries.
- **Synchronous bottleneck.** The lead blocks on all subagents before proceeding; a stalled subagent halts the system and the lead cannot steer subagents mid-flight. Acknowledged limitation, not fully solved.
- **Compounding errors in long trajectories.** Minor failures cascade into large behavioral changes because agents maintain state across many tool calls. Mitigation: resume from the error point rather than restart; deterministic retry logic and regular checkpoints.
- **Endless scouring for nonexistent sources.** Mitigation: source-quality heuristics and guardrails.
- **Eval brittleness from nondeterminism.** Identical prompts produce different runs; "not finding obvious info" bugs are hard to reproduce. Mitigation: end-state evals, human review for edge cases.
- **Automated evals miss hallucinations, source bias, system failures.** Mitigation: mandatory human eval alongside LLM-judge.

### Quotable principles
> "Multi-agent systems work mainly because they help spend enough tokens to solve the problem."

> "Start with small-scale testing right away with a few examples, rather than delaying until you can build more thorough evals."

> "The compound nature of errors in agentic systems means that minor issues for traditional software can derail agents entirely."

### Relevance tags
`multi-agent-orchestrator`, `chat-engine-loop`, `tool-broker`, `context-broker`, `memory/continuity`, `workflow-engine`, `evals`

---

## 2. Claude Code sandboxing

### Core thesis
OS-level sandboxing with dual filesystem and network isolation removes 84% of permission prompts while containing prompt-injection blast radius — both boundaries are required; either alone is insufficient.

### Validated patterns
- **Dual-isolation model.** Filesystem isolation and network isolation together. Without network isolation a compromised agent exfiltrates SSH keys; without filesystem isolation it escapes and gains network access.
- **Filesystem isolation scoped to CWD.** Read and write are allowed inside the current working directory only. All paths outside are blocked, including for spawned subprocesses (sandbox inherits through fork/exec).
- **Network via unix domain socket to an out-of-sandbox proxy.** All outbound traffic routes through a Unix socket to a proxy running outside the sandbox. The proxy enforces domain allowlists and handles user confirmation for new domains. The proxy is customizable for arbitrary egress policies.
- **OS primitive selection.** Linux uses bubblewrap (bind mounts to restrict directory visibility). macOS uses the seatbelt sandbox with equivalent directory policies. Both platforms use the same proxy model for network.
- **Default permission behavior (pre-sandbox).** Read-only by default; safe commands (`echo`, `cat`, etc.) auto-allowed; most development commands prompted. Sandboxing replaces the prompt treadmill rather than just muting it.
- **Claude Code on the Web variant.** Each session runs in an isolated cloud sandbox. Git credentials and signing keys live outside the sandbox. A git proxy transparently attaches scoped real credentials after validating auth tokens, branch names, and repository destinations.
- **Open-source sandbox runtime.** Published as `anthropic-experimental/sandbox-runtime` so other agents can adopt the same model.

### Anti-patterns / gotchas
- **Approval fatigue.** Constantly clicking approve slows dev cycles and leads to users rubber-stamping dangerous actions — making a "safer" prompting UX actually less safe. This was the core motivator for sandboxing.
- **Single-boundary sandboxing is insufficient.** Either filesystem-only or network-only leaves a pivot path. Both must be enforced.
- **Silent failure semantics.** Attempts to access files outside CWD fail silently at the OS level; network requests to unapproved domains block at the proxy layer. Applications don't get a chance to circumvent.
- **No YOLO / skip-permissions mode discussed.** The post does not mention `--dangerously-skip-permissions` or any unrestricted toggle. Sandboxing is framed as *the* alternative to prompting, not an optional layer.

### Quotable principles
> "It's by using both techniques that we can provide a safer and faster agentic experience for Claude Code users."

> "Sandboxing ensures that even a successful prompt injection is fully isolated, and cannot impact overall user security."

> "By defining set boundaries within which Claude can work freely, they increase security and agency."

### Permission model details (inferred permission matrix)
| Scope | Default (no sandbox) | Sandbox enabled | Override |
| --- | --- | --- | --- |
| Read inside CWD | Prompt | Auto-allowed | N/A |
| Write inside CWD | Prompt | Auto-allowed | N/A |
| Read outside CWD | Blocked | Blocked by OS | User notified on attempt |
| Write outside CWD | Blocked | Blocked by OS | User notified on attempt |
| Network to pre-approved host | Prompt | Auto-allowed | Per-request |
| Network to new host | Blocked | Prompted via proxy | Per-request |

### Relevance tags
`sandbox`, `shell-feature`, `tool-broker`, `plugin-host/events`

---

## 3. Infrastructure noise in agentic coding evals

### Core thesis
On agentic coding benchmarks, resource-enforcement methodology alone can swing scores by 6 percentage points — often more than the gap between top leaderboard positions — so infra configuration changes *what is being measured*, not just *how reliably*.

### Validated patterns
- **Decomposition of stability gains vs capability gains.** Up to ~3x resource headroom, score improvements are infrastructure noise reduction (OOM kills dropped 5.8% → 2.1%, p < 0.001; score gain p = 0.40). Beyond 3x, additional resources enable genuinely new solution strategies (dependency installs, subprocess use, memory-heavy libraries).
- **Separate guaranteed allocation from hard-kill threshold.** Container runtimes expose both; collapsing them into one value makes transient spikes fatal. Their calibrated setup: guarantee = spec, hard kill = 3x spec.
- **Multi-configuration sweeps.** Run the same model, same harness, same tasks across 1x, 1.5x, 2x, 2.5x, 3x, uncapped. Track infra error rates and task success separately per config.
- **Cross-benchmark replication.** They confirmed the pattern on SWE-bench (5x vs 1x RAM across 227 problems, 10 samples each) — monotonic improvement, though smaller (1.54 pp).
- **Infra error rate as a first-class metric.** Pod crashes, timeouts, and OOM kills tracked independently as a leading indicator of measurement validity.
- **Time/day averaging.** Multiple runs at different times and days to smooth out API latency and cluster-health variance.
- **Published methodology requirement.** Benchmark scores should always be reported alongside CPU/RAM floor, ceiling, enforcement method, grace periods, and multiplier justification.

### Anti-patterns / gotchas
- **Pinning a single spec as both floor and ceiling.** Leaves zero headroom for transient spikes; rewards noise-free runs rather than capability.
- **Reporting scores without config.** A 2-point leaderboard lead could be real, or bigger VMs, or time-of-day luck.
- **Conflating stability and capability gains.** Below the ~3x threshold the two look identical on the score line.
- **Ignoring model-specific strategy selection.** On `bn-fit-modify`, some models install pandas/networkx/scikit-learn; others implement math from stdlib. Tight limits kill the first group before they write code, inverting rankings.
- **Single-run evaluations.** API latency, cluster heterogeneity, and concurrency level all vary run-to-run.

### Noise sources encountered
| Source | Manifestation | Measured impact |
| --- | --- | --- |
| Container enforcement | Guaranteed alloc ≠ hard kill | 5.8% task failures @ 1x → 0.5% uncapped |
| Model strategy | Dependency stack vs stdlib on `bn-fit-modify` | Ranking flips between configs |
| API latency | Time-of-day traffic | Fluctuating pass rates (not quantified) |
| Cluster heterogeneity | Node health / hardware variance | Contributes to variance |

### Quotable principles
> "Two agents with different resource budgets and time limits aren't taking the same test."

> "Tight limits inadvertently reward very efficient strategies, while generous limits reward agents that can better exploit all available resources."

> "A few-point lead might signal a real capability gap — or it might just be a bigger VM."

### Relevance tags
`evals`, `workflow-engine`, `plugin-host/events`, `sandbox`, `shell-feature`

---

## 4. Demystifying evals for AI agents

### Core thesis
Systematic evals turn agent development from reactive debugging into proactive iteration; teams without them "fly blind," while teams with them ship confidently and adopt new models in days instead of weeks.

### Validated patterns
- **Bootstrap with 20–50 tasks** from manual pre-release testing, user-reported failures, and bug tracker mining. Prioritize by user impact.
- **Unambiguous tasks with reference solutions** so domain experts can independently verify pass/fail.
- **Balanced positive/negative cases.** Test both "when the agent should search" and "when it shouldn't" (e.g., foundational facts). One-sided evals produce one-sided optimization.
- **Grader taxonomy.** Code-based (fast, cheap, brittle), model-based / LLM-as-judge (flexible, non-deterministic, expensive, requires calibration), and human (gold standard, slow, expensive).
- **Rubric design for LLM judges.** Isolate each dimension into its own LLM call to reduce hallucination; provide structured rubrics; include "Unknown" escape hatches; calibrate frequently against human experts.
- **End-to-end (outcome) over path grading.** Grade final environment state (e.g., reservation exists in DB, refund processed) rather than an enforced tool-call sequence.
- **Partial credit.** Support continuum of success: an agent that identifies the problem and verifies identity but fails to refund is meaningfully better than one that fails immediately.
- **Trajectory evals.** Record outputs, tool calls, reasoning, intermediate results, API messages. Score transcript-level (turns, tokens, latency), outcome-level, and quality-level (LLM rubrics on code quality, tone, reasoning).
- **pass@k vs pass^k metrics.** `pass@k` = ≥1 success in k trials (use for exploratory tasks); `pass^k` = all k trials succeed (use for production consistency). Example: 75% per-trial × 3 = 42.1% pass^3.
- **Capability vs regression suites.** Capability evals start low and provide a hill to climb; regression evals sit near 100% and catch drift. Saturated capability evals graduate into the regression suite.
- **Layered methods (Swiss Cheese model).** Automated offline evals, production monitoring, A/B tests, user feedback, manual transcript review, systematic human studies. No single method catches every issue.
- **Isolated trial environments.** Clean state per trial to prevent shared-state contamination and prior-run git-history leakage.
- **Eval-driven development.** Write evals before the agent can pass them; on new model release, run the suite to reveal which capability bets paid off.
- **Dedicated ownership of eval suites** as living artifacts with routine maintenance, contributed to via PR by product managers and customer success teams (often authored via Claude Code).
- **Transcript reading is non-negotiable.** "We do not take eval scores at face value until someone digs into the details of the eval and reads some transcripts."
- **Agent-type-specific patterns.** Coding = deterministic tests + static analysis + LLM quality rubric. Conversational = task state + LLM interaction rubric + turn-limit + simulated user. Research = groundedness + coverage + source quality + synthesis rubric, with frequent human calibration. Computer use = deterministic outcome checks on files, DBs, UI state; verify token-efficient modality choice (DOM vs screenshot).

### Anti-patterns / gotchas
- **Over-constraining step sequences.** Penalizes valid creative solutions.
- **Rigid string matching.** `"96.12"` vs `"96.124991…"` fails unnecessarily — use fuzzy / regex / outcome checks.
- **Ambiguous task specs.** Unstated success criteria cause capability-unrelated failures.
- **Uncalibrated LLM judges.** Divergence from human experts invalidates the suite.
- **Hallucinating judges.** Invent transcript content; mitigate via structured rubrics, isolated dimensions, and "Unknown" option.
- **Silent eval drift.** Stale evals create false confidence when products and models evolve.
- **Insufficient human review.** Masks grading bugs, task ambiguity, and harness constraints.
- **Shared state between trials.** Cached files, git history, resource exhaustion artificially shift scores.
- **Rigid harness scaffolding.** CORE-Bench's constrained harness vs flexible harness: 42% → 95% on Opus 4.5 for the same model, same tasks.
- **Bypass vulnerabilities.** Tasks that can be passed via loopholes rather than solving them.
- **Class imbalance.** Testing only positive cases produces over-eager agents.
- **Saturation signal loss.** At ~100%, no further signal; large improvements appear small (Qodo underestimated Opus 4.5 until the framework was rebuilt for longer agentic tasks).
- **0% pass@100** almost always signals a broken task rather than an incapable agent.

### Quotable principles
> "Good evaluations help teams ship AI agents more confidently. Without them, it's easy to get stuck in reactive loops — catching issues only in production, where fixing one failure creates others."

> "Two engineers reading the same initial spec could come away with different interpretations on how the AI should handle edge cases. An eval suite resolves this ambiguity."

> "The people closest to product requirements and users are best positioned to define success."

### Relevance tags
`evals`, `chat-engine-loop`, `tool-broker`, `multi-agent-orchestrator`, `workflow-engine`, `memory/continuity`

---

## Cross-cutting themes

These four posts reinforce each other along several axes.

**Measurement is inseparable from the system under test.** The evals post says explicitly that grading must reflect what agents actually produce, not a path the designer imagined. The infrastructure-noise post extends the same argument one layer down: the harness, container limits, and enforcement policy are *part* of the agent's capability surface. The multi-agent post agrees from a third angle — automated metrics alone miss hallucinations and emergent coordination failures, so human review is mandatory. Across all three, the shared claim is that a score without a documented environment is uninterpretable.

**Context is a scarce, engineered resource.** The multi-agent post leans hard on subagent context isolation, memory externalization, and direct-to-filesystem artifacts precisely because a single context window cannot hold a complex research trajectory. The sandbox post treats filesystem scope as a security boundary and a UX one. The evals post treats trial isolation as a correctness boundary (shared state corrupts measurement). All three treat "what the agent can see and touch" as an architectural primitive that must be designed, not inherited.

**Dual boundaries beat single boundaries.** Sandboxing insists both filesystem *and* network must be constrained because either alone leaks. Evals insists on layered methods (offline + production + human review) because no single layer catches every issue. Multi-agent insists on deterministic guardrails *plus* adaptive model behavior because neither survives alone. Infra noise insists on separating guaranteed allocation from hard-kill threshold because one value cannot express both goals. The pattern: when a single knob is asked to carry two responsibilities, it fails.

**Errors compound; recovery must be first-class.** The multi-agent post explicitly warns about compounding errors in long trajectories and recommends resume-from-error rather than restart. The evals post treats regression suites as a permanent safety net. The infra post treats a single flaky run as untrustworthy and mandates sweeps. The sandbox post treats prompt injection as inevitable and designs for blast-radius containment. None of the posts assume individual operations will succeed reliably; all assume durable state, checkpoints, and retries.

**Heuristics over rigid rules.** The multi-agent post explicitly prefers instilling human-expert heuristics in prompts over prescribing exact steps. The evals post prefers outcome grading over step enforcement. The sandbox post prefers defined boundaries within which the agent works freely. The infra post prefers sweep-and-attribute over a single authoritative number. The shared posture is: give the agent and the evaluator *latitude within boundaries*, because rigidity either breaks valid behavior or hides real signal.
