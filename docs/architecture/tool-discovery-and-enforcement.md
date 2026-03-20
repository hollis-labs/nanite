# Tool Discovery, Selection, and Enforcement

> Written: 2026-03-20 (Session 26)
> Context: SPR-CONDUIT-TOOL-HARDENING investigation revealed the full tool lifecycle

## Overview

Conduit's tool system has five distinct phases: **Discovery → Selection → Delivery → Execution → Enforcement**. Understanding each phase is critical because they have different enforcement guarantees.

## Phase 1: Discovery

Tools are discovered from MCP servers configured on the agent profile.

```
Agent Profile (DB)
  mcp_servers: ["engine", "cortex", "hadron"]
       │
       ▼
  MCPManager.DiscoverServerTools()
       │
       ├── engine  → 24 tools (engine_tasks_list, engine_task_create, ...)
       ├── cortex  → 15 tools (context_search, context_write, ...)
       └── hadron  → 8 tools  (hadron_run, hadron_blueprints_list, ...)
       │
       ▼
  47 total MCP tools discovered
```

**Enforcement level: HARD GATE.** If an MCP server isn't in the agent's `mcp_servers` list, its tools are never discovered. You can't call tools on servers you're not connected to.

## Phase 2: Selection (Broker)

The LocalBroker scores and ranks tools by relevance to the user's message.

```
User message: "List all tasks in the engine project"
       │
       ▼
  ExtractIntent() → hints: ["list", "tasks", "engine"]
       │
       ▼
  LocalBroker.SelectTools(intent, hints)
  - Rule-based scoring per tool
  - Returns ranked list
       │
       ▼
  Cap at MaxSelectedTools (15)
       │
       ▼
  PruneToTokenBudget()
  - Estimate JSON size of each tool definition
  - Drop lowest-ranked until under budget
       │
       ▼
  Prepend builtins (conduit_start_builder, etc.)
       │
       ▼
  CheckPermission(agentID, toolName) for EACH tool
  - Allow list: tool must match a glob pattern
  - Deny list: tool must NOT match any glob pattern
  - Deny wins over allow
       │
       ▼
  Dedup by name (seen map)
       │
       ▼
  15 tools ready for delivery
```

## Phase 3: Delivery (Progressive Discovery)

When the selected tool count exceeds the ProgressiveDiscoveryThreshold (5 MCP tools), Conduit switches to progressive discovery mode.

### Without Progressive Discovery (≤5 MCP tools)

All tools are sent directly in the API request's `tools` array. The LLM can call any of them immediately.

### With Progressive Discovery (>5 MCP tools)

```
Instead of sending 15+ tool definitions (~15K tokens):

1. Send ONLY:
   - request_tools (meta-tool for on-demand loading)
   - Builtin tools (conduit_start_builder, etc.)

2. Inject into system prompt:
   "## Available Tool Catalog
    engine_tasks_list — List tasks with filters
    engine_task_create — Create a new task
    cortex_search — Search context records
    ... (compact one-line summaries)"

3. LLM reads catalog, calls request_tools(tools=["engine_tasks_list"])

4. HandleRequestTools():
   - Looks up full tool definition from broker
   - Adds to live tool set for this session
   - Returns confirmation text

5. LLM can now call engine_tasks_list normally

Guards:
  - Max 3 request_tools calls per message
  - 2 consecutive empty results → halt
  - Dedup via loadedTools map (won't re-add)
```

**Token savings:** ~90% reduction. 15K tokens of tool definitions → ~1.7K tokens (200 for request_tools + 1,500 for catalog text).

**Trade-off:** One extra LLM turn to load tools. Usually invisible because the LLM loads tools and calls them in the same iteration.

## Phase 4: Execution

```
LLM returns tool_use block:
  { name: "engine_tasks_list", input: { project_id: "conduit" } }
       │
       ▼
  ToolClient.CallTool(agentID, name, input)
       │
       ├── CheckPermission(agentID, name)  ← SECOND CHECK (hard gate)
       │   Denied? → return error with is_error=true
       │
       ├── Route to MCPManager.ExecuteTool()
       │   ├── parsePrefixedToolName() → server="engine", tool="engine_tasks_list"
       │   ├── Find transport for "engine" server
       │   └── transport.CallTool(ctx, name, input)
       │       ├── Stdio: 30s timeout
       │       └── HTTP: 60s timeout
       │
       ├── Extract <!--ENVELOPE_DATA:--> markers
       │
       ├── Repeat detection (same result 3x → block tool)
       │
       └── Truncate result (4K chars / 200 lines for LLM context)
           Full result saved to disk if truncated
```

## Phase 5: Enforcement Summary

| Layer | What it enforces | Guarantee |
|-------|-----------------|-----------|
| MCP server connection | Which servers' tools are reachable | **Hard gate** — can't reach unconnected servers |
| CheckPermission (selection) | Which tools enter the candidate set | **Hard gate** — denied tools not sent to LLM |
| CheckPermission (execution) | Which tools can actually execute | **Hard gate** — denied even if LLM somehow has the definition |
| Anthropic API | Tool must be in the `tools` array | **Hard gate** — API rejects unknown tool calls |
| Progressive discovery | LLM must explicitly request tools | **Soft gate** — speed bump, not security boundary |
| System prompt | Behavioral guidance ("read-only agent") | **Honor system** — no enforcement |
| Repeat detection | Prevents infinite loops | **Hard gate** — tool blocked after 3 identical results |
| Token budget | Prevents context overflow | **Hard gate** — tools dropped if over budget |

### The Bottom Line

**The only real security boundary is CheckPermission at execution time.** Everything else is either a performance optimization (progressive discovery, token budget), a behavioral guide (system prompt), or a convenience filter (broker selection). If you need to prevent an agent from calling a tool, it MUST be in the deny list or excluded from the allow list. System prompt instructions alone are not sufficient.
