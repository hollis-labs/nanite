---
from: toolbroker-agent
to: lead
type: info
priority: normal
timestamp: 2026-03-14T16:10:00Z
subject: TASK-165 Universal ToolBroker — Complete
---

## Summary

TASK-20260314-165 is complete. ToolBroker is now the universal tool selection layer with two consumers (Mentat, Volon) and a documented integration pattern for future consumers.

## What Was Done

### 1. Audit (all tool call paths)
- **Mentat**: Already uses ToolBroker via ToolClient ✅
- **Volon**: CompletionService had NO tool support — now wired ✅
- **Cortex, Hadron, Cerberus**: Tool providers only (MCP adapters) — not consumers, no changes needed
- **CLI tools**: None consume tools directly

### 2. Universal Interface (tiamat-tool-broker/)
Added two new files to the core library:
- `broker/budget.go` — `EstimateToolTokens()`, `PruneToTokenBudget()` (extracted from Mentat, now universal)
- `broker/discovery.go` — `ScoreByKeywords()`, `ScoreByIntent()`, `FindByNames()`, `TokenizeIntent()` (progressive discovery primitives)
- Full test coverage: `budget_test.go` (5 tests), `discovery_test.go` (9 tests)
- Updated `docs/integration-guide.md` with sections on budget management, progressive discovery, and minimal consumer setup

### 3. Volon ToolClient (new package)
- Created `volon/internal/toolclient/client.go` — wraps `broker.LocalBroker`
- Implements: `SelectTools()`, `FindByNames()`, `FindByIntent()`, `AllSummaries()`
- Returns `SelectionResult` with progressive discovery flag and catalog
- Test coverage: 8 tests, all passing
- Added `tool-broker` dependency to Volon's go.mod

### 4. Volon Provider + CompletionService Integration
- Added `ToolDefinition` type to `providers.CompletionRequest`
- Updated Anthropic client to pass tools to the API
- Wired `ToolClient` into `CompletionService` — auto-selects tools per turn using intent detection
- CompletionService.selectTools() extracts intent from last user message via `broker.DetectIntent()`

### 5. Integration Pattern Documented
Updated `tiamat-tool-broker/docs/integration-guide.md` with:
- Token budget management section
- Progressive discovery pattern
- request_tools meta-tool construction guide
- Minimal 3-step consumer setup pattern

## Test Results
- tiamat-tool-broker: all tests pass (broker, budget, discovery, intent, config)
- Volon toolclient: 8/8 pass
- Volon unit tests: all pass (E2E scheduler failures are pre-existing autopick bug)
- Mentat toolclient: all pass (unchanged, compatible with broker additions)
- Mentat build: clean

## Follow-ups Identified
- Full tool execution loop in Volon (parsing tool_use blocks, calling tools, returning results) — separate task
- OpenAI provider adapter needs same tool passthrough (currently only Anthropic done)
- Mentat's ToolClient could delegate budget/discovery to the new broker functions (reduce duplication)

## Files Changed
- `tiamat-tool-broker/broker/budget.go` (new)
- `tiamat-tool-broker/broker/budget_test.go` (new)
- `tiamat-tool-broker/broker/discovery.go` (new)
- `tiamat-tool-broker/broker/discovery_test.go` (new)
- `tiamat-tool-broker/docs/integration-guide.md` (updated)
- `volon/internal/toolclient/client.go` (new)
- `volon/internal/toolclient/client_test.go` (new)
- `volon/internal/providers/providers.go` (added ToolDefinition, Tools field)
- `volon/internal/providers/anthropic/client.go` (tools passthrough)
- `volon/internal/runtime/chat/service.go` (ToolClient wiring)
- `volon/go.mod` (added tool-broker dependency)
