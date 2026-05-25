# Boot Prompt: Unified Session Activity Pipeline

Repo: `/Users/chrispian/dev/hollis-labs/apps/nanite`

## Goal

Implement a unified activity/presence pipeline so users can clearly see when agents, CLI-backed sessions, and subagents are actively doing work.

This should not be a one-off UI patch. Build one shared backend activity contract and feed every execution path through it. The frontend should consume that contract for:

- Left chat rail working spinner/presence.
- Parent/root session activity when subagents run tools.
- Chat header live tool-call counter beside the existing available-tools count.
- A single replace-in-place activity line in the streaming assistant block, e.g. `Using dev_read to read internal/service/chat_generate.go`.

## Product Requirements

1. The left chat rail presence indicator should show an active spinner/state when the session is doing work, especially while a tool call is running.
2. Subagent tool calls/activity should bubble up and count as activity on the parent/root chat session.
3. The chat header currently shows available tools, e.g. `349 tools`. Beside that, add a live `Tool calls: N` counter that updates during the turn.
4. In the chat transcript, where the working/thinking strip appears, show one live replace-in-place activity row with a spinner and text. It should not append noisy history. It should update as activity changes.
5. CLI-backed sessions must propagate activity to the Nanite GUI. We should rely on typed streaming/json/stdio events from the runtime where available.
6. Presence state must be session-scoped, not active-chat-scoped. Switching chats must not leak activity UI between sessions.

## Current State / Audit

Frontend already has most primitives:

- Global presence SSE hook: `ui/src/hooks/usePresence.ts`
- Presence event type: `ui/src/lib/types.ts` near `PresenceEvent`
- Session-scoped chat store: `ui/src/stores/useChatStore.ts`
- Session slice shape: `ui/src/stores/chatSessionState.ts`
- Left rail derives activity state here: `ui/src/lib/sidebar-session.ts`
- Left rail dot rendering here: `ui/src/components/sidebar/LeftSidebar.tsx`
- Chat stream handles `tool_call` / `tool_result`: `ui/src/hooks/useChat.ts`
- Chat transcript working strip: `ui/src/components/chat/ChatTranscript.tsx`
- Header available-tools count: `ui/src/components/chat/ChatHeader.tsx`

Backend primitives:

- Stream event shape: `internal/chat/engine.go` (`StreamEvent`)
- Presence event shape: `internal/chat/engine.go` (`PresenceEvent`)
- Presence SSE endpoint: `internal/api/presence.go`
- Stream manager presence state/fanout: `internal/service/stream.go`
- Main API-backed tool execution emits presence: `internal/service/chat_tool_executor.go`
- Chat generation emits stream start/end presence: `internal/service/chat_generate.go`
- CLI runtime bridge maps typed events to chat stream events: `internal/service/agent_deps.go`
- Runtime dependency documents typed callback purpose: `internal/runtime/agent/deps.go`
- Subagent runner drains child stream privately: `internal/service/subagent_runner.go`
- Subagent status sink only emits run status to parent today: `internal/service/subagent_sink.go`

Important gaps found:

- Some local/meta tool paths emit `tool_call` / `tool_result` but do not emit presence, e.g. scratchpad/result-cache paths.
- CLI typed events already generate `tool_call` / `tool_result` stream events, but do not broadcast presence/activity.
- Subagent child streams are consumed privately by `ChatRunner`; parent only gets status transitions, not child tool activity.
- Presence replay currently snapshots active streams only, not active tool/activity state.
- The frontend working strip appends narration/thinking, but there is no replaceable current-activity line.

## Desired Architecture

Create a single backend activity service/contract. All writers should call that one path.

Suggested event model:

```go
type SessionActivityEvent struct {
    Type            string `json:"type"` // activity_start, activity_update, activity_end
    SessionID       string `json:"session_id"`
    RootSessionID   string `json:"root_session_id,omitempty"`
    ParentSessionID string `json:"parent_session_id,omitempty"`
    SourceSessionID string `json:"source_session_id,omitempty"`
    AgentID         string `json:"agent_id,omitempty"`
    SubagentRunID   string `json:"subagent_run_id,omitempty"`
    ActivityID      string `json:"activity_id"`
    Kind            string `json:"kind"` // tool_call, subagent, cli, stream
    ToolName        string `json:"tool_name,omitempty"`
    Detail          string `json:"detail,omitempty"`
    Label           string `json:"label,omitempty"`
    Status          string `json:"status"` // running, done, error
    Timestamp       string `json:"timestamp"`
}
```

