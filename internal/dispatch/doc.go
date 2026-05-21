// Package dispatch implements the load-bearing seam between the Chat agent
// (harness) and Worker/Planner agents — the executeTask primitive.
//
// # Three-role model (harness spec §1)
//
// Nanite defines three named agent roles, each with a fixed tool surface
// assigned at boot — surfaces do not change mid-turn:
//
//   - Chat (harness): internal Nanite primitives only — todos, plans,
//     scratchpad, peer_query (messaging), narration (envelope renderers).
//     Plus the executeTask dispatch primitive itself. The Chat agent never
//     executes work directly; it dispatches via executeTask.
//   - Worker: task-appropriate surface assigned by ScopeTier. Spawned by
//     Chat to execute concrete work.
//   - Planner: planning-appropriate surface. Spawned by Chat for tasks that
//     warrant breakdown before execution.
//
// # Dispatch flow
//
//   user message
//     → Chat agent (with fixed surface)
//     → executeTask(ctx, args)
//     → classify.Classify → ScopeTier + ExecutionPattern
//     → AssignRole(...) → (Worker | Planner, surface)
//     → spawn role agent (via subagent.Service)
//     → middleware envelope wraps result
//     → Chat receives envelope only (not raw output)
//
// Worker/Planner output never enters Chat's context window directly — it
// is wrapped by the envelope pipeline before Chat sees it. This is the
// architectural fix for context-pollution and card-awareness drift.
//
// # Boundaries
//
// This package owns:
//   - Role enum (Chat / Worker / Planner).
//   - AssignRole mapping: ScopeTier × ExecutionPattern → role + role slug.
//   - ExecuteTask primitive and its dependency surface (Spawner interface).
//
// This package does NOT own:
//   - ScopeTier classification logic (internal/classify).
//   - Envelope construction or rendering (internal/chat).
//   - Worker/Planner role definitions (internal/agent/builtin/profiles).
//   - Subagent spawn machinery (internal/subagent).
//
// # References
//
//   - Spec: docs/superpowers/specs/2026-04-21-agent-platform-harness-design.md §1
//   - Phase plan: planning/agent-platform/architecture-sequence/phase-3-spine.md
//   - Ticket: CW-20260421-0010
package dispatch
