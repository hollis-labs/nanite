package api

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strings"

	agentpkg "github.com/hollis-labs/nanite/internal/agent"
	"github.com/hollis-labs/nanite/internal/agentimport"
	"github.com/hollis-labs/nanite/internal/service"
)

// CW-20260910-0013: the REST twin of `nanite agent install` / `nanite agent
// sync`, mirroring the skills precedent (POST /api/skills/install,
// POST /api/skills/{slug}/sync) so the GUI and API reach agent import, not
// only the CLI.
//
// # The ownership boundary is reused, not re-implemented
//
// Most of it was already built. agentView already puts ManageClass and
// CopyToManaged on every profile the client receives, and writeNotManaged
// already writes the per-class 409 — including the ManageClassExternal case,
// which imported agents now make reachable for the first time. So the sync
// guard below CALLS writeNotManaged rather than growing its own refusal.
// TestAgentImportSyncRefusesNonImportedThroughWriteNotManaged asserts the
// response is byte-for-byte the shape that helper produces, which is what
// makes "verify it routes correctly" a check rather than a claim.
//
// # An *agentimport.Importer is never shared across requests
//
// Same reasoning as service.Container.SkillVendor's doc comment: the
// Importer's State()/Emit are single-call-scoped. Each handler builds a fresh
// one from the two stateless, concurrency-safe primitives the container
// shares — Store (satisfying agentimport.ProfileStore directly) and
// AdapterRegistry.

// runAgentImport builds a fresh Importer and runs the Parse -> Validate ->
// Write pipeline against path. The returned int is the HTTP status a caller
// should use when err is non-nil: a Parsing/Validating failure is the
// caller's malformed source (422), a Writing failure is server-side (500).
//
// Classification reads the Importer's own event stream rather than parsing
// error text — Import's wrapped errors are for a human, not for control flow.
// This mirrors runSkillInstall exactly.
func (a *API) runAgentImport(ctx context.Context, path, adapter string) (agentimport.Result, int, error) {
	var lastState agentimport.State
	importer := &agentimport.Importer{
		Store:        a.Services.Store,
		Parse:        a.agentImportParser(adapter),
		SeedChildren: service.SeedImportedAgentChildren(a.Services.Store),
		Emit: func(e agentimport.Event) {
			if e.Err == nil {
				lastState = e.State
			}
		},
	}

	result, err := importer.Import(ctx, agentimport.Source{Path: path})
	if err != nil {
		var status int
		switch lastState {
		case agentimport.StateParsing, agentimport.StateValidating:
			// The caller named a path that is not a usable definition.
			status = http.StatusUnprocessableEntity
		default:
			// StateWriting is a database problem; the remaining states
			// cannot be the last successful one on an error path. Either
			// way it is ours, not the caller's.
			status = http.StatusInternalServerError
		}
		return agentimport.Result{}, status, err
	}
	return result, http.StatusOK, nil
}

// agentImportParser builds the same format chain cmd/nanite builds: Nanite's
// own format first because a definition authored for Nanite is the ordinary
// case, then the registered format adapters in priority order. A named
// adapter skips the chain.
func (a *API) agentImportParser(adapter string) agentimport.Parser {
	registry := a.Services.AdapterRegistry
	if adapter != "" {
		return agentimport.RegistryParser{Registry: registry, Adapter: adapter}
	}
	return agentimport.ChainParser{Parsers: []agentimport.Parser{
		agentimport.NativeParser{},
		agentimport.RegistryParser{Registry: registry},
	}}
}

// toImportAgentResponse renders a Result for the wire, decorating each
// created/synced outcome with the same ownership view every other agent
// endpoint returns — so a client sees immediately that what it just imported
// is read-only and offers a copy path.
func (a *API) toImportAgentResponse(res agentimport.Result) ImportAgentResponse {
	created, synced, skipped := res.Counts()
	out := ImportAgentResponse{
		Path:    res.Path,
		Created: created,
		Synced:  synced,
		Skipped: skipped,
	}
	for _, o := range res.Outcomes {
		item := ImportAgentOutcome{
			Slug:   o.Slug,
			Name:   o.Name,
			Action: string(o.Action),
			Reason: o.Reason,
		}
		if o.BlockedBy != "" {
			item.BlockedBy = string(o.BlockedBy)
			item.CopyToManaged = o.BlockedBy.CopyToManagedAllowed()
		}
		if o.Profile != nil {
			view := a.agentView(*o.Profile)
			item.Agent = &view
		}
		out.Outcomes = append(out.Outcomes, item)
	}
	return out
}

