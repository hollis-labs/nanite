// This file implements TASKS/teams/11-team-run-launch-api.md: the "launch a
// saved Team by name, with invocation-time overrides" surface
// 15-teams.md's "Runtime overrides follow the existing cascade" section
// requires, calling task 08's already-merged LaunchTeamRun
// (internal/service/team_run_launcher.go).
//
// --- Surface choice: REST, not a self-tool or an A2A skill target
// (this task's own required step-1 research finding) ---------------------
//
// This task's own instruction was to grep for the real existing
// entry-point pattern for launching an agentworkflow.WorkflowLaunchRequest-
// shaped workflow before building anything, rather than inventing a new
// pattern if one already existed. That grep turned up three real call
// sites, not one, and none of them is a bare, general-purpose REST route:
//
//  1. internal/api/workflows.go's POST /api/workflows/runs -- initially
//     looked like the obvious precedent (same "/api/workflows"-shaped
//     path this task's own Context section explicitly names as something
//     to check), but is a RED HERRING: it drives an entirely different
//     execution engine (internal/workflow's YAML-pipeline Executor/
//     RunStore/Broadcaster), never agentworkflow.WorkflowDefinition/
//     WorkflowLaunchRequest/WorkflowLauncher at all. Confirmed directly
//     against the handler body, not assumed from the route name.
//  2. internal/selftools/self_tools_workflow_run.go's workflow_run
//     self-tool -- the real, working "launch a named agentworkflow
//     definition with params" entry point, constructing a real
//     dispatch.WorkflowLaunchRequest. But this is reachable only from
//     inside an already-running agent's own MCP tool-calling loop (a live
//     chat turn, or a CLI-launched subprocess proxying back through the
//     loopback-only POST /api/tools/call -- internal/api/tools_call.go's
//     own doc comment: "restricted to loopback callers... the `nanite mcp`
//     subprocess always reaches it via http://127.0.0.1"). Not a surface
//     an external HTTP consumer like Loom (docs/engineering/GLOSSARY.md:
//     "MCP -- the single external door for Nanite-aware external
//     consumers") can reach directly without an agent/session already in
//     the loop.
//  3. internal/service/a2a_task_manager.go's submitWorkflowTask -- the A2A
//     JSON-RPC task-submission path (GLOSSARY.md: "for callers with zero
//     Nanite-specific knowledge"), routing a target string against
//     registry.Get(target) when it isn't a msg:// address. Genuinely
//     external-facing, but shaped for "launch ANY already-registered named
//     workflow skill with a single free-text message mapped to
//     {"prompt": ...}" -- there is no TeamRunOverrides-shaped
//     (per-slot min/max counts, arbitrary Params passthrough,
//     ProjectID/ParentSessionID/TimeoutSeconds/AgentProfileID) request
//     body anywhere on this path, and a saved Team is not itself a
//     registered workflow skill by name today (only its per-launch
//     COMPILED definition is, under a fresh ulid-suffixed name nobody
//     could address ahead of time) -- so this path cannot express "launch
//     Team X by name with overrides" without new A2A-layer plumbing this
//     task's own scope does not cover.
//
// Net finding: nothing genuinely external-facing exists today that is
// both (a) reachable by a plain HTTP caller with no agent/session/A2A
// task already in flight and (b) shaped to accept TeamRunOverrides'
// richer body. Per this task file's own explicit fallback instruction
// ("If genuinely nothing external-facing exists yet... build the most
// consistent-with-this-codebase surface -- a REST route following 10's
// CRUD-endpoint conventions, is the safe default"), this file adds
// POST /api/teams/{id}/launch, mirroring task 10's own teams.go CRUD
// handlers (internal/api/teams.go) verbatim in shape -- same
// decode/errorResp/jsonResp helpers, same errors.Is-dispatched not-found
// handling, same auth posture (the server's uniform /api/* middleware
// chain; no narrower operator-only tier, per teams.go's own documented
// call). A future task is free to also expose this same LaunchTeamRun call
// through workflow_run-style self-tool or richer A2A target syntax; this
// task's own scope is the REST surface only.
package api

import (
	"errors"
	"net/http"

	"github.com/hollis-labs/nanite/internal/service"
	"github.com/hollis-labs/nanite/internal/store"
)

