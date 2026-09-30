package service

import (
	"context"

	"github.com/hollis-labs/nanite/internal/store"
)

// RoleStore is the store surface RoleService reads and writes.
type RoleStore interface {
	ListRoles(ctx context.Context) ([]store.Role, error)
	GetRole(ctx context.Context, id string) (*store.Role, error)
	CreateRole(ctx context.Context, r *store.Role) error
	UpdateRole(ctx context.Context, r *store.Role) error
	DeleteRole(ctx context.Context, id string) error
}

// RoleService is the transport-facing home for role rows. It is a
// pass-through: the store validates role fields (the default_class enum) on
// create and update, and returns its errors unwrapped.
type RoleService struct {
	store RoleStore
}

func NewRoleService(st RoleStore) *RoleService {
	return &RoleService{store: st}
}

// List returns every role.
func (s *RoleService) List(ctx context.Context) ([]store.Role, error) {
	return s.store.ListRoles(ctx)
}

// Get returns the role, or nil when there is none.
func (s *RoleService) Get(ctx context.Context, id string) (*store.Role, error) {
	return s.store.GetRole(ctx, id)
}

// Create inserts a role; the store fills in its ID and timestamps.
func (s *RoleService) Create(ctx context.Context, r *store.Role) error {
	return s.store.CreateRole(ctx, r)
}

// Update writes every field of r.
func (s *RoleService) Update(ctx context.Context, r *store.Role) error {
	return s.store.UpdateRole(ctx, r)
}

// Delete removes a role. A missing role is an error from the store.
func (s *RoleService) Delete(ctx context.Context, id string) error {
	return s.store.DeleteRole(ctx, id)
}
