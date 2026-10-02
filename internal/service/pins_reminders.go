package service

import (
	"context"

	"github.com/hollis-labs/nanite/internal/store"
)

// PinStore is the store surface PinService uses.
type PinStore interface {
	CreatePinnedContent(context.Context, store.PinnedContent) error
	ListPinnedContent(ctx context.Context, sessionID string) ([]store.PinnedContent, error)
	DeletePinnedContent(ctx context.Context, id string) error
	UpdatePinScope(ctx context.Context, id, scope, projectID string) error
}

// PinService is the transport-facing home for pinned content (the items fed
// into an agent's pinned-context slot). It is a pass-through: the store
// validates scope changes and returns its errors unwrapped.
type PinService struct {
	store PinStore
}

func NewPinService(st PinStore) *PinService { return &PinService{store: st} }

// List returns the pins visible to a session: its own and cross-session
// ones.
func (s *PinService) List(ctx context.Context, sessionID string) ([]store.PinnedContent, error) {
	return s.store.ListPinnedContent(ctx, sessionID)
}

// Delete removes a pin.
func (s *PinService) Delete(ctx context.Context, id string) error {
	return s.store.DeletePinnedContent(ctx, id)
}

// UpdateScope moves a pin between session and project scope.
func (s *PinService) UpdateScope(ctx context.Context, id, scope, projectID string) error {
	return s.store.UpdatePinScope(ctx, id, scope, projectID)
}

// ReminderService is the transport-facing home for reminder rows, over the
// existing ReminderStore; firing them is reminders.Engine's. It is a pass-through: the store validates
// scope changes and returns its errors unwrapped.
type ReminderService struct {
	store ReminderStore
}

func NewReminderService(st ReminderStore) *ReminderService { return &ReminderService{store: st} }

// ListUnfired returns the unfired reminders visible to a session, including
// project-scoped ones for the session's project.
func (s *ReminderService) ListUnfired(ctx context.Context, sessionID string) ([]store.Reminder, error) {
	return s.store.ListUnfiredReminders(ctx, sessionID)
}

// Delete removes a reminder.
func (s *ReminderService) Delete(ctx context.Context, id string) error {
	return s.store.DeleteReminder(ctx, id)
}

// UpdateScope moves a reminder between session and project scope.
func (s *ReminderService) UpdateScope(ctx context.Context, id, scope, projectID string) error {
	return s.store.UpdateReminderScope(ctx, id, scope, projectID)
}

// Get returns one reminder.
func (s *ReminderService) Get(ctx context.Context, id string) (store.Reminder, error) {
	return s.store.GetReminder(ctx, id)
}

// Create persists an already-scoped pin from the self-tools.
func (s *PinService) Create(ctx context.Context, row store.PinnedContent) error {
	return s.store.CreatePinnedContent(ctx, row)
}

// Create persists a reminder after its trigger has been validated.
func (s *ReminderService) Create(ctx context.Context, row store.Reminder) error {
	return s.store.CreateReminder(ctx, row)
}