// teamLaunchRequest is POST /api/teams/{id}/launch's request body -- a
// TeamRunOverrides-shaped invocation-time override, per task 08's own
// TeamRunOverrides struct (internal/service/team_run_launcher.go) and
// 15-teams.md's "Runtime overrides follow the existing cascade" section.
// Every field mirrors a TeamRunOverrides field directly (same meaning, JSON-
// tagged snake_case) rather than introducing a second vocabulary for the
// same concept. Every field is optional -- an empty request body is a valid
// "launch with the Team's own saved defaults, no overrides" call.
type teamLaunchRequest struct {
	// SlotMemberCounts overrides a concurrent-activation Team Slot's
	// eagerly-resolved member count for this one launch, keyed by Team Slot
	// name. See TeamRunOverrides.SlotMemberCounts's own doc comment for the
	// [min,max] enforcement LaunchTeamRun checks this against.
	SlotMemberCounts map[string]int `json:"slot_member_counts,omitempty"`
	// Params is forwarded as WorkflowLaunchRequest.Params, layered on top
	// of a small Team-derived base -- see TeamRunOverrides.Params's own doc
	// comment.
	Params map[string]any `json:"params,omitempty"`
	// ProjectID / ParentSessionID / TimeoutSeconds / AgentProfileID forward
	// straight through to TeamRunOverrides -- see that struct's own doc
	// comments (internal/service/team_run_launcher.go).
	ProjectID       string `json:"project_id,omitempty"`
	ParentSessionID string `json:"parent_session_id,omitempty"`
	TimeoutSeconds  int    `json:"timeout_seconds,omitempty"`
	AgentProfileID  string `json:"agent_profile_id,omitempty"`
}

// teamLaunchResponse is POST /api/teams/{id}/launch's 200 response body --
// per this task's own "Done means," enough for a caller to track the
// launched run: the real workflow_runs.id (the required minimum),
// the run's status right after this call returns -- for a Team whose first
// phase is a flex step (every illustrative example in 15-teams.md) that is
// "waiting_on_flex", not "completed": the run is genuinely still in
// progress, not finished, exactly like a gate-fronted workflow already
// returns "waiting_on_gate" today -- and the concrete team_run_members rows
// resolved for this launch, so a caller doesn't need a second round-trip
// just to see who got resolved into which Team Slot.
type teamLaunchResponse struct {
	WorkflowRunID string                `json:"workflow_run_id"`
	Status        string                `json:"status"`
	Error         string                `json:"error,omitempty"`
	Members       []store.TeamRunMember `json:"members,omitempty"`
}

// handleLaunchTeam launches a saved Team by id with invocation-time
// overrides -- TASKS/teams/11-team-run-launch-api.md. See this file's own
// package-level doc comment for why REST (not a self-tool or an A2A skill
// target) is this task's chosen surface, and task 08's LaunchTeamRun
// (internal/service/team_run_launcher.go) for the actual slot-resolution /
// compile / launch behavior this handler is a thin wrapper over.
//
// POST /api/teams/{id}/launch
func (a *API) handleLaunchTeam(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if a.Services == nil || a.Services.TeamRunLauncher == nil {
		a.errorResp(w, http.StatusServiceUnavailable, "team run launcher not available")
		return
	}

	var req teamLaunchRequest
	if err := a.decode(r, &req); err != nil {
		a.errorResp(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	overrides := service.TeamRunOverrides{
		SlotMemberCounts: req.SlotMemberCounts,
		Params:           req.Params,
		ProjectID:        req.ProjectID,
		ParentSessionID:  req.ParentSessionID,
		TimeoutSeconds:   req.TimeoutSeconds,
		AgentProfileID:   req.AgentProfileID,
	}

	result, err := a.Services.TeamRunLauncher.LaunchTeamRun(r.Context(), id, overrides)
	if err != nil {
		if errors.Is(err, store.ErrTeamNotFound) {
			a.errorResp(w, http.StatusNotFound, "team not found")
			return
		}
		// Every other LaunchTeamRun failure this batch's own tests exercise
		// (ErrTeamSlotNotFound, ErrTeamRunElasticResolutionUnauthorized, a
		// member-count override outside [min,max], an empty/malformed
		// phase sequence, a slot-resolution failure) is caller-correctable
		// request-shape/authorization input, not an unexpected server
		// fault -- a 400, same posture teams.go's own validateTeamDefinition
		// path already uses for launch-time-adjacent rejections.
		a.errorResp(w, http.StatusBadRequest, err.Error())
		return
	}

	// Best-effort: the launch itself already fully succeeded (a real
	// workflow_runs row plus every team_run_members row LaunchTeamRun
	// backfilled exist) by the time this call returns -- a failure
	// re-reading them back purely for this response's own convenience must
	// not be reported as the launch itself failing.
	var members []store.TeamRunMember
	if a.Services.Store != nil {
		if ms, memberErr := a.Services.Store.ListTeamRunMembersByRun(r.Context(), result.RunID); memberErr == nil {
			members = ms
		}
	}

	a.jsonResp(w, http.StatusOK, teamLaunchResponse{
		WorkflowRunID: result.RunID,
		Status:        string(result.Status),
		Error:         result.Error,
		Members:       members,
	})
}
