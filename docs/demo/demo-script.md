# Nanite Demo Script

Repeatable walkthrough of key Nanite features. Target: under 15 minutes.

## Prerequisites

1. **Environment**:
   - `ANTHROPIC_API_KEY` set in `.env` or exported
   - Volon running (`volon serve` or via Cerberus)
   - Cortex running (`contextd serve` or via Cerberus)
   - Hadron running (optional, for blueprint demos)

2. **Build & start**:
   ```bash
   cd ~/Projects-apps/nanite
   cd ui && npm install && npm run build && cd ..
   go build ./cmd/nanite/
   ./nanite serve -port 8090 -dev  # -dev skips embedded SPA, uses Vite proxy
   ```
   Or with Docker:
   ```bash
   docker compose up --build
   ```

3. **Verify**: Open `http://localhost:8090` in browser (or `http://localhost:5173` in dev mode with Vite proxy).

---

## Demo Flow

### 1. Launch & Chat (2 min)

**Goal**: Show Nanite starts, connects to providers, and handles basic chat.

1. Open the Nanite UI in browser
2. Create a new session (click "New Chat")
3. Send a simple message: "What is Fragments Engine?"
4. Watch the streaming response appear
5. Point out: agent attribution, model indicator, token usage

**Talking points**:
- Multi-provider support (Anthropic, OpenAI, Ollama)
- Session persistence (SQLite)
- Auto-titling and auto-tagging

**Fallback**: If no API key, Nanite shows a clear error banner. Switch to Ollama (local, no key needed).

### 2. Agent Modes (2 min)

**Goal**: Show mode switching changes agent behavior.

1. In the current session, open the mode selector
2. Switch from "default" to "plan" mode
3. Send: "How should we restructure the Cortex namespace system?"
4. Observe: response style changes (structured plan vs. conversational)
5. Switch back to "default" mode

**Talking points**:
- Modes are reusable across agents
- Each mode has its own system prompt overlay
- Mode switch recomposes the system prompt mid-session

**Fallback**: If mode switch fails, explain the concept using the API: `POST /api/sessions/{id}/mode`

### 3. Tool Use via MCP (3 min)

**Goal**: Show Nanite calling external tools through MCP.

1. Send: "List the active sprints in the mentat project"
2. Watch the tool_call event appear (volon_sprints_list)
3. Result appears inline with tool attribution
4. Send: "What tasks are in the current sprint?"
5. Watch volon_tasks_list called automatically

**Talking points**:
- MCP integration: Volon, Hadron, Cortex all connected
- Progressive discovery: LLM requests tools by intent, not by name
- Tool repeat detection prevents infinite loops (just fixed!)
- ToolBroker enforces agent-level permissions

**Fallback**: If MCP servers aren't running, show the tool list via `GET /api/tools` and explain the architecture.

### 4. Sprint Planning Modal (2 min)

**Goal**: Show Volon integration for task management.

1. Send: "Create a new task: Research vector search options for Cortex"
2. Watch the envelope proposal appear in the chat
3. Show the Volon proxy endpoints in action
4. Navigate to the tasks view to see it reflected

**Talking points**:
- Envelope system: structured proposals extracted from LLM responses
- Volon proxy: Nanite proxies task CRUD to Volon's MCP
- Bi-directional: create tasks from chat, view tasks in UI

**Fallback**: Use the API directly: `POST /api/volon/backlog`

### 5. Delegation (3 min)

**Goal**: Show Mentat agent spawning a worker session.

1. Via API (curl or Postman):
   ```bash
   curl -X POST http://localhost:8090/api/sessions/{SESSION_ID}/delegate \
     -H 'Content-Type: application/json' \
     -d '{
       "title": "Research Go embedding options",
       "description": "Survey the current Go libraries for text embeddings. Compare chromem-go, qdrant-go, and pgvector. List pros/cons of each."
     }'
   ```
2. Show the response: worker session ID, content, token usage
3. Show the worker session was created and archived after completion
4. Or use delegate-aggregate for multi-task decomposition:
   ```bash
   curl -X POST http://localhost:8090/api/sessions/{SESSION_ID}/delegate-aggregate \
     -H 'Content-Type: application/json' \
     -d '{
       "message": "Compare the architecture of three chat applications: Nanite, ChatGPT, and Claude.ai. For each, analyze the backend, frontend, and plugin system."
     }'
   ```
5. Show the decomposed sub-tasks and aggregated result

**Talking points**:
- Delegation = spawn worker session, execute, return result, archive
- Decomposer uses LLM to break complex tasks into sub-tasks
- Each worker gets its own session with full chat engine capabilities
- Aggregator combines results via LLM for coherent output
- Worker sessions are archived after completion (clean state)

**Fallback**: If delegation times out, show the architecture diagram and explain the flow. The pieces are all functional — it's an API-level feature that will get UI integration in a future sprint.

### 6. Context Continuity via Cortex (2 min)

**Goal**: Show Mentat remembers across sessions.

1. Send: "What decisions have we made about the agent taxonomy?"
2. If Cortex has relevant records, they inform the response
3. Show Cortex records via API: `GET /api/tools` (search for cortex tools)
4. Explain the namespace system: `app/mentat`, `global/system`

**Talking points**:
- Cortex = persistent context/memory registry
- Records organized by namespace (project-scoped, global)
- `/reorient` skill pulls from Cortex + Volon + bootstrap for full context
- Session summaries, decisions, project context all stored

**Fallback**: If Cortex is sparse, explain the seeding process and show the 12 representative records that were created during preflight.

### 7. Wrap-up (1 min)

**Summary points**:
- Nanite is the chat harness for Fragments Engine
- Provider-agnostic (Anthropic, OpenAI, Ollama)
- MCP-native: tools from any MCP server
- Agent/mode system for different interaction styles
- Delegation for complex multi-step tasks
- Context continuity via Cortex
- Full API for integration (`/api/*`)

---

## Quick Reference

| Feature | Endpoint | Method |
|---------|----------|--------|
| Send message | `/api/messages` | POST |
| Stream response | `/api/stream/{messageID}` | GET (SSE) |
| Switch mode | `/api/sessions/{id}/mode` | POST |
| List tools | `/api/tools` | GET |
| Delegate task | `/api/sessions/{id}/delegate` | POST |
| Decompose+delegate | `/api/sessions/{id}/delegate-aggregate` | POST |
| Volon tasks | `/api/volon/tasks` | GET |
| Create backlog | `/api/volon/backlog` | POST |
| List agents | `/api/agents` | GET |
| Session usage | `/api/sessions/{id}/usage` | GET |

## Troubleshooting

| Problem | Fix |
|---------|-----|
| "Anthropic provider not available" | Set `ANTHROPIC_API_KEY` in `.env` |
| Tool calls fail | Check MCP servers are running (`/health-check`) |
| No Volon data | Run `volon serve` with `VOLON_POSTGRES_DSN` set |
| UI not loading | In dev mode, run `cd ui && npm run dev` separately |
| Delegation timeout | Check worker session logs, increase timeout if needed |
