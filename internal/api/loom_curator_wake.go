package api

import (
	"errors"
	"fmt"
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
// body as DurableAgentStartRequest{ProjectID, WakePayload:
// {Reason, Prompt, Facts, Metadata map[string]string}} — `fragment` is a
// nested object, so pointing FE's `target` straight at that endpoint would
// arrive with an empty WakePayload and lose every field. This file is the
// purpose-built decode target that closes that gap: it accepts FE's actual
// {generator, fragment} shape and folds the fragment identity into the wake.
//
// CW-20260817 finding (this endpoint reported done in CW-20260816-0020 but
// never actually ran Curator's classify/compile procedure end to end):
// WakePayload.Facts is NOT read by anything downstream of Wake()/Start() —
// grep the whole service package and the only consumer is the recipe
// wake-defaults substitution DSL used to prefill an operator's create-agent
// form, which never runs on this path. The ONLY wired channel that turns a
// wake into a real, running agent turn is DurableAgentWakePayload.Prompt:
// durableAgentService.Start's deliverWakePrompt only calls
// ChatService.HandleMessage (a real user turn, async generation) when Prompt
// is non-empty — see durable_agents.go's deliverWakePrompt doc comment and
// durable_wake.go's RunDue, which forwards a schedule's Body into Prompt for
// exactly this reason. The original implementation here only ever populated
// Facts, so every callback wake created a session, marked the instance
// active, and returned in a few milliseconds — without ever delivering a
// message, so the async generation goroutine never started and Curator's
// classify_and_compile_fragment procedure never ran. buildLoomCuratorWakePrompt
// below renders the fragment identity into real prompt text so the turn
// actually fires; Facts is still populated too (harmless, cheap, and lines
// up with what loom-curator.md's procedures describe reading), but Prompt is
// what makes the wake real.

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

// handleLoomCuratorWake decodes FE's callback payload, resolves it to the
// `loom-curator` durable_agent_instances row, and wakes it with the
// fragment identity rendered into a real WakePayload.Prompt turn (and also
// folded into WakePayload.Facts for anything that later reads it back).
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

	inst, err := a.Services.Store.GetDurableAgentInstanceBySlug(r.Context(), loomCuratorInstanceSlug)
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
		WakePayload: service.DurableAgentWakePayload{
			Reason: reason,
			Prompt: buildLoomCuratorWakePrompt(generator, req.Fragment),
			Facts:  facts,
		},
	})
	if err != nil {
		a.errorResp(w, http.StatusConflict, err.Error())
		return
	}
	a.jsonResp(w, http.StatusOK, result)
}

// buildLoomCuratorWakePrompt renders FE's fragment identity into the real
// user-turn text delivered to Curator's session (see the file doc comment —
// this is the only field that actually reaches a running agent turn).
// Deliberately does not include fragment body/content: per
// loom-architecture.md §4 ("no template content, ever") the callback payload
// is opaque by design, so the prompt instructs Curator to fetch the
// fragment's real content itself via its fragments_* role tools before
// classifying.
func buildLoomCuratorWakePrompt(generator string, frag LoomCuratorWakeFragment) string {
	var b strings.Builder
	b.WriteString("Fragments Engine routed a fragment into the `nanite` wiki bundle via a callback wake")
	if generator != "" {
		fmt.Fprintf(&b, " (generator: %s)", generator)
	}
	b.WriteString(".\n\nRun your classify_and_compile_fragment procedure now. This payload carries no body text by design — fetch the fragment's real content yourself via fragment_id before classifying. Fragment identity:\n")
	fmt.Fprintf(&b, "- fragment_id: %s\n", frag.ID)
	if frag.Source != "" {
		fmt.Fprintf(&b, "- fragment_source: %s\n", frag.Source)
	}
	if frag.SourceType != "" {
		fmt.Fprintf(&b, "- fragment_source_type: %s\n", frag.SourceType)
	}
	if frag.SourceID != "" {
		fmt.Fprintf(&b, "- fragment_source_id: %s\n", frag.SourceID)
	}
	if frag.Title != "" {
		fmt.Fprintf(&b, "- fragment_title: %s\n", frag.Title)
	}
	if frag.CanonicalPath != "" {
		fmt.Fprintf(&b, "- fragment_canonical_path: %s\n", frag.CanonicalPath)
	}
	return b.String()
}
