package api

import (
	"net/http"

	"github.com/hollis-labs/nanite/internal/store"
)

// Consumers CRUD -- Phase 5 item 01 (TASKS/phase-5/01-build-assignment-
// api.md). Phase 1 item 03 (TASKS/phase-1/03-add-consumers-table.md) built
// the store-layer CRUD (internal/store/consumers.go) and deliberately
// deferred the REST layer to this task. Mirrors roles.go's shape exactly --
// consumers is a minimal id/slug/name tag with no cascade/behavior
// attached (see GLOSSARY.md's Consumer entry and
// docs/engineering/architecture/01-agent-construction.md's "Ownership and
// instancing" section).

// handleListConsumers returns all consumers.
// GET /api/consumers
func (a *API) handleListConsumers(w http.ResponseWriter, r *http.Request) {
	consumers, err := a.Services.Store.ListConsumers(r.Context())
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.jsonResp(w, http.StatusOK, consumers)
}

// handleCreateConsumer creates a new consumer.
// POST /api/consumers
func (a *API) handleCreateConsumer(w http.ResponseWriter, r *http.Request) {
	var req CreateConsumerRequest
	if err := a.decode(r, &req); err != nil {
		a.errorResp(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if req.Slug == "" || req.Name == "" {
		a.errorResp(w, http.StatusBadRequest, "slug and name are required")
		return
	}

	consumer := &store.Consumer{
		Slug: req.Slug,
		Name: req.Name,
	}
	if err := a.Services.Store.CreateConsumer(r.Context(), consumer); err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.jsonResp(w, http.StatusCreated, consumer)
}

// handleGetConsumer returns a single consumer by ID.
// GET /api/consumers/{id}
func (a *API) handleGetConsumer(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	consumer, err := a.Services.Store.GetConsumer(r.Context(), id)
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	if consumer == nil {
		a.errorResp(w, http.StatusNotFound, "consumer not found")
		return
	}
	a.jsonResp(w, http.StatusOK, consumer)
}

// handleUpdateConsumer updates a consumer's mutable fields.
// PUT /api/consumers/{id}
func (a *API) handleUpdateConsumer(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	existing, err := a.Services.Store.GetConsumer(r.Context(), id)
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	if existing == nil {
		a.errorResp(w, http.StatusNotFound, "consumer not found")
		return
	}

	var req UpdateConsumerRequest
	if err := a.decode(r, &req); err != nil {
		a.errorResp(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}

	if req.Slug != nil {
		existing.Slug = *req.Slug
	}
	if req.Name != nil {
		existing.Name = *req.Name
	}

	if err := a.Services.Store.UpdateConsumer(r.Context(), existing); err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.jsonResp(w, http.StatusOK, existing)
}

// handleDeleteConsumer deletes a consumer by ID. Fails with a wrapped FK
// constraint error (surfaced as 500 here, matching roles.go's precedent for
// store-level validation/constraint errors) if any agent_profiles row still
// references it -- see store.DeleteConsumer's doc comment.
// DELETE /api/consumers/{id}
func (a *API) handleDeleteConsumer(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := a.Services.Store.DeleteConsumer(r.Context(), id); err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.jsonResp(w, http.StatusOK, map[string]string{"status": "deleted"})
}
