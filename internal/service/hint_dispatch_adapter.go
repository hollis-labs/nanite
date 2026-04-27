package service

import (
	"context"
	"fmt"

	"github.com/hollis-labs/nanite/internal/chat"
	"github.com/hollis-labs/nanite/internal/dispatch"
)

// hintDispatchAdapter bridges chat.HintDispatcher to dispatch.Spawner so
// the F5 think-block v2 path can fire in production (CW-20260420-0022
// follow-up; see handoff-phase-6-to-7.md "F5 production wiring").
//
// The hint-selector is a peer-query-shaped call: a single sync spawn of
// the `hint-selector` agent slug with the JSON payload as prompt, return
// the assistant text. The synthetic ParentSessionID is intentional —
// hint selection has no meaningful "calling session" context for the
// peer to thread; it's a system-level dispatch initiated during system-
// prompt assembly. ContextClient signal plumbing (passing the real
// session, scope tier, reflex match) is a separate follow-up
// (CW-20260420-0022, "F5 ContextClient signal plumbing").
type hintDispatchAdapter struct {
	spawner dispatch.Spawner
}

// hintSelectorParentSessionID is the synthetic session id stamped on
// hint-selector spawns. The real subagent.Service requires a non-empty
// parent_session_id, but hint dispatch isn't tied to a user session —
// it fires at system-prompt assembly time. Using a stable synthetic id
// makes the rows easy to filter out of session-scoped queries and
// makes telemetry self-describing.
const hintSelectorParentSessionID = "_hint_selector_system_"

// hintSelectorParentAgentID is the synthetic agent id paired with the
// session id above. Same rationale: hint dispatch is a system call, so
// the parent agent isn't a real user-facing agent.
const hintSelectorParentAgentID = "_system_"

// NewHintDispatchAdapter wraps a dispatch.Spawner so it satisfies
// chat.HintDispatcher. Returns nil when spawner is nil so the
// ContextClient field stays nil and the v2 path falls back to v1
// (matching the existing nil-dispatcher contract).
func NewHintDispatchAdapter(spawner dispatch.Spawner) chat.HintDispatcher {
	if spawner == nil {
		return nil
	}
	return &hintDispatchAdapter{spawner: spawner}
}

// Dispatch implements chat.HintDispatcher. Spawns the hint-selector
// peer in sync mode with the payload as the prompt; returns the
// assistant Summary as the raw response. The hint_dispatch parser
// handles JSON extraction and prose-wrap tolerance.
func (a *hintDispatchAdapter) Dispatch(ctx context.Context, payload string) (string, error) {
	if a == nil || a.spawner == nil {
		return "", fmt.Errorf("hint dispatch: adapter not configured")
	}
	res, err := a.spawner.Spawn(ctx, dispatch.SpawnRequest{
		ParentSessionID: hintSelectorParentSessionID,
		ParentAgentID:   hintSelectorParentAgentID,
		Role:            chat.HintSelectorSlug,
		Prompt:          payload,
		Mode:            "sync",
	})
	if err != nil {
		return "", fmt.Errorf("hint dispatch: spawn: %w", err)
	}
	if res == nil {
		return "", fmt.Errorf("hint dispatch: nil result")
	}
	return res.Summary, nil
}
