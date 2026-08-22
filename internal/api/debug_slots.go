package api

import (
	"encoding/json"
	"net/http"
)

// SlotData describes one context window slot for the debug endpoint.
type SlotData struct {
	Name   string `json:"name"`
	Tokens int    `json:"tokens"`
	Cached bool   `json:"cached"`
}

// DebugSlotsResponse is returned by GET /api/debug/slots.
type DebugSlotsResponse struct {
	SessionID   string     `json:"session_id"`
	Slots       []SlotData `json:"slots"`
	TotalBudget int        `json:"total_budget"`
	TotalUsed   int        `json:"total_used"`
}

func (a *API) handleDebugSlots(w http.ResponseWriter, r *http.Request) {
	sessionID := r.URL.Query().Get("session_id")
	if sessionID == "" {
		a.errorResp(w, http.StatusBadRequest, "session_id query parameter is required")
		return
	}

	// Get the most recent execution metrics for this session.
	metrics, err := a.Services.Store.GetSessionExecutionMetrics(r.Context(), sessionID)
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, "failed to load execution metrics")
		return
	}

	resp := DebugSlotsResponse{
		SessionID: sessionID,
		Slots:     make([]SlotData, 0),
	}

	if len(metrics) == 0 {
		a.jsonResp(w, http.StatusOK, resp)
		return
	}

	// Most recent metric (metrics are ordered DESC by created_at).
	latest := metrics[0]

	// Try to extract slot data from debug_snapshots.
	if latest.DebugSnapshots != "" && latest.DebugSnapshots != "null" {
		var snapshots []map[string]any
		if err := json.Unmarshal([]byte(latest.DebugSnapshots), &snapshots); err == nil && len(snapshots) > 0 {
			lastSnap := snapshots[len(snapshots)-1]
			if slotsRaw, ok := lastSnap["slots"]; ok {
				if slotArr, ok := slotsRaw.([]any); ok {
					for _, s := range slotArr {
						if sm, ok := s.(map[string]any); ok {
							slot := SlotData{
								Name: stringFromMap(sm, "name"),
							}
							if v, ok := sm["tokens"].(float64); ok {
								slot.Tokens = int(v)
							}
							if v, ok := sm["cached"].(bool); ok {
								slot.Cached = v
							}
							resp.Slots = append(resp.Slots, slot)
						}
					}
				}
			}
			if v, ok := lastSnap["total_budget"].(float64); ok {
				resp.TotalBudget = int(v)
			}
		}
	}

	// Compute total used from slots or fall back to execution metric fields.
	if len(resp.Slots) > 0 {
		for _, s := range resp.Slots {
			resp.TotalUsed += s.Tokens
		}
	} else {
		// No slot-level data; provide aggregate from execution metrics.
		resp.TotalUsed = latest.ContextTokens
		resp.Slots = append(resp.Slots, SlotData{
			Name:   "Context",
			Tokens: latest.ContextTokens,
			Cached: latest.CacheReadTokens > 0,
		})
	}

	a.jsonResp(w, http.StatusOK, resp)
}

// stringFromMap safely extracts a string value from a map.
func stringFromMap(m map[string]any, key string) string {
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}
