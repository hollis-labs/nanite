package api

import (
	"encoding/json"
	"net/http"

	"github.com/hollis-labs/nanite/internal/a2a"
)

// handleAgentCard serves the A2A Agent Card at /.well-known/agent-card.json.
// Per A2A v1.0 spec, this is the capability-discovery endpoint for the Nanite host.
//
// One card represents the entire Nanite instance, not one per durable-agent.
// Skills are derived from:
// - Named workflow definitions from the Agent Workflows registry
// - Durable-agent boot profiles that can be started fresh via a Task
func (a *API) handleAgentCard(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Generate the Agent Card using the service-layer generator
	cardGen := a.Services.AgentCardGenerator
	if cardGen == nil {
		http.Error(w, "Agent Card generation not available", http.StatusServiceUnavailable)
		return
	}

	card, err := cardGen.Generate()
	if err != nil {
		http.Error(w, "Failed to generate Agent Card: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(card); err != nil {
		http.Error(w, "Failed to encode Agent Card: "+err.Error(), http.StatusInternalServerError)
		return
	}
}

// handleWhoami serves agent address self-discovery at /api/whoami.
// Returns the canonical msg:// address for a given agent ID.
//
// Query params:
//   - agent_id: the durable agent instance ID (required)
//
// Response: {"address": "msg://agent/nanite/agt_..."}
func (a *API) handleWhoami(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	agentID := r.URL.Query().Get("agent_id")
	if agentID == "" {
		http.Error(w, "agent_id query parameter required", http.StatusBadRequest)
		return
	}

	// Derive canonical address using the a2a package
	addr := a2a.NewAgentAddress(agentID)

	response := map[string]string{
		"address": addr.URN(),
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(response); err != nil {
		http.Error(w, "Failed to encode response: "+err.Error(), http.StatusInternalServerError)
		return
	}
}
