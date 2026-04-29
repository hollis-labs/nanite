package api

import (
	"errors"
	"net/http"
	"strings"

	"github.com/hollis-labs/nanite/internal/store"
)

// --- Bottom drawer pinned cards (C1, CW-20260428-0012) ---
//
// Persists the user's "pinned tab" lifecycle for the bottom chat drawer.
// Distinct from `pinned_content` (J11) which feeds the agent context slot.

type pinDrawerCardRequest struct {
	CardType   string `json:"card_type"`
	ContentRef string `json:"content_ref"`
	Title      string `json:"title"`
	Payload    string `json:"payload"` // optional JSON blob (envelope snapshot etc.)
}

// handleListBottomDrawerCards — GET /api/sessions/{id}/drawer-cards
func (a *API) handleListBottomDrawerCards(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("id")
	cards, err := a.Services.Store.ListBottomDrawerPinnedCards(sessionID)
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, "failed to list drawer cards: "+err.Error())
		return
	}
	if cards == nil {
		cards = []store.BottomDrawerPinnedCard{}
	}
	a.jsonResp(w, http.StatusOK, cards)
}

// handlePinBottomDrawerCard — POST /api/sessions/{id}/drawer-cards
// Returns 409 Conflict when the 10-pin cap is exceeded so the FE can render
// the user-facing "10-tab limit; unpin one first" toast.
func (a *API) handlePinBottomDrawerCard(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("id")

	var req pinDrawerCardRequest
	if err := a.decode(r, &req); err != nil {
		a.errorResp(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if strings.TrimSpace(req.CardType) == "" {
		a.errorResp(w, http.StatusBadRequest, "card_type is required")
		return
	}

	card := &store.BottomDrawerPinnedCard{
		SessionID:  sessionID,
		CardType:   req.CardType,
		ContentRef: req.ContentRef,
		Title:      req.Title,
		Payload:    req.Payload,
	}
	err := a.Services.Store.PinBottomDrawerCard(card)
	if errors.Is(err, store.ErrBottomDrawerPinCapExceeded) {
		a.errorResp(w, http.StatusConflict, "pin cap exceeded; unpin one first")
		return
	}
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, "failed to pin drawer card: "+err.Error())
		return
	}
	a.jsonResp(w, http.StatusCreated, card)
}

// handleUnpinBottomDrawerCard — DELETE /api/drawer-cards/{id}
func (a *API) handleUnpinBottomDrawerCard(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := a.Services.Store.UnpinBottomDrawerCard(id); err != nil {
		a.errorResp(w, http.StatusInternalServerError, "failed to unpin drawer card: "+err.Error())
		return
	}
	a.jsonResp(w, http.StatusOK, map[string]any{"deleted": true, "id": id})
}