Names can be adjusted to fit existing code style. The key is that activity must carry both the source session and the session/root session that should display the aggregate activity.

Suggested backend API:

```go
BeginActivity(ctx, Activity)
UpdateActivity(ctx, Activity)
EndActivity(ctx, Activity)
```

Or a small `SessionActivityTracker` in `internal/service` that owns:

- Presence broadcast.
- Current activity snapshot for `/api/presence` replay.
- Root/parent bubbling.
- Optional stream broadcast into active parent/root message streams.
- Tool-call aggregate count.

The service should emit both:

- Presence-level events for cross-session state and left rail.
- Chat stream events for active transcript/header state.

Do not let each caller independently decide UI behavior.

## Event Behavior

When a tool starts:

- Emit `activity_start`.
- Broadcast presence for the session.
- If source session has `parent_session_id` or `root_session_id`, also emit aggregate activity to those sessions.
- Broadcast/update chat stream for active parent/root stream if present.
- Increment live tool call count for the visible aggregate session.

When detail changes:

- Emit `activity_update`.
- Replace current activity label on the frontend.

When tool completes:

- Emit `activity_end`.
- Mark tool done/error.
- Clear current activity for that activity id unless another activity is still running.

For parallel tools:

- Keep a map of active activities.
- Current activity line can show the most recent running tool.
- Header counter should count total started calls for the current turn, not just currently running.
- Left rail remains working while any activity is running.

For reconnect:

- `/api/presence` should replay current active activities, not only active stream starts.

## Backend Implementation Pointers

Start by extending `internal/chat/engine.go`:

- Add activity event fields either to `PresenceEvent` and `StreamEvent`, or add a dedicated struct that can be carried in `Data`.
- Prefer explicit fields if feasible.

Extend `internal/service/stream.go`:

- Add active activity map, probably keyed by display session/root session and activity id.
- Add methods like:
  - `SetActiveActivity(sessionID string, event chat.PresenceEvent)`
  - `ClearActiveActivity(sessionID, activityID string)`
  - `ActivePresenceState()` should include active activity snapshots.
  - Or add `ActiveActivityState()` and have `internal/api/presence.go` replay both.

Main API-backed tool path:

- `internal/service/chat_tool_executor.go`
- Around `executeSingleTool`, replace direct `tool_pending` / `tool_resolved` presence with the new activity helper.
- Keep existing `tool_call` / `tool_result` stream events for compatibility.
- Ensure blocked/denied/validation paths that emit `tool_call` / `tool_result` also use activity if they represent actual attempted work.

Local/meta tools:

- `internal/service/chat_scratchpad.go`
- result-cache handling in `internal/service/chat_tool_executor.go`
- These should emit the same activity start/end if they surface as tool calls.

CLI-backed sessions:

- `internal/service/agent_deps.go`
- In `agentEventBridge.typedCallback`, `events.ToolUse` should call the activity helper in addition to broadcasting `tool_call`.
- `events.ToolResult` should end the matching activity.
- `events.Thinking` can optionally update activity label, but do not spam.
- This is the key path for streaming/json/stdio CLI launches.

Subagents:

- `internal/service/subagent_runner.go`
- `drainCapture` currently consumes child events privately. Add a bridge path that forwards child tool activity to the parent/root session while still preserving capture behavior.
- Use `run.ParentSessionID`, `run.ChildSessionID`, and `run.ID` for lineage metadata.
- For child `tool_call`, emit/display activity on both child session and parent/root aggregate session.
- For child `tool_result`, resolve it on both.
- Avoid forwarding child final text into parent answer unless existing behavior already does that; this task is activity visibility, not transcript merging.

Subagent status:

- `internal/service/subagent_sink.go` can remain for status transitions, but status alone is insufficient. The new activity path should complement it.

## Frontend Implementation Pointers

Types:

- `ui/src/lib/types.ts`
- Extend `PresenceEvent`.
- Add `SessionActivity` or similar.
- Extend `ToolCall` only if useful for `source_session_id`, `subagent_run_id`, etc.

