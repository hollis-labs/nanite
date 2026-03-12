# Tool Broker Architecture

> A shared system for selecting the right MCP tools for the task at hand, replacing the current "dump everything into context" approach.

## Problem

Tiamat currently exposes **124+ MCP tools** across three services (Volon, Hadron, Cortex), and the number grows with every Hadron blueprint. The current approach to tool availability has several compounding costs:

1. **Context bloat.** Every tool definition (~150 tokens per tool) is serialized into the LLM's system prompt. 124 tools consume ~18,600 tokens before the conversation starts.

2. **Decision fatigue.** LLMs make worse tool-selection decisions when presented with too many options.

3. **Bandaid filtering.** Mentat currently applies a hardcoded `defaultExcludePatterns` list (`"hadron_bp_"`) to hide blueprint runner tools. This is brittle and lives in application code.

4. **No intent awareness.** The system has no way to say "for this task, you need these 8 tools" — it is all or nothing.

## Vision

A **Tool Broker** that selects relevant MCP tools based on intent, task context, and configurable rules. Three layers, built incrementally:

| Layer | Where it runs | What it does |
|-------|--------------|--------------|
| **Shared Go module** (`tiamat-tool-broker`) | Imported by any Go app | Core interfaces, rule engine, matching logic |
| **Local broker** (embedded) | Inside Mentat, CLI tools, agents | Fast, offline, config-driven tool filtering |
| **Broker service** (optional) | Standalone HTTP/MCP server | Cross-app tool discovery, user-managed rules, permissions |

The key insight from Cortex's context broker: **intent detection + budget constraints + progressive disclosure** is the right pattern.

## Architecture

### Core Interface

```go
package broker

import "context"

type ToolDefinition struct {
    Name        string         `json:"name"`
    Description string         `json:"description"`
    Server      string         `json:"server"`
    InputSchema map[string]any `json:"input_schema"`
    Tags        []string       `json:"tags"`
    CostTier    string         `json:"cost_tier"`
}

type ToolSummary struct {
    Name        string   `json:"name"`
    Description string   `json:"description"`
    Server      string   `json:"server"`
    Tags        []string `json:"tags"`
    CostTier    string   `json:"cost_tier"`
}

type SelectResult struct {
    Tools     []ToolDefinition `json:"tools"`
    Excluded  int              `json:"excluded"`
    Rationale string           `json:"rationale"`
    Hints     []string         `json:"hints"`
}

type Broker interface {
    SelectTools(ctx context.Context, intent string, hints []string) (*SelectResult, error)
    ListSummaries(ctx context.Context, filters ...Filter) ([]ToolSummary, error)
    GetToolSchema(ctx context.Context, name string) (*ToolDefinition, error)
}

type Filter struct {
    Servers  []string
    Tags     []string
    CostTier string
}
```

### Intent-to-Tool Mappings

| Intent | Tools selected | Rationale |
|--------|---------------|-----------|
| `explore_project` | `volon_tasks_list`, `volon_sprints_list`, `cortex_context_view` | Understanding project state |
| `run_build` | `hadron_blueprints_list`, `hadron_run_enqueue`, `hadron_run_get` | Build workflows |
| `search_memory` | `cortex_context_view`, `cortex_context_history`, `cortex_context_broker_fetch` | Memory search |
| `manage_tasks` | `volon_task_create`, `volon_task_update`, `volon_task_transition` | Task CRUD |
| `full_access` | All tools (no filtering) | Escape hatch for power users |

### Progressive Disclosure

Instead of injecting full JSON Schema for every tool (~150 tokens each), the broker supports:

1. **Summaries first.** ~30 tokens per tool instead of ~150.
2. **Schema on demand.** Full input schema fetched when the LLM decides to use a tool.

For 8 out of 50 tools used: ~7,500 tokens → ~2,700 tokens (64% reduction).

## Phases

### Phase 1: Shared Module + Local Broker
Replace hardcoded `defaultExcludePatterns` with configurable, intent-aware broker. Integrate into Mentat's MCP `Manager`.

### Phase 2: Rule Configuration + Intent Detection
YAML rule format, intent detection heuristics, progressive disclosure API.

### Phase 3: Broker Service
Centralized tool registry with HTTP/MCP endpoints, persistence, cross-app discovery.

### Phase 4: GUI for Rule Management
Web UI for tool inventory, rule editor, intent mappings.

## Relationship to ADRs

| ADR | Relationship |
|-----|-------------|
| **ADR-006 (MCP Response Budget)** | Complementary — ADR-006 constrains response size; broker constrains which tools are available. |
| **ADR-003 (agentrc rename)** | Broker config files follow `.agentrc/` convention if stored per-project. |

## Open Questions

1. **Where does the module live?** Standalone `tiamat-tool-broker` repo vs package inside `tiamat-otel`.
2. **Intent detection at the LLM boundary?** Caller passes intent vs broker infers from first user message vs two-phase with summaries.
3. **Should the broker service be part of Cortex?** Avoids a new service but risks scope creep.
4. **Dynamic tool sets.** Handling server availability gracefully — selecting tools from a down server should warn, not error.
