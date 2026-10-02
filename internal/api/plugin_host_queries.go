package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/hollis-labs/nanite/internal/service"
	"github.com/hollis-labs/nanite/pkg/pluginapi"
)

func (a *API) handlePluginHostQuery(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	authorization := r.Header.Get("Authorization")
	if a.pluginHost == nil || !strings.HasPrefix(authorization, "Bearer ") {
		a.errorResp(w, http.StatusUnauthorized, "host query credential required")
		return
	}
	token := strings.TrimPrefix(authorization, "Bearer ")
	permit, allowed := a.pluginHost.AuthorizeHostQuery(token)
	if !allowed {
		a.errorResp(w, http.StatusUnauthorized, "host query credential is invalid")
		return
	}
	if a.Services.PluginQueries == nil {
		a.errorResp(w, http.StatusServiceUnavailable, "host queries unavailable")
		return
	}
	parameters, parseErr := url.ParseQuery(r.URL.RawQuery)
	if parseErr != nil {
		a.errorResp(w, http.StatusBadRequest, "invalid host query parameters")
		return
	}
	for key, values := range parameters {
		if key != "session_id" && key != "limit" || len(values) != 1 {
			a.errorResp(w, http.StatusBadRequest, "invalid host query parameters")
			return
		}
	}
	query := pluginapi.QueryRequest{Resource: pluginapi.QueryResource(r.PathValue("resource")), SessionID: parameters.Get("session_id")}
	if raw := parameters.Get("limit"); parameters.Has("limit") {
		limit, err := strconv.Atoi(raw)
		if err != nil || limit < 1 || limit > pluginapi.MaxQueryLimit {
			a.errorResp(w, http.StatusBadRequest, "invalid host query limit")
			return
		}
		query.Limit = limit
	}
	if !permit.Scope.Allows(query.Resource, query.SessionID) {
		a.errorResp(w, http.StatusForbidden, "query exceeds granted scope")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	stopRevocation := context.AfterFunc(permit.Context, cancel)
	defer cancel()
	defer stopRevocation()
	data, err := a.Services.PluginQueries.Read(ctx, permit.Scope, query)
	if err != nil {
		switch {
		case errors.Is(err, service.ErrPluginQueryDenied):
			a.errorResp(w, http.StatusForbidden, "query exceeds granted scope")
		case errors.Is(err, service.ErrPluginQueryNotFound):
			a.errorResp(w, http.StatusNotFound, "session not found")
		case ctx.Err() != nil:
			a.errorResp(w, http.StatusGatewayTimeout, "host query canceled")
		default:
			a.errorResp(w, http.StatusInternalServerError, "host query failed")
		}
		return
	}
	raw, err := json.Marshal(data)
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, "host query encoding failed")
		return
	}
	response, err := json.Marshal(pluginapi.QueryResponse{Protocol: pluginapi.QueryProtocol, Resource: query.Resource, SessionID: query.SessionID, Data: raw})
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, "host query encoding failed")
		return
	}
	if len(response) > pluginapi.MaxQueryResponseBytes {
		a.errorResp(w, http.StatusRequestEntityTooLarge, "host query response exceeds limit")
		return
	}
	if _, stillAllowed := a.pluginHost.AuthorizeHostQuery(token); !stillAllowed {
		a.errorResp(w, http.StatusUnauthorized, "host query connection was revoked")
		return
	}
	if ctx.Err() != nil {
		a.errorResp(w, http.StatusGatewayTimeout, "host query canceled")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(response)
}