Store:

- `ui/src/stores/chatSessionState.ts`
- Add:
  - `currentActivity?: SessionActivity | null`
  - `activeActivities?: Record<string, SessionActivity>` or keep map in root store.
  - `toolCallCountThisTurn: number` or equivalent.
- `ui/src/stores/useChatStore.ts`
- Add actions:
  - `beginActivity(sessionID, activity)`
  - `updateActivity(sessionID, activity)`
  - `endActivity(sessionID, activityID, status)`
  - `resetTurnActivity(sessionID)` when a new user message starts.
- Keep all fields session-scoped.

Presence hook:

- `ui/src/hooks/usePresence.ts`
- Dispatch `activity_start`, `activity_update`, `activity_end`.
- Existing `tool_pending` / `tool_resolved` can remain as backwards-compatible aliases or be mapped into activity events until backend is fully migrated.

Chat SSE:

- `ui/src/hooks/useChat.ts`
- On `tool_call`, increment/add current activity if the backend does not emit separate stream activity events yet.
- On `tool_result`, complete activity.
- Prefer backend activity events long-term so subagent/CLI activity comes through one shape.

Left rail:

- `ui/src/lib/sidebar-session.ts`
- Treat active activities as `working`.
- Pending approval/user action can remain `pending_action`.
- `ui/src/components/sidebar/LeftSidebar.tsx`
- Replace the pulsing dot with a spinner for `working` if desired. `Loader2` from `lucide-react` is already used elsewhere.

Chat header:

- `ui/src/components/chat/ChatHeader.tsx`
- Beside `${toolCount} tools`, show live `Tool calls: N` when N > 0 or while streaming/activity exists.
- Count must include bubbled subagent calls.

Transcript:

- `ui/src/components/chat/ChatTranscript.tsx`
- In the streaming assistant block, add a single current activity row with spinner.
- This should be replace-in-place, not appended.
- Example text:
  - `Using dev_read to read internal/service/chat_generate.go`
  - `Subagent reviewer using dev_grep`
  - `Claude Code (cli) using Bash`

## Label Formatting

Backend should send structured fields. Frontend can format:

- `tool_name` + `detail`: `Using ${tool_name} ${detail}`
- If `source_session_id !== session_id` or `subagent_run_id` is set: prefix with subagent/agent label if available.
- Keep labels short and clamp to one line.

## Tests To Add

Backend focused tests:

- API-backed tool emits activity start/end presence.
- Local/meta tool emits activity start/end.
- CLI typed `ToolUse` / `ToolResult` emits activity and stream events.
- Subagent child `tool_call` bubbles to parent/root session activity.
- Presence replay includes active activity after client connects mid-tool.

Frontend tests:

- `dispatchPresenceEvent` handles activity start/update/end and updates store.
- Session A activity does not appear in Session B after switching chats.
- Header live tool-call counter updates from tool/activity events.
- Transcript current activity line replaces previous text instead of appending.
- Left rail shows working/spinner for active activity.

Existing tests to inspect/extend:

- `ui/src/__tests__/presence-session-mode-changed.test.ts`
- `ui/src/stores/__tests__/useChatStore.test.ts`
- Existing session-switch tests around stream/tool isolation.

## Acceptance Checks

- Start an API-backed chat that uses tools: left rail spinner appears, header counter increments, transcript shows current activity line, and it clears on completion.
- Start a CLI-backed harness/boot-profile chat: typed tool events appear in the same UI surfaces.
- Spawn/use subagents: parent chat row/header/transcript shows child tool activity and counts it.
- Switch from active Chat A to Chat B while A is working: B stays clean; A still shows activity in left rail.
- Open/reload another browser tab mid-tool: presence replay restores working/activity state.
- No duplicate tool calls in the drawer/header from double-emitting CLI typed events plus runtime stream events.

## Constraints

- Do not revert unrelated existing changes.
- Use `apply_patch` for manual edits.
- Prefer focused tests before broad test runs.
- Build frontend with `npm run build`.
- Build Nanite with `make build`. The Makefile should copy `ui/dist` into `internal/server/ui_dist` before `go build`.
- If deploying, use:

```bash
cerberus resource apply nanite-api-service
cerberus resource status nanite-api-service
```

