// Package agentworkflow defines Nanite's Agent Workflows product DTOs,
// definition registry, and trustworthy harness-backed StepExecutor boundary.
//
// Naming note: this package remains deliberately named agentworkflow after
// retirement of the unrelated generic YAML pipeline runtime. The specific
// name keeps its LLM/tool/verify model distinct from the product API
// projection and from the reusable go-workflow engine contracts.
//
// This package holds product types and interfaces only. Graph compilation,
// sequencing, waits, retries, and runtime state belong exclusively to
// go-workflow through internal/workflowhost. The real StepExecutor
// implementation lives in internal/service (see workflow_step_executor.go)
// per the design doc: "StepExecutor's real implementation lives in
// internal/service ... as plain functions — not in the MCP layer, not
// duplicated per consumer."
package agentworkflow