// handleInstallAgent implements POST /api/agents/install: import agent
// definitions from a local path.
//
// A path may expand to more than one definition — that is the "directory
// argument is sugar, not a tier" contract, and each definition gets its own
// outcome in the response. A definition refused because its slug belongs to a
// profile import does not own is reported as a skipped outcome rather than
// aborting the rest, and the response is 409 when anything was skipped so a
// client cannot mistake a partial import for a complete one.
func (a *API) handleInstallAgent(w http.ResponseWriter, r *http.Request) {
	var req ImportAgentRequest
	if err := a.decode(r, &req); err != nil {
		a.errorResp(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if strings.TrimSpace(req.Path) == "" {
		a.errorResp(w, http.StatusBadRequest, "path is required")
		return
	}

	result, status, err := a.runAgentImport(r.Context(), req.Path, req.Adapter)
	if err != nil {
		a.errorResp(w, status, "agent import failed: "+err.Error())
		return
	}

	resp := a.toImportAgentResponse(result)
	if result.Skipped() {
		a.jsonResp(w, http.StatusConflict, resp)
		return
	}
	a.jsonResp(w, http.StatusCreated, resp)
}

// handleSyncAgent implements POST /api/agents/{slug}/sync: re-run the import
// pipeline against the same source for an already-imported agent.
//
// Three guards, all before anything is written:
//
//  1. the slug must already exist (404) — sync re-reads a known agent, it does
//     not silently create one;
//  2. that agent must be one import owns (409 through writeNotManaged — an
//     imported agent is external, and external is exactly the class that
//     helper already handles);
//  3. the source at path must declare that same slug (409) — this endpoint
//     re-syncs a known agent, it does not let a caller relabel one
//     definition's content onto a different agent's slug.
//
// Guard 3 parses the source (a pure read, no write of any kind) before the
// real Importer runs, so a mismatched sync target is rejected without
// mutating anything — the same pre-flight ordering handleSyncSkill uses.
func (a *API) handleSyncAgent(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")

	existing, err := a.Services.Store.GetAgentBySlug(r.Context(), slug)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	if existing == nil {
		a.errorResp(w, http.StatusNotFound, "agent not found: "+slug)
		return
	}
	// The ownership boundary, reused rather than re-implemented. An imported
	// agent is ManageClassExternal, which is not Editable() and IS
	// CopyToManagedAllowed() — so writeNotManaged already says the right
	// thing, including the copy path, for every class this can refuse.
	if class := a.Services.AgentConfig.Classify(existing); class != agentpkg.ManageClassExternal {
		a.writeNotManaged(w, existing, class)
		return
	}

	var req ImportAgentRequest
	if decodeErr := a.decode(r, &req); decodeErr != nil {
		a.errorResp(w, http.StatusBadRequest, "invalid JSON: "+decodeErr.Error())
		return
	}
	if strings.TrimSpace(req.Path) == "" {
		a.errorResp(w, http.StatusBadRequest, "path is required")
		return
	}

	defs, parseErr := a.agentImportParser(req.Adapter).Parse(req.Path)
	if parseErr != nil {
		a.errorResp(w, http.StatusUnprocessableEntity, "agent sync failed: parse: "+parseErr.Error())
		return
	}
	if !definesSlug(defs, slug) {
		a.errorResp(w, http.StatusConflict, fmt.Sprintf(
			"source at %q does not declare slug %q", req.Path, slug))
		return
	}

	result, status, importErr := a.runAgentImport(r.Context(), req.Path, req.Adapter)
	if importErr != nil {
		a.errorResp(w, status, "agent sync failed: "+importErr.Error())
		return
	}

	resp := a.toImportAgentResponse(result)
	if result.Skipped() {
		a.jsonResp(w, http.StatusConflict, resp)
		return
	}
	a.jsonResp(w, http.StatusOK, resp)
}

func definesSlug(defs []*agentpkg.Definition, slug string) bool {
	for _, d := range defs {
		if d != nil && d.Slug == slug {
			return true
		}
	}
	return false
}
