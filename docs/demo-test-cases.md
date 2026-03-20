# Demo Presenter — Test Cases

**Agent:** Demo Presenter
**Setup:** Open Conduit (`localhost:5176`) and Engine GUI (`localhost:1420`) side-by-side. Select "Demo Presenter" from the agent dropdown. Start a new session for each test group.

---

## 1. Cross-App Navigation

### 1a. Basic navigation
**Prompt:** "Show me the tasks page"
**Expected:**
- Engine GUI navigates to the Tasks page
- Agent confirms with a brief message
- Conduit Link indicator in Engine NavRail is green

### 1b. Navigation with filters (params sent, Engine reads from URL)
**Prompt:** "Show me the P1 tasks"
**Expected:**
- Engine GUI navigates to Tasks page with `priority=1` in URL hash
- Note: Project filter is controlled by Engine's project selector (top of page),
  not overridden by URL params. For demo, switch project manually if needed.

### 1c. Different pages
**Prompt:** "Show me the sprints"
**Expected:**
- Engine GUI navigates to Sprints page

### 1e. Page variety
**Prompt:** "Take me to the kanban board"
**Expected:**
- Engine GUI navigates to Kanban Board view

### 1f. Detail navigation
**Prompt:** "Show me sprint SPR-DEMO-001"
**Expected:**
- Engine GUI navigates to Sprint Detail with `id=SPR-DEMO-001`

---

## 2. Sprint Planning Review

### 2a. Full planning flow
**Prompt:** "I'm doing a demo. Let's demonstrate how we can use Conduit to plan and manage tasks. Please create 2-3 demo sprints and 4-8 tasks per sprint. Then let's review the tasks — you suggest which sprints they go in."
**Expected:**
- Agent creates demo data (may narrate or call Engine MCP tools)
- Interactive SprintPlanningReviewCard appears in chat
- Card shows: task rows with expand arrows, priority dots, sprint dropdowns, Add/Move buttons
- Sprint legend at top right shows the sprint names
- Counter shows "0/N confirmed"

### 2b. Interact with the card
**Actions:**
1. Click expand arrow on a task — summary text appears
2. Click "Add" on a task — checkmark appears, counter increments
3. Change dropdown on a task to a different sprint — button changes to amber "Move"
4. Click "Move" — checkmark appears with the new sprint name
5. Click "Accept Page" — all unconfirmed tasks on the page get checkmarks
6. Click "Finish (N)" — single batch message sent to agent

**Expected:**
- All local state updates are instant (no chat messages until Finish)
- After Finish, agent responds confirming the assignments
- Green completion banner: "N tasks assigned to sprints"

---

## 3. Task Disposition / Triage

### 3a. Basic triage
**Prompt:** "Let me triage some tasks"
**Expected:**
- TaskDispositionCard appears with task rows
- Each task shows: ID badge, priority badge, status badge, title, action dropdown
- Default actions: Approve, Done, Request Changes, Defer, Skip
- Dropdown shows action hints (e.g. "Approve (→ queued)")

### 3b. Request Changes with comment
**Actions:**
1. Select "Request Changes" on a task
2. Comment input appears with amber border
3. Type a comment: "needs more detail on the auth approach"
4. Click "Submit All"

**Expected:**
- Message sent: `DISPOSITION: TASK-123=RequestChanges[needs more detail on the auth approach]`
- Agent processes: adds comment + transitions to todo

### 3c. Mixed dispositions
**Actions:** Set different actions on different tasks (Approve one, Done another, Defer a third)
**Expected:** Submit sends all in one message, agent processes each

---

## 4. Giphy

### 4a. Basic GIF search
**Prompt:** "Show me a celebration GIF"
**Expected:**
- GiphyModalCard appears with:
  - Title: "Here's your celebration!"
  - Animated GIF (celebration-themed)
  - "Powered by GIPHY" attribution
  - Fade-in + scale animation

### 4b. Different queries give different GIFs
**Prompt:** "Show me a cat GIF"
**Expected:** Different GIF than the celebration one

### 4c. Keywords: hamster, running, coffee, rocket, dance, coding, dog, fire, thumbs up, mind blown, happy, success
**Expected:** Each returns a different themed GIF

---

## 5. Reports

### 5a. Run a report
**Prompt:** "Run the executive summary report"
**Expected:**
- Agent calls `conduit_run_report`
- TaskCompleteNotificationCard appears:
  - Green checkmark icon
  - "Report Complete: executive-summary"
  - Completion timestamp
  - Output preview (truncated)
  - "Show Report" button (green)
  - "Dismiss" button (X icon)

### 5b. Show the report
**Action:** Click "Show Report" button
**Expected:**
- Sends `SHOW_REPORT:RUN-xxxxx` message
- Agent responds with DocumentViewerCard or report content

### 5c. Dismiss notification
**Action:** Click X on notification card
**Expected:** Card disappears (local state)

---

## 6. Report Card (Metrics)

### 6a. Status report
**Prompt:** "Give me a status report on the portfolio"
**Expected:**
- ReportCard appears with:
  - Title + timestamp
  - Metrics grid (2-column) with labels, values, progress bars
  - Color-coded metrics (emerald for good, red for bad)
  - Summary text (markdown)
  - Optional action buttons

---

## 7. Document Viewer

### 7a. Show a document
**Prompt:** "Show me the executive summary document"
**Expected:**
- DocumentViewerCard appears:
  - Title bar with file icon
  - Open in new tab button
  - Scrollable content area (max 500px height)
  - Markdown or HTML rendered correctly

---

## 8. ThinkingIndicator

### 8a. Initial thinking
**Action:** Send any message
**Expected:**
- Spinning gear + rotating fun message appears immediately
- Messages rotate every 3 seconds: "Grinding my gears…", "Consulting the oracle…", etc.
- Disappears when agent starts streaming text

### 8b. Mid-stream stall
**Action:** Send a complex request that requires tool calls
**Expected:**
- Agent starts responding with text
- When stream pauses for 2+ seconds (tool processing), gear reappears below the text
- Disappears when more content streams in

---

## 9. Combined Demo Flow (Recommended for presentation)

This is the suggested order for the Monday demo:

1. **Open both screens** — Conduit + Engine side-by-side
2. **"Show me the tasks page"** → Engine navigates (wow moment: chat controls the dashboard)
3. **"Show me P1 tasks for the engine project"** → Filters apply live
4. **"Let's plan some sprints"** → Sprint planning card appears, interact with Add/Move/Finish
5. **"Let me triage these tasks"** → Disposition card, show Request Changes with comment
6. **"Run the executive summary report"** → Notification card, click Show Report
7. **"Give me a status report"** → Metrics card with progress bars
8. **"Show me a celebration GIF"** → Fun ending, audience engagement
9. **"Take me to the kanban board"** → Final navigation to show the board view

---

## Environment Requirements

- **Conduit API:** `localhost:8090` (Cerberus managed)
- **Conduit Frontend:** `localhost:5176` (Vite dev server)
- **Engine API:** `localhost:8085` (GUI server with SSE)
- **Engine Frontend:** `localhost:1420` (Vite dev server)
- **GIPHY_API_KEY:** Set in `conduit/.env` for live GIF search (optional — demo mode works without)
- **Demo Presenter agent:** Must be seeded in DB (check Settings → Agents)
- **Conduit Link:** Green indicator in Engine NavRail confirms SSE connection
