package api

// inspector.go — I1 (CW-20260426-0004)
//
// GET /api/inspector/sessions/{session_id}/turns
// GET /api/inspector/sessions/{session_id}/turns/{turn_id}
//
// Both endpoints return 404 when developer_mode is false or the inspector
// service was not wired at startup.

import (
	"net/http"
	"strconv"

	inspectsvc "github.com/hollis-labs/nanite/internal/inspector"
)

// handleInspectorListTurns returns recent TurnSnapshots for a session.
//
// Query params:
//   - limit (int, default 20): maximum number of snapshots to return
//
// Response: {"session_id": "...", "turns": [...], "count": N}
func (a *API) handleInspectorListTurns(w http.ResponseWriter, r *http.Request) {
	if a.Services.Inspector == nil {
		a.errorResp(w, http.StatusNotFound, "inspector not enabled — set developer_mode=true and restart")
		return
	}

	sessionID := r.PathValue("session_id")
	if sessionID == "" {
		a.errorResp(w, http.StatusBadRequest, "session_id path parameter is required")
		return
	}

	limit := 20
	if raw := r.URL.Query().Get("limit"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			limit = n
		}
	}

	turns := a.Services.Inspector.RecentSnapshots(sessionID, limit)

	type inspectorListResponse struct {
		SessionID string                    `json:"session_id"`
		Turns     []inspectsvc.TurnSnapshot `json:"turns"`
		Count     int                       `json:"count"`
	}

	if turns == nil {
		turns = []inspectsvc.TurnSnapshot{}
	}

	a.jsonResp(w, http.StatusOK, inspectorListResponse{
		SessionID: sessionID,
		Turns:     turns,
		Count:     len(turns),
	})
}

// handleInspectorGetTurn returns the full TurnSnapshot for a specific turn.
//
// Path params:
//   - session_id: session identifier
//   - turn_id: monotonic turn number ("1", "2", …)
func (a *API) handleInspectorGetTurn(w http.ResponseWriter, r *http.Request) {
	if a.Services.Inspector == nil {
		a.errorResp(w, http.StatusNotFound, "inspector not enabled — set developer_mode=true and restart")
		return
	}

	sessionID := r.PathValue("session_id")
	turnID := r.PathValue("turn_id")
	if sessionID == "" || turnID == "" {
		a.errorResp(w, http.StatusBadRequest, "session_id and turn_id path parameters are required")
		return
	}

	snap := a.Services.Inspector.Snapshot(sessionID, turnID)
	if snap == nil {
		a.errorResp(w, http.StatusNotFound, "turn not found")
		return
	}

	a.jsonResp(w, http.StatusOK, snap)
}
