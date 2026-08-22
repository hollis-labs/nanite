package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/hollis-labs/nanite/internal/skill"
	"github.com/hollis-labs/nanite/internal/skillinstall"
	"github.com/hollis-labs/nanite/internal/store"
)

// devModeEnabled returns true when either the NANITE_DEVMODE env var is set
// to a truthy value (1/true/yes/on) or the user_settings.developer_mode flag
// is enabled. E1 (CW-20260428-0016) gates the inline-edit affordances on
// internal skills behind this check.
func (a *API) devModeEnabled(r *http.Request) bool {
	if envDevModeOn() {
		return true
	}
	if a.Services.Store == nil {
		return false
	}
	us, err := a.Services.Store.GetUserSettings()
	if err != nil || us == nil {
		return false
	}
	return us.DeveloperMode
}

func envDevModeOn() bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv("NANITE_DEVMODE")))
	switch v {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

func (a *API) handleListSkills(w http.ResponseWriter, r *http.Request) {
	var skills []store.Skill
	var err error

	if source := r.URL.Query().Get("source"); source != "" {
		skills, err = a.Services.Skills.ListBySource(r.Context(), source)
	} else {
		skills, err = a.Services.Skills.List(r.Context())
	}
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.jsonResp(w, http.StatusOK, skills)
}

func (a *API) handleCreateSkill(w http.ResponseWriter, r *http.Request) {
	var req CreateSkillRequest
	if err := a.decode(r, &req); err != nil {
		a.errorResp(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if req.Name == "" || req.Slug == "" {
		a.errorResp(w, http.StatusBadRequest, "name and slug are required")
		return
	}

	sk := &store.Skill{
		Name:                 req.Name,
		Slug:                 req.Slug,
		Description:          req.Description,
		Category:             req.Category,
		Icon:                 req.Icon,
		InputSchema:          req.InputSchema,
		SourceTier:           req.SourceTier,
		ContentHash:          req.ContentHash,
		DeclaredDependencies: req.DeclaredDependencies,
		Enabled:              true,
	}
	if req.Enabled != nil {
		sk.Enabled = *req.Enabled
	}
	if err := a.Services.Skills.Create(r.Context(), sk); err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.jsonResp(w, http.StatusCreated, sk)
}

// resolveSkillRef resolves ref against the skill index, trying it first as
// a slug -- this batch's addressing convention throughout the rest of the
// skill REST surface (install/sync, grant, preview, and this task's own
// real uninstall all key off slug, never a bare index-row ID) -- and
// falling back to a bare index-row ID (task 02's earlier admin-CRUD
// surface's own addressing: handleCreateSkill/handleUpdateSkill still hand
// callers an ID-keyed row, and there is no reason a caller holding only
// that ID should lose lookup/delete access here). Returns (nil, nil) when
// neither resolves, matching a.Services.Skills.Get/GetBySlug's own
// "not found is nil, nil" convention.
func (a *API) resolveSkillRef(ctx context.Context, ref string) (*store.Skill, error) {
	sk, err := a.Services.Skills.GetBySlug(ctx, ref)
	if err != nil {
		return nil, err
	}
	if sk != nil {
		return sk, nil
	}
	return a.Services.Skills.Get(ctx, ref)
}

// handleGetSkill implements GET /api/skills/{slug} -- task 12's "List/get
// indexed skills" item. See resolveSkillRef's doc comment for why the path
// value (still named "slug" at the route, see internal/api/api.go) also
// accepts a bare index-row ID as a fallback.
func (a *API) handleGetSkill(w http.ResponseWriter, r *http.Request) {
	ref := r.PathValue("slug")
	sk, err := a.resolveSkillRef(r.Context(), ref)
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	if sk == nil {
		a.errorResp(w, http.StatusNotFound, "skill not found: "+ref)
		return
	}
	a.jsonResp(w, http.StatusOK, sk)
}

func (a *API) handleUpdateSkill(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	existing, err := a.Services.Skills.Get(r.Context(), id)
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	if existing == nil {
		a.errorResp(w, http.StatusNotFound, "skill not found")
		return
	}

	var req UpdateSkillRequest
	if err := a.decode(r, &req); err != nil {
		a.errorResp(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}

	if req.Name != nil {
		existing.Name = *req.Name
	}
	if req.Slug != nil {
		existing.Slug = *req.Slug
	}
	if req.Description != nil {
		existing.Description = *req.Description
	}
	if req.Category != nil {
		existing.Category = *req.Category
	}
	if req.Icon != nil {
		existing.Icon = *req.Icon
	}
	if req.InputSchema != nil {
		existing.InputSchema = *req.InputSchema
	}
	if req.SourceTier != nil {
		existing.SourceTier = *req.SourceTier
	}
	if req.ContentHash != nil {
		existing.ContentHash = *req.ContentHash
	}
	if req.DeclaredDependencies != nil {
		existing.DeclaredDependencies = *req.DeclaredDependencies
	}
	if req.Enabled != nil {
		existing.Enabled = *req.Enabled
	}

	if err := a.Services.Skills.Update(r.Context(), existing); err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.jsonResp(w, http.StatusOK, existing)
}

// handleDeleteSkill implements DELETE /api/skills/{slug} -- task 12's real
// uninstall: "remove the index row (task 02) and the vendored copy (task
// 03's deletion primitive)." Replaces the old bare
// a.Services.Skills.Delete(id) index-row-only delete (task 02's own doc
// comment on that call site flagged this handler's contract as "the full
// redesign... is explicitly later work" -- this task). Shares its actual
// deletion mechanics with the skill_delete self-tool
// (internal/selftools/self_tools_transport.go's callDeleteSkill) via
// skillinstall.Uninstaller -- see that type's doc comment for why vendor
// deletion always runs before the index row is removed.
func (a *API) handleDeleteSkill(w http.ResponseWriter, r *http.Request) {
	ref := r.PathValue("slug")
	sk, err := a.resolveSkillRef(r.Context(), ref)
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	if sk == nil {
		a.errorResp(w, http.StatusNotFound, "skill not found: "+ref)
		return
	}

	// a.Services.SkillVendor is a *skillvendor.Store that may itself be a
	// nil pointer when the vendor root failed to initialize at
	// container-build time (Container.SkillVendor's own doc comment). It
	// must never be assigned directly into the UninstallVendorer interface
	// field in that case -- doing so would produce a non-nil interface
	// wrapping a nil concrete pointer (Go's classic "typed nil" trap),
	// which Uninstall's own `u.Vendor == nil` guard cannot detect, leading
	// to a nil-pointer panic inside Delete instead of Uninstall's intended
	// clear error.
	var vendor skillinstall.UninstallVendorer
	if a.Services.SkillVendor != nil {
		vendor = a.Services.SkillVendor
	}
	u := &skillinstall.Uninstaller{Vendor: vendor, Index: a.Services.Store}
	result, err := u.Uninstall(sk)
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, "skill uninstall failed: "+err.Error())
		return
	}
	a.jsonResp(w, http.StatusOK, map[string]any{
		"status":         "deleted",
		"skill":          result.Skill,
		"vendor_deleted": result.VendorDeleted,
	})
}

func (a *API) handleListAgentSkills(w http.ResponseWriter, r *http.Request) {
	agentID := r.PathValue("id")
	skills, err := a.Services.Store.ListAgentSkills(agentID)
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.jsonResp(w, http.StatusOK, skills)
}

func (a *API) handleAssignAgentSkill(w http.ResponseWriter, r *http.Request) {
	agentID := r.PathValue("id")

	var req AssignAgentSkillRequest
	if err := a.decode(r, &req); err != nil {
		a.errorResp(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if req.SkillID == "" {
		a.errorResp(w, http.StatusBadRequest, "skill_id is required")
		return
	}

	// Verify agent exists as a real agent_profiles DB row directly against
	// the store (a.Services.Agents.Get is equivalent post-TASKS/adhoc/01-
	// eliminate-file-based-agent-runtime.md -- it is a plain DB passthrough
	// now too -- but this direct call is kept as the explicit, load-bearing
	// check: AssignSkillToAgent now writes through agent_known_skills
	// (TASKS/skills/02 -- the old, now-dropped per-agent skill join table
	// used to carry this FK instead), which carries the same real FK to
	// agent_profiles(id), so the existence check here must match what the
	// FK actually enforces, independent of whatever AgentService does).
	if _, err := a.Services.Store.GetAgent(agentID); err != nil {
		a.errorResp(w, http.StatusNotFound, "agent not found")
		return
	}
	// Verify skill exists.
	sk, err := a.Services.Skills.Get(r.Context(), req.SkillID)
	if err != nil || sk == nil {
		a.errorResp(w, http.StatusNotFound, "skill not found")
		return
	}

	if err := a.Services.Store.AssignSkillToAgent(agentID, req.SkillID, req.Config); err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}

	skills, err := a.Services.Store.ListAgentSkills(agentID)
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.jsonResp(w, http.StatusCreated, skills)
}

func (a *API) handleRemoveAgentSkill(w http.ResponseWriter, r *http.Request) {
	agentID := r.PathValue("id")
	skillID := r.PathValue("skillId")

	if err := a.Services.Store.RemoveSkillFromAgent(agentID, skillID); err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.jsonResp(w, http.StatusOK, map[string]string{"status": "removed"})
}

// handleGetDevMode returns whether the dev-mode editor affordances are
// active. E1 (CW-20260428-0016): the FE checks this on render to decide
// whether to surface the "edit internal skill" path.
func (a *API) handleGetDevMode(w http.ResponseWriter, r *http.Request) {
	a.jsonResp(w, http.StatusOK, map[string]any{
		"dev_mode": a.devModeEnabled(r),
		"env_flag": envDevModeOn(),
	})
}

// TASKS/skills/05: install/sync REST surface — the operator-facing trigger
// for task 04's explicit, single-target install/sync pipeline (internal/
// skillinstall.Installer), per docs/engineering/architecture/20-skills.md's
// "API surface" section: "Install/sync a skill package (by path or upload)
// into the vendored store + index; re-sync re-hashes and re-vendors on
// change." Local-path-only for this batch — upload support is deferred, see
// task 05's Work Log for why.
//
// A *skillinstall.Installer is deliberately never shared across requests —
// see service.Container.SkillVendor's doc comment. Both handlers below build
// a fresh one per call from the two stateless, concurrency-safe primitives
// the container does share: SkillVendor (the vendored store) and Store (the
// index, satisfying skillinstall.IndexStore directly).

// runSkillInstall builds a fresh *skillinstall.Installer and runs task 04's
// Parse -> Validate -> Vendor -> Index pipeline against path. The returned
// int is the HTTP status a caller should use when err is non-nil: a
// Parsing/Validating-step failure is the caller's malformed package (422
// Unprocessable Entity — this task's Done-means: "a malformed package
// produces a clear error response ... not a panic or an opaque 500"); a
// Vendoring/Indexing-step failure is a server-side/infra problem (500).
// Classification reads the Installer's own Emit stream rather than parsing
// error message text — Install's wrapped errors ("parse: %w", "validate:
// %w", ...) are for a human, not for control flow.
func (a *API) runSkillInstall(ctx context.Context, path string) (skillinstall.Result, int, error) {
	var lastState skillinstall.State
	installer := &skillinstall.Installer{
		Vendor: a.Services.SkillVendor,
		Index:  a.Services.Store,
		Emit: func(e skillinstall.Event) {
			if e.Err == nil {
				lastState = e.State
			}
		},
	}

	result, err := installer.Install(ctx, skillinstall.Source{Path: path})
	if err != nil {
		status := http.StatusInternalServerError
		switch lastState {
		case skillinstall.StateParsing, skillinstall.StateValidating:
			status = http.StatusUnprocessableEntity
		}
		return skillinstall.Result{}, status, err
	}
	return result, http.StatusOK, nil
}

func toInstallSkillResponse(r skillinstall.Result) InstallSkillResponse {
	return InstallSkillResponse{Skill: r.Skill, Address: r.Address, Reused: r.Reused}
}

// handleInstallSkill implements POST /api/skills/install: install (or
// re-sync, if the package's own frontmatter slug already matches an
// existing index row — task 04's Installer decides create-vs-update
// internally, keyed on the parsed package's slug, not a caller-supplied
// distinction) a skill package from a local directory path.
func (a *API) handleInstallSkill(w http.ResponseWriter, r *http.Request) {
	if a.Services.SkillVendor == nil {
		a.errorResp(w, http.StatusServiceUnavailable, "skill install/sync unavailable: vendor store not initialized")
		return
	}

	var req InstallSkillRequest
	if err := a.decode(r, &req); err != nil {
		a.errorResp(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if strings.TrimSpace(req.Path) == "" {
		a.errorResp(w, http.StatusBadRequest, "path is required")
		return
	}

	result, status, err := a.runSkillInstall(r.Context(), req.Path)
	if err != nil {
		a.errorResp(w, status, "skill install failed: "+err.Error())
		return
	}
	a.jsonResp(w, http.StatusCreated, toInstallSkillResponse(result))
}

// handleSyncSkill implements POST /api/skills/{slug}/sync: re-runs task 04's
// install pipeline against the same source path for an already-indexed
// skill. Requires the target slug to already exist in the index (404
// otherwise) and requires the freshly parsed package at path to declare that
// same slug in its own SKILL.md frontmatter (409 otherwise) — this endpoint
// re-syncs a known skill, it does not let a caller relabel one skill
// package's content onto a different skill's slug. The slug check parses
// the package (skill.ParsePackageDir — a pure read, no vendoring/indexing
// side effect) before ever calling the real Installer, so a mismatched sync
// target is rejected without mutating anything.
func (a *API) handleSyncSkill(w http.ResponseWriter, r *http.Request) {
	if a.Services.SkillVendor == nil {
		a.errorResp(w, http.StatusServiceUnavailable, "skill install/sync unavailable: vendor store not initialized")
		return
	}
	slug := r.PathValue("slug")

	existing, err := a.Services.Store.GetSkillBySlug(slug)
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	if existing == nil {
		a.errorResp(w, http.StatusNotFound, "skill not found: "+slug)
		return
	}

	var req InstallSkillRequest
	if err := a.decode(r, &req); err != nil {
		a.errorResp(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if strings.TrimSpace(req.Path) == "" {
		a.errorResp(w, http.StatusBadRequest, "path is required")
		return
	}

	def, _, err := skill.ParsePackageDir(req.Path)
	if err != nil {
		a.errorResp(w, http.StatusUnprocessableEntity, "skill sync failed: parse: "+err.Error())
		return
	}
	if def.Slug != slug {
		a.errorResp(w, http.StatusConflict, fmt.Sprintf(
			"package at %q declares slug %q, does not match sync target %q", req.Path, def.Slug, slug))
		return
	}

	result, status, err := a.runSkillInstall(r.Context(), req.Path)
	if err != nil {
		a.errorResp(w, status, "skill sync failed: "+err.Error())
		return
	}
	a.jsonResp(w, http.StatusOK, toInstallSkillResponse(result))
}

// --- TASKS/skills/12: grant/revoke + grants/policy view ---
//
// A deliberately separate route family from
// handleAssignAgentSkill/handleRemoveAgentSkill (above, POST/DELETE
// /api/agents/{id}/skills[/{skillId}]) and from agent_capabilities.go's
// known-skills CRUD (GET/POST/PUT/DELETE
// /api/agents/{id}/known-skills[/{skillName}]) rather than folding this
// action into either. Both alternatives were considered and rejected:
//
//   - Folding into AssignSkillToAgent would conflate "this row exists" with
//     "this skill is approved to execute with these capabilities" — exactly
//     the two concepts docs/engineering/architecture/20-skills.md's own API
//     surface section keeps as separate bullets ("Assign/revoke... distinct
//     from merely existing in the index" vs. "Grants/policy view").
//   - Folding into the known-skills PUT would mean adding grant-state
//     fields to AgentKnownSkillUpsertRequest, which task 02's own review
//     deliberately kept grant-state-free (see that struct's doc comment in
//     types.go) specifically so a plain pinned/ttl/reason Panel edit can
//     never silently wipe or forge a grant. Undoing that isolation here
//     would reopen exactly the hazard task 02 closed.
//
// A parallel /grant sub-route keeps this security-sensitive action behind
// its own explicit, single-purpose endpoint instead.
//
// Access-control note (flagged explicitly per this task's own instruction
// to note rather than silently assume it's fine): this codebase's REST API
// has exactly ONE caller-authentication mechanism gating the whole /api/
// surface — internal/server's optional, coarse HTTP Basic Auth
// (NANITE_AUTH_USER/PASSWORD; a no-op "local dev mode" when unset, per
// basicAuthMiddleware's own doc comment) — a single on/off switch for
// whether anyone unauthenticated may call at all, not anything specific to
// agent-mutating endpoints. No comparable agent-mutating endpoint in this
// codebase (internal/api/agent_capabilities.go, internal/api/agents.go)
// gates on caller identity either: every one of them, including
// requireMutableAgent (agent_capabilities.go, reused below for consistency
// with every sibling agent-mutating endpoint — known-tools, known-skills,
// procedures, knowledge seeds) — gates only on WHICH agent record may be
// mutated (a managed/editable profile vs. an embedded/plugin/vendor-owned
// one), never on WHO the caller is. This handler follows that same
// convention rather than inventing new access control that has no
// precedent among its siblings.
//
// Note: internal/server/caller_identity.go's callerIdentityMiddleware IS a
// real, wired-in caller-identity mechanism — internal/messaging's own authz
// checks (Inbox caller-match, Thread participant filter, Ack/Resolve
// recipient check, UnreadCount caller-match), consumed via
// internal/api/messaging.go's handlers, already rely on it. It is simply
// never applied to agent-mutation endpoints (this file,
// agent_capabilities.go, agents.go). So the accurate statement is not "no
// per-caller-identity access-control convention exists anywhere in this
// codebase" — it is "the one that exists was never extended to
// agent-mutating endpoints." The practical consequence is unchanged:
// granting a skill capability over REST is reachable by anything that can
// reach this process's HTTP port at all, exactly as every other
// agent-mutating endpoint already is.

// handleGrantAgentSkill implements POST /api/agents/{id}/skills/{slug}/grant
// — the real assign-with-approval action: ApprovedContentHash is always
// set to the skill's current vendored ContentHash (never a caller-supplied
// value — see AgentSkillGrantRequest's own doc comment), GrantedAt to now,
// GrantedBy/CapabilitiesGranted from the request. Any pre-existing
// telemetry/panel fields on the same agent_known_skills row (pinned,
// activation_count, last_used_at, added_at, ttl_seconds, reason — whether
// set by AssignSkillToAgent's bare assignment or the known-skills Panel)
// are carried forward unchanged, mirroring handleUpdateAgentKnownSkill's
// own "never silently wipe the other surface's data" convention.
func (a *API) handleGrantAgentSkill(w http.ResponseWriter, r *http.Request) {
	agent, ok := a.requireMutableAgent(w, r)
	if !ok {
		return
	}
	slug := r.PathValue("slug")

	sk, err := a.Services.Store.GetSkillBySlug(slug)
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	if sk == nil {
		a.errorResp(w, http.StatusNotFound, "skill not found: "+slug)
		return
	}
	if sk.ContentHash == "" {
		a.errorResp(w, http.StatusUnprocessableEntity,
			fmt.Sprintf("skill %q has not been installed/vendored yet — nothing to approve", slug))
		return
	}

	var req AgentSkillGrantRequest
	if err := a.decode(r, &req); err != nil {
		a.errorResp(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if strings.TrimSpace(req.GrantedBy) == "" {
		a.errorResp(w, http.StatusBadRequest, "granted_by is required")
		return
	}

	caps := skill.Capabilities{}
	if req.Capabilities != nil {
		caps = *req.Capabilities
	}
	capsJSON, err := json.Marshal(caps)
	if err != nil {
		a.errorResp(w, http.StatusBadRequest, "invalid capabilities: "+err.Error())
		return
	}

	current, err := a.Services.Store.GetAgentKnownSkill(r.Context(), agent.ID, sk.Slug)
	if err != nil && !errors.Is(err, store.ErrAgentKnownSkillNotFound) {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	row := store.AgentKnownSkill{
		AgentID:             agent.ID,
		SkillName:           sk.Slug,
		ApprovedContentHash: sk.ContentHash,
		GrantedAt:           time.Now().UTC().Format(time.RFC3339),
		GrantedBy:           req.GrantedBy,
		CapabilitiesGranted: string(capsJSON),
	}
	if current != nil {
		row.Pinned = current.Pinned
		row.ActivationCount = current.ActivationCount
		row.LastUsedAt = current.LastUsedAt
		row.AddedAt = current.AddedAt
		row.TTLSeconds = current.TTLSeconds
		row.Reason = current.Reason
	}
	if err := a.Services.Store.InsertAgentKnownSkill(r.Context(), row); err != nil {
		a.errorResp(w, http.StatusBadRequest, err.Error())
		return
	}

	updated, err := a.Services.Store.GetAgentKnownSkill(r.Context(), agent.ID, sk.Slug)
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.jsonResp(w, http.StatusCreated, updated)
}

// handleRevokeAgentSkillGrant implements
// DELETE /api/agents/{id}/skills/{slug}/grant — clears only the
// grant-state fields (ApprovedContentHash/GrantedAt/GrantedBy/
// CapabilitiesGranted), preserving the rest of the agent_known_skills row
// (pinned/activation_count/last_used_at/added_at/ttl_seconds/reason)
// exactly the way RemoveSkillFromAgent (skills.go, task 02) already
// preserves non-bare rows for the sibling assignment surface — revoking a
// capability grant is not a reason to also forget an agent's pin/telemetry
// state for a skill it may still legitimately be assigned (unapproved).
func (a *API) handleRevokeAgentSkillGrant(w http.ResponseWriter, r *http.Request) {
	agent, ok := a.requireMutableAgent(w, r)
	if !ok {
		return
	}
	slug := r.PathValue("slug")

	current, err := a.Services.Store.GetAgentKnownSkill(r.Context(), agent.ID, slug)
	if err != nil {
		if errors.Is(err, store.ErrAgentKnownSkillNotFound) {
			a.errorResp(w, http.StatusNotFound, "no grant exists for this agent/skill")
			return
		}
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	if current.ApprovedContentHash == "" && current.GrantedAt == "" &&
		current.GrantedBy == "" && current.CapabilitiesGranted == "" {
		a.errorResp(w, http.StatusNotFound, "no grant exists for this agent/skill")
		return
	}

	row := *current
	row.ApprovedContentHash = ""
	row.GrantedAt = ""
	row.GrantedBy = ""
	row.CapabilitiesGranted = ""
	if err := a.Services.Store.InsertAgentKnownSkill(r.Context(), row); err != nil {
		a.errorResp(w, http.StatusBadRequest, err.Error())
		return
	}
	a.jsonResp(w, http.StatusOK, map[string]string{"status": "revoked"})
}

// handleGetAgentSkillGrant implements
// GET /api/agents/{id}/skills/{slug}/grant — the grants/policy view.
// Reuses internal/skill.Gate.Authorize (tasks 09/11) for the actual
// "is this still valid" decision rather than re-deriving the check, per
// this task's own instruction — the exact same typed errors
// (GrantRequiredError / ReapprovalRequiredError) the real execution-time
// gate produces are what classify Status here, so an operator sees the
// same state a real skill_get/preview call would hit before it happens.
func (a *API) handleGetAgentSkillGrant(w http.ResponseWriter, r *http.Request) {
	agent, ok := a.requireAgent(w, r)
	if !ok {
		return
	}
	slug := r.PathValue("slug")

	sk, err := a.Services.Store.GetSkillBySlug(slug)
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	if sk == nil {
		a.errorResp(w, http.StatusNotFound, "skill not found: "+slug)
		return
	}

	view := AgentSkillGrantView{
		AgentID:            agent.ID,
		SkillSlug:          sk.Slug,
		CurrentContentHash: sk.ContentHash,
	}

	row, err := a.Services.Store.GetAgentKnownSkill(r.Context(), agent.ID, sk.Slug)
	if err != nil && !errors.Is(err, store.ErrAgentKnownSkillNotFound) {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	if row != nil {
		view.ApprovedContentHash = row.ApprovedContentHash
		view.GrantedAt = row.GrantedAt
		view.GrantedBy = row.GrantedBy
	}

	gate := skill.NewGate(a.Services.Store, a.Services.Store)
	_, authErr := gate.Authorize(r.Context(), sk.Slug, agent.ID)
	switch {
	case authErr == nil:
		view.Status = "approved"
		view.Message = "granted and approved against the skill's current content"
		if row != nil {
			caps, capErr := skill.ParseCapabilities(row.CapabilitiesGranted)
			if capErr == nil {
				view.Capabilities = &caps
			}
		}
	default:
		var grantRequired *skill.GrantRequiredError
		var reapproval *skill.ReapprovalRequiredError
		switch {
		case errors.As(authErr, &grantRequired):
			view.Status = "grant_required"
		case errors.As(authErr, &reapproval):
			view.Status = "reapproval_required"
		default:
			a.errorResp(w, http.StatusInternalServerError, authErr.Error())
			return
		}
		view.Message = authErr.Error()
	}

	a.jsonResp(w, http.StatusOK, view)
}

// handlePreviewSkill implements POST /api/skills/{slug}/preview —
// invoke/preview materialization outside a live agent turn, per
// docs/engineering/architecture/20-skills.md's "API surface" section: "run
// the Resolver/Materializer pipeline for a given skill + params outside of
// a live agent turn, so authoring/debugging doesn't require a real chat
// session."
//
// Design call (this task's own item 4 explicitly asks this be decided and
// documented, not left ambiguous): this endpoint does NOT bypass the grant
// check — it requires the exact same internal/skill.Gate.Authorize
// approval skill_get performs, against a caller-supplied AgentID (REST has
// no live agent turn to derive one from the way skill_get's ctx does).
// Reasoning:
//
//  1. The task's own wording is direct: "runs the same... pipeline... a
//     real skill_get call would" — read literally, that includes the
//     Policy/Sandbox stage, not just Resolver/Materializer.
//  2. This task's own Done-means dogfeed sequence orders grant BEFORE
//     preview ("install → grant → list... → preview materializes
//     correctly → ..."), directly confirming preview is meant to run
//     against an already-granted agent, not to bypass grants entirely.
//  3. Bypassing the gate would require either inventing a second, ungated
//     executor for real script/marker subprocess execution (recreating
//     exactly the "unsandboxed, ungated shell-out" flaw
//     docs/engineering/architecture/20-skills.md's "What's cut" section
//     names as fixed) or building a parallel GatedExecutor with a
//     different, weaker security posture — both worse than reusing Gate.
//  4. This app's REST API has no real caller-authentication by default
//     (see the access-control note above handleGrantAgentSkill) — the
//     grant check is the ONLY discrimination that exists between "an
//     operator debugging their own skill" and "anything else that can
//     reach this HTTP port." Removing it would make preview the single
//     least-restricted execution path in the whole skill system.
//
// The practical consequence: previewing a brand-new, never-granted skill
// requires first granting it to a real (possibly throwaway/test) agent via
// POST /api/agents/{id}/skills/{slug}/grant — which this same task is what
// newly makes possible over REST at all (previously only reachable by
// direct DB seeding, per tasks 10/11's own dogfeeds).
func (a *API) handlePreviewSkill(w http.ResponseWriter, r *http.Request) {
	if a.Services.SkillVendor == nil {
		a.errorResp(w, http.StatusServiceUnavailable, "skill preview unavailable: vendor store not initialized")
		return
	}
	slug := r.PathValue("slug")

	var req SkillPreviewRequest
	if err := a.decode(r, &req); err != nil {
		a.errorResp(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if strings.TrimSpace(req.AgentID) == "" {
		a.errorResp(w, http.StatusBadRequest, "agent_id is required")
		return
	}

	sk, err := a.Services.Store.GetSkillBySlug(slug)
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	if sk == nil {
		a.errorResp(w, http.StatusNotFound, "skill not found: "+slug)
		return
	}
	if !sk.Enabled {
		a.errorResp(w, http.StatusConflict, fmt.Sprintf("skill %q is disabled", slug))
		return
	}
	if sk.ContentHash == "" {
		a.errorResp(w, http.StatusUnprocessableEntity,
			fmt.Sprintf("skill %q has not been installed/vendored yet — nothing to preview", slug))
		return
	}

	// Step 1: the same unconditional, top-level grant check skill_get
	// performs — see this handler's own doc comment for why preview does
	// not bypass this.
	gate := skill.NewGate(a.Services.Store, a.Services.Store)
	if _, err := gate.Authorize(r.Context(), slug, req.AgentID); err != nil {
		status := http.StatusForbidden
		var reapproval *skill.ReapprovalRequiredError
		if errors.As(err, &reapproval) {
			status = http.StatusConflict
		}
		a.errorResp(w, status, err.Error())
		return
	}

	def, pkgDir, err := skill.LoadRootDefinition(a.Services.SkillVendor, sk)
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Step 2: Resolver + Materializer — the same internal/skill functions
	// (and the same MaterializerDeps shape) self_tools_skill_get.go's
	// callSkillGet calls for a real agent turn; see internal/skill/load.go
	// for LoadRootDefinition, the one piece those two callers literally
	// share rather than duplicate.
	deps := skill.MaterializerDeps{
		Resolvers: a.Services.Store,
		Index:     a.Services.Store,
		Vendor:    a.Services.SkillVendor,
		Subagent:  a.Services.Subagent,
	}
	input := skill.MaterializeInput{
		AgentID:            req.AgentID,
		StaticArgs:         req.Params,
		ParentAgentID:      req.AgentID,
		AgentProfileID:     req.AgentID,
		ForkRole:           req.ForkRole,
		ForkTimeoutSeconds: req.ForkTimeoutSeconds,
	}
	materialized, err := skill.MaterializeSkill(r.Context(), deps, *def, input)
	if err != nil {
		status := http.StatusUnprocessableEntity
		var pending *skill.ForkPendingApprovalError
		if errors.As(err, &pending) {
			status = http.StatusConflict
		}
		a.errorResp(w, status, fmt.Sprintf("materialize skill %q: %v", slug, err))
		return
	}

	// Step 3: marker execution, against the FINAL composed content — same
	// ordering/attribution rationale as self_tools_skill_get.go's own call
	// (see that file's comment above its own ResolveInlineMarkers call).
	resolved, err := skill.ResolveInlineMarkers(r.Context(), gate, *def, req.AgentID, pkgDir, materialized.Content, skill.ExecOptions{})
	if err != nil {
		a.errorResp(w, http.StatusUnprocessableEntity, fmt.Sprintf("resolve inline markers: %v", err))
		return
	}

	a.jsonResp(w, http.StatusOK, SkillPreviewResponse{Slug: slug, Content: resolved})
}

// TASKS/skills/02: handleForkSkillToUser (POST /api/skills/{id}/fork-to-user)
// and ForkSkillToUserRequest are deleted outright, forced by two independent
// facts: (1) store.Skill.Prompt is dropped in this same task (no more
// markdown body on this struct to fork from), and (2) this handler's own
// doc comment described a reload path ("the next discovery cycle... the J7
// AutoIngest pipeline overrides the DB row") that TASKS/skills/01 already
// fully deleted (skill.Discover always returns nil, AutoIngestSkills/
// upsertSkillDef no longer exist) — the feature was already non-functional
// before this task touched it, just not yet noticed. skill.WriteUserSkillFile
// itself is left untouched (internal/skill/loader.go) — TASKS/skills/01 kept
// it as an independently-useful primitive with its own test coverage
// (internal/skill/loader_test.go), out of this task's scope.
