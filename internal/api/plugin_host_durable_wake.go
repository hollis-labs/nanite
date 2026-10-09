package api

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/hollis-labs/libs/plugin-mcp/plugin-sdk/manifest"
	"github.com/hollis-labs/nanite/internal/service"
	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/pkg/pluginapi"
)

// The core owns resolution and execution. Integration plugins translate their
// callback payload into a prompt, and receive only a queued-state projection.
func (a *API) handlePluginHostDurableWake(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	authorization := r.Header.Get("Authorization")
	if a.pluginHost == nil || !strings.HasPrefix(authorization, "Bearer ") {
		a.errorResp(w, 401, "wake credential required")
		return
	}
	token := strings.TrimPrefix(authorization, "Bearer ")
	permit, allowed := a.pluginHost.AuthorizeHostDurableWake(token)
	if !allowed {
		a.errorResp(w, 401, "wake credential invalid")
		return
	}
	if a.Services.DurableAgents == nil || a.Services.DurableWake == nil {
		a.errorResp(w, 503, "durable wakes unavailable")
		return
	}
	if r.URL.RawQuery != "" {
		a.errorResp(w, 400, "wake query parameters are not accepted")
		return
	}
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, pluginapi.MaxDurableWakeBytes))
	if err != nil {
		a.errorResp(w, 413, "wake request exceeds limit")
		return
	}
	var call pluginapi.DurableWakeRequest
	if err = manifest.DecodeExtension(raw, &call); err != nil {
		a.errorResp(w, 400, "invalid wake request")
		return
	}
	if err = call.Validate(); err != nil {
		a.errorResp(w, 400, "invalid wake request")
		return
	}
	if !permit.Scope.Allows(call.AgentSlug) {
		a.errorResp(w, 403, "wake exceeds granted scope")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	stopRevocation := context.AfterFunc(permit.Context, cancel)
	defer cancel()
	defer stopRevocation()
	instance, err := a.Services.DurableAgents.GetBySlug(ctx, call.AgentSlug)
	if err != nil {
		if ctx.Err() != nil {
			a.errorResp(w, http.StatusGatewayTimeout, "wake canceled")
		} else if errors.Is(err, store.ErrDurableAgentInstanceNotFound) {
			a.errorResp(w, 404, "durable instance not provisioned")
		} else {
			a.errorResp(w, 500, "durable instance lookup failed")
		}
		return
	}
	if ctx.Err() != nil {
		a.errorResp(w, 504, "wake canceled")
		return
	}
	if _, allowed = a.pluginHost.AuthorizeHostDurableWake(token); !allowed {
		a.errorResp(w, 401, "wake connection revoked")
		return
	}
	result, err := a.Services.DurableWake.Wake(ctx, instance.ID, service.DurableAgentWakeRequest{WakePayload: service.DurableAgentWakePayload{Reason: call.Reason, Prompt: call.Prompt, Facts: call.Facts}})
	if err != nil || result == nil || result.Skipped || result.FailureReason != "" {
		if ctx.Err() != nil {
			a.errorResp(w, 504, "wake canceled")
		} else {
			a.errorResp(w, 409, "durable agent wake unavailable")
		}
		return
	}
	response := pluginapi.DurableWakeResponse{Protocol: pluginapi.DurableWakeProtocol, AgentSlug: call.AgentSlug, InstanceID: result.InstanceID, Status: "queued"}
	if result.LaunchResult != nil && result.LaunchResult.Session != nil {
		response.SessionID = result.LaunchResult.Session.ID
	}
	a.jsonResp(w, 200, response)
}
