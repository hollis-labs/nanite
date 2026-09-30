package api

import (
	"net/http"
	"strconv"

	"github.com/hollis-labs/nanite/internal/chat"
	"github.com/hollis-labs/nanite/internal/service"
)

func (a *API) handleGetSessionExecutionMetrics(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("id")
	metrics, err := a.Services.Store.GetSessionExecutionMetrics(r.Context(), sessionID)
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, "failed to load execution metrics")
		return
	}
	a.jsonResp(w, http.StatusOK, metrics)
}

func (a *API) handleGetRecentExecutionMetrics(w http.ResponseWriter, r *http.Request) {
	limit := 50
	if l := r.URL.Query().Get("limit"); l != "" {
		if n, err := strconv.Atoi(l); err == nil && n > 0 {
			limit = n
		}
	}
	metrics, err := a.Services.Store.GetRecentExecutionMetrics(r.Context(), limit)
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, "failed to load execution metrics")
		return
	}
	a.jsonResp(w, http.StatusOK, metrics)
}

func (a *API) handleGetUtilityCallSummary(w http.ResponseWriter, r *http.Request) {
	summary, err := a.Services.Store.GetUtilityCallSummary(r.Context())
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, "failed to load utility call summary")
		return
	}
	a.jsonResp(w, http.StatusOK, summary)
}

func (a *API) handleGetUtilityCallLog(w http.ResponseWriter, r *http.Request) {
	limit := 50
	if l := r.URL.Query().Get("limit"); l != "" {
		if n, err := strconv.Atoi(l); err == nil && n > 0 {
			limit = n
		}
	}
	log, err := a.Services.Store.GetUtilityCallLog(r.Context(), limit)
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, "failed to load utility call log")
		return
	}
	a.jsonResp(w, http.StatusOK, log)
}

// handleGetSessionHarnessProfile reports the harness profile the session's next
// turn would run under: the resolved values, the layer that supplied each, and
// the profile digest. Recorded values for turns already run are on
// /api/sessions/{id}/metrics. The model is the request's ?model=, else the
// session's, else the agent's default; the chat loop may resolve a different
// model when none of these is set.
func (a *API) handleGetSessionHarnessProfile(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("id")
	sess, err := a.Services.Store.GetSession(r.Context(), sessionID)
	if err != nil || sess == nil {
		a.errorResp(w, http.StatusNotFound, "session not found")
		return
	}
	var constraints chat.AgentConstraints
	agentModel := ""
	if agent, aerr := a.Services.Agents.ResolveForSession(r.Context(), sessionID); aerr == nil && agent != nil {
		constraints = chat.ParseAgentConstraints(agent.Constraints)
		agentModel = agent.DefaultModel
	}
	model := r.URL.Query().Get("model")
	if model == "" {
		model = sess.Model
	}
	if model == "" {
		model = agentModel
	}
	res, err := service.ResolveHarness(r.Context(), a.Services.HarnessProfiles, a.Services.Store, sess, constraints, model)
	if err != nil {
		a.errorResp(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	a.jsonResp(w, http.StatusOK, res.Effective())
}
