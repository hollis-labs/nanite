package messaging

import (
	"context"
	"database/sql"
	"fmt"
	"testing"

	"github.com/hollis-labs/nanite/internal/store"
)

// fakeResolver is a test double for AgentResolver. It returns a
// synthetic profile for known IDs and sql.ErrNoRows for anything
// else — mirroring the real AgentService.Get behavior, which wraps
// sql.ErrNoRows on missing. The errors.Is-based not-found
// distinction in maybeAutoRegister depends on this shape.
type fakeResolver struct {
	known map[string]bool
}

func (f *fakeResolver) Get(_ context.Context, id string) (*store.AgentProfile, error) {
	if f.known[id] {
		return &store.AgentProfile{ID: id}, nil
	}
	return nil, fmt.Errorf("agent %s: %w", id, sql.ErrNoRows)
}

func newFakeResolver(ids ...string) *fakeResolver {
	m := make(map[string]bool, len(ids))
	for _, id := range ids {
		m[id] = true
	}
	return &fakeResolver{known: m}
}

func TestValidateAgentID_User(t *testing.T) {
	// The "user" sentinel is always valid — resolver not consulted.
	if err := ValidateAgentID(context.Background(), nil, UserSentinel); err != nil {
		t.Errorf("user sentinel rejected: %v", err)
	}
}

func TestValidateAgentID_FileSlug(t *testing.T) {
	r := newFakeResolver("file-backend")
	if err := ValidateAgentID(context.Background(), r, "file-backend"); err != nil {
		t.Errorf("file-backend rejected: %v", err)
	}
	if err := ValidateAgentID(context.Background(), r, "file-does-not-exist"); err == nil {
		t.Error("expected rejection for unknown file agent")
	}
}

func TestValidateAgentID_DBUUID(t *testing.T) {
	const uuid = "11111111-1111-1111-1111-111111111111"
	r := newFakeResolver(uuid)
	if err := ValidateAgentID(context.Background(), r, uuid); err != nil {
		t.Errorf("DB agent rejected: %v", err)
	}
	if err := ValidateAgentID(context.Background(), r, "ffffffff-ffff-ffff-ffff-ffffffffffff"); err == nil {
		t.Error("expected rejection for unknown UUID")
	}
}

func TestValidateAgentID_Empty(t *testing.T) {
	if err := ValidateAgentID(context.Background(), nil, ""); err == nil {
		t.Error("expected rejection for empty agent id")
	}
}

func TestValidateAgentID_NilResolver(t *testing.T) {
	// Anything other than the user sentinel requires a resolver.
	if err := ValidateAgentID(context.Background(), nil, "file-backend"); err == nil {
		t.Error("expected rejection when resolver is nil and id is not the user sentinel")
	}
}
