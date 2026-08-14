// Package agentworkflow defines the adapter interfaces for the Agent
// Workflows pillar (see docs/architecture/agent-workflows-design.md):
// WorkflowEngine (deterministic sequencing) and StepExecutor (trustworthy,
// harness-backed execution of every unit of work an engine sequences).
//
// Naming note: an unrelated package already owns the name
// "internal/workflow" — a generic YAML-authored pipeline runner (shell /
// skill / parallel / loop steps, wired into internal/api/workflows.go) that
// predates this design and has nothing to do with the Agent Workflows
// pillar. Reusing that package name here would have collided both in
// vocabulary (its Step/StepInput/StepOutput/Executor types have different
// shapes and meanings from this package's LLM/tool/verify step model) and
// in the mental model a reader brings to the word "workflow" in this
// codebase. This package is deliberately named agentworkflow instead.
//
// This package holds types and interfaces only. The real StepExecutor
// implementation lives in internal/service (see workflow_step_executor.go)
// per the design doc: "StepExecutor's real implementation lives in
// internal/service ... as plain functions — not in the MCP layer, not
// duplicated per consumer." No workflow engine (built-in or external) is
// implemented here — that is downstream work this package's interfaces
// exist to unblock.
package agentworkflow
