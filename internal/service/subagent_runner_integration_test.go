package service

import "testing"

// TestChatRunner_EndToEnd_StubProvider is a placeholder for the
// eventual end-to-end test. Spinning up a real chatServiceImpl +
// provider registry is too much wiring to build in isolation — this
// gets verified by Task 9's manual smoke instead.
//
// Skeleton to finalize in a future follow-up:
//  1. Create a real *store.Store on a temp sqlite DB.
//  2. Insert a parent session row + bound agent profile (slug='r1').
//  3. Build provider.Registry with a stub provider registered as the
//     agent's DefaultProvider.
//  4. Construct chatServiceImpl with minimum viable config.
//  5. NewChatRunner(chatSvc, store, store, store, db).
//  6. Call Run() with a SpawnRequest and assert:
//     - returned Result.Summary contains the stub's delta content
//     - subagent_runs row's child_session_id is populated
//     - the new child session row exists
//     - session_agents row exists binding the child to the agent.
func TestChatRunner_EndToEnd_StubProvider(t *testing.T) {
	t.Skip("deferred to Task 9 manual smoke; requires full chatServiceImpl wiring")
}
