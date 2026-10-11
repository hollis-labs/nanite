package main

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/hollis-labs/nanite/internal/apiauth"
	"github.com/hollis-labs/nanite/internal/service"
)

// credentialStanding answers, from the credential store on every call, whether
// the credential that submitted a native turn is still valid and may run tools.
// It never caches: revoking the credential refuses the turn's next dispatch.
type credentialStanding struct {
	store *apiauth.Store
	now   func() time.Time
}

func (c credentialStanding) CallerStanding(ctx context.Context, credentialID string) (service.CallerStanding, error) {
	cred, err := c.store.Get(ctx, credentialID)
	if errors.Is(err, sql.ErrNoRows) {
		return service.CallerStanding{}, nil // unknown to the store: not valid
	}
	if err != nil {
		return service.CallerStanding{}, err // lookup failure: the caller fails closed
	}
	now := time.Now
	if c.now != nil {
		now = c.now
	}
	valid := cred.RevokedAt.IsZero() && (cred.ExpiresAt.IsZero() || cred.ExpiresAt.After(now()))
	return service.CallerStanding{Valid: valid, Tools: valid && cred.Has(apiauth.ScopeTools)}, nil
}
