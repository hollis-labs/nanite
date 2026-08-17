package api

import (
	"errors"
	"net/http"
	"strings"

	"github.com/hollis-labs/nanite/internal/service"
	"github.com/hollis-labs/nanite/internal/store"
)

// CW-20260816-0020 — Loom Curator's wake seam.
//
// Fragments Engine's CallbackDestinationExecutor.Execute (FE repo,
// internal/ingest/destination_writer.go) POSTs this exact shape to Curator's
// wake URL, fire-and-forget, on a matched `callback` route:
//
//	{
//	  "generator": "wiki_page",
//	  "fragment": {
//	    "id": "...", "source": "...", "source_type": "...",
//	    "source_id": "...", "title": "...", "canonical_path": "..."
//	  }
//	}
//
// `generator` is FE's opaque destination-config tag, forwarded unmodified —
// FE never interprets it. Nanite's existing generic wake endpoint,
// POST /api/durable-agents/{id}/wake (durable_agent_wake.go), decodes the
// body as DurableAgentStartRequest{WorkspaceID, ProjectID, WakePayload:
// {Reason, Prompt, Facts, Metadata map[string]string}} — `fragment` is a
// nested object, so pointing FE's `target` straight at that endpoint would
// arrive with an empty WakePayload and lose every field. This file is the
// purpose-built decode target that closes that gap: it accepts FE's actual
// {generator, fragment} shape and folds the fragment identity into
// WakePayload.Facts so Loom Curator's boot/classify procedures
// (.nanite/agents/loom-curator.md) can read it back out.

// LoomCuratorWakeFragment mirrors the `fragment` object in FE's callback
// destination payload field-for-field.
type LoomCuratorWakeFragment struct {
	ID            string `json:"id"`
	Source        string `json:"source"`
	SourceType    string `json:"source_type"`
	SourceID      string `json:"source_id"`
	Title         string `json:"title"`
	CanonicalPath string `json:"canonical_path"`
}

// LoomCuratorWakeRequest mirrors FE's callback destination POST body
// verbatim (see doc comment above) — `generator` plus a nested `fragment`.
type LoomCuratorWakeRequest struct {
	Generator string                  `json:"generator"`
	Fragment  LoomCuratorWakeFragment `json:"fragment"`
}

// loomCuratorInstanceSlug is the .nanite/durable-agents/loom-curator.yaml
// `slug:` value. Resolving by slug (instead of requiring FE to know
// Curator's DB-minted instance UUID) is what lets this endpoint's URL stay
// fixed and version-controlled across environments and DB resets.
const loomCuratorInstanceSlug = "loom-curator"

// loomCuratorWakeWorkspaceID: Nanite consolidated to a single dogfood
// workspace by explicit operator decision on 2026-08-15 (migration
// 087_consolidate_personal_workspace.sql; the sole seeded row is
// store/seed.go's {"default", "Default", ...}). durableWakeService.Wake
// skips a wake with "workspace unavailable" when no WorkspaceID is supplied
// and the instance has no prior session to inherit one from — which is
// exactly Curator's state on its very first-ever callback wake. Supplying
// "default" explicitly here means the callback seam works from the first
// call, not only after some undocumented manual bootstrap wake. This
// literal is safe only because of the single-workspace consolidation above;
// revisit if Nanite ever reintroduces multiple workspaces.
const loomCuratorWakeWorkspaceID = "default"

// handleLoomCuratorWake decodes FE's callback payload, resolves it to the
// `loom-curator` durable_agent_instances row, and wakes it with the
// fragment identity folded into WakePayload.Facts.
func (a *API) handleLoomCuratorWake(w http.ResponseWriter, r *http.Request) {
	var req LoomCuratorWakeRequest
	if err := a.decode(r, &req); err != nil {
		a.errorResp(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if strings.TrimSpace(req.Fragment.ID) == "" {
		a.errorResp(w, http.StatusBadRequest, "fragment.id is required")
		return
	}

	inst, err := a.Services.Store.GetDurableAgentInstanceBySlug(loomCuratorInstanceSlug)
	if err != nil {
		if errors.Is(err, store.ErrDurableAgentInstanceNotFound) {
			a.errorResp(w, http.StatusServiceUnavailable,
				"loom curator durable-agent instance not provisioned: "+
					".nanite/durable-agents/loom-curator.yaml has not been synced yet "+
					"(the service must be restarted once after that file is added — "+
					"SyncManagedDurableAgentConfigs only runs at container boot)")
			return
		}
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}

	generator := strings.TrimSpace(req.Generator)
	reason := "callback"
	if generator != "" {
		reason = "callback:" + generator
	}

	facts := map[string]string{
		"generator":               generator,
		"fragment_id":             req.Fragment.ID,
		"fragment_source":         req.Fragment.Source,
		"fragment_source_type":    req.Fragment.SourceType,
		"fragment_source_id":      req.Fragment.SourceID,
		"fragment_title":          req.Fragment.Title,
		"fragment_canonical_path": req.Fragment.CanonicalPath,
	}
	for k, v := range facts {
		if v == "" {
			delete(facts, k)
		}
	}

	result, err := a.Services.DurableWake.Wake(r.Context(), inst.ID, service.DurableAgentWakeRequest{
		WorkspaceID: loomCuratorWakeWorkspaceID,
		WakePayload: service.DurableAgentWakePayload{
			Reason: reason,
			Facts:  facts,
		},
	})
	if err != nil {
		a.errorResp(w, http.StatusConflict, err.Error())
		return
	}
	a.jsonResp(w, http.StatusOK, result)
}
