# agent-status

Request a status update from another agent, or report your own status.

## Usage
`/agent-status <session_id>` — Request status from another agent
`/agent-status` — Report your own status to the lead

## Instructions

### Requesting status from another agent:

1. Create the agent's inbox directory if needed: `mkdir -p .agentrc/inbox/<session_id>/`
2. Count existing files to get sequence number
3. Write a status request message:
   ```markdown
   ---
   from: <your_session_id>
   to: <session_id>
   type: context-request
   priority: medium
   timestamp: <current ISO>
   subject: Status check — what are you working on?
   ---

   Status request from <your role>.

   1. What task are you currently executing?
   2. What's your plan for the next 2-3 tasks?
   3. Any blockers or decisions needed?
   4. Estimated completion for current task?

   Reply to .agentrc/inbox/<your_session_id or lead>/
   ```

### Reporting your own status:

1. Gather:
   - Current task (from Volon — status "doing")
   - Recently completed tasks (last 3-5 done)
   - Next planned tasks
   - Any blockers
2. Write to lead inbox:
   ```markdown
   ---
   from: <your_session_id>
   to: lead
   type: info
   priority: medium
   timestamp: <current ISO>
   subject: Status report — <current task summary>
   ---

   ## Current
   <TASK-ID>: <title> — <progress notes>

   ## Recently Completed
   - <TASK-ID>: <title>
   - <TASK-ID>: <title>

   ## Next
   1. <TASK-ID>: <title>
   2. <TASK-ID>: <title>

   ## Blockers
   <none, or describe>
   ```
