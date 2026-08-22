// Package loop implements the continuation-policy decision engine for
// docs/engineering/architecture/21-loops.md's goal-driven control layer: the
// function that computes CONTINUE | RETRY | REPLAN | REARCHITECT | WAIT |
// ESCALATE | COMPLETE | FAIL from a Goal, an iteration's Evaluation, its
// iteration history, and a Budget. Deterministic-first: goal-met and
// budget-exhausted are plain Go, no LLM call; only a sustained no-progress
// streak falls through to a reasoning fallback, and that fallback is a bare
// agentworkflow.StepExecutor.ExecuteLLMStep call — the identical pattern
// internal/service's VerifyModeAgent already uses for its own independent
// reviewer step — not a second workflow engine or a nested WorkflowRun.
//
// Naming note: the bare word "loop" is reachable from two other, different
// things in this codebase, and this package is neither of them:
//
//  1. internal/loopdetect — the harness's own per-turn tool-call repeat-count
//     guard (internal/loopdetect/detector.go). It detects a single agent
//     turn's reason-act-observe cycle repeating a tool call too many times
//     within one turn and fires a callback; it has no concept of a Goal, a
//     budget, or a multi-iteration WorkflowRun sequence. This package (loop)
//     operates one level up: it decides what happens *between* whole
//     WorkflowRun executions, not within one turn's tool-call loop.
//
//  2. internal/workflow.LoopStep (internal/workflow/handlers.go) — a step
//     handler in the older, generic YAML-authored pipeline runner (live and
//     API-wired via POST /api/workflows/runs; shell/skill/parallel/loop step
//     kinds). It repeats a step until a gate passes or a max-iteration count
//     is reached: narrow, deterministic-only, no LLM involvement, for an
//     ops/automation-pipeline audience. It is not retired or replaced by
//     this package — see 21-loops.md's "Legacy internal/workflow.LoopStep"
//     section. This package (loop) is an agentic, goal-driven control layer
//     over agentworkflow, a structurally different mechanism for a
//     different audience.
//
// This package needs no doc-comment claim about DAG-only-no-cycles the way
// internal/agentworkflow's own doc.go makes one for that package (see
// internal/agentworkflow/dag.go and types.go for that claim, not this file)
// — a Loop's defining property is exactly the opposite: the plan CAN change
// shape between iterations (REPLAN/REARCHITECT), which is why LoopRun is a
// new peer entity to WorkflowRun rather than a collapse into it
// (21-loops.md, "Decision 1").
package loop
