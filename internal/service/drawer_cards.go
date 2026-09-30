package service

import (
	"context"

	"github.com/hollis-labs/nanite/internal/store"
)

// DrawerCardStore is the store surface DrawerCardService uses.
type DrawerCardStore interface {
	ListBottomDrawerPinnedCards(ctx context.Context, sessionID string) ([]store.BottomDrawerPinnedCard, error)
	PinBottomDrawerCard(ctx context.Context, card *store.BottomDrawerPinnedCard) error
	UnpinBottomDrawerCard(ctx context.Context, id string) error
}

// DrawerCardService is the transport-facing home for a session's pinned
// bottom-drawer cards. It is a pass-through: the store enforces the pin cap
// and reports it as store.ErrBottomDrawerPinCapExceeded.
type DrawerCardService struct {
	store DrawerCardStore
}

func NewDrawerCardService(st DrawerCardStore) *DrawerCardService {
	return &DrawerCardService{store: st}
}

// List returns the session's pinned cards in display order.
func (s *DrawerCardService) List(ctx context.Context, sessionID string) ([]store.BottomDrawerPinnedCard, error) {
	return s.store.ListBottomDrawerPinnedCards(ctx, sessionID)
}

// Pin adds a card; the store fills in its ID, position and timestamp.
func (s *DrawerCardService) Pin(ctx context.Context, card *store.BottomDrawerPinnedCard) error {
	return s.store.PinBottomDrawerCard(ctx, card)
}

// Unpin removes a card.
func (s *DrawerCardService) Unpin(ctx context.Context, id string) error {
	return s.store.UnpinBottomDrawerCard(ctx, id)
}
