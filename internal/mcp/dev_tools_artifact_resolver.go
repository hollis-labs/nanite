// Package mcp — dev_tools artifact resolver adapter.
//
// SP-20260512-0008 W2C (CW-20260512-0110): bridges the internal/store
// artifact rows to the ArtifactResolver shape dev_tools needs. Lives in
// internal/mcp (not internal/store) so internal/store remains agnostic
// of mcp's interface vocabulary — store is the substrate; mcp is a
// consumer that adapts to the substrate's data shape.
package mcp

import (
	"context"

	"github.com/hollis-labs/nanite/internal/store"
)

// StoreArtifactResolver adapts *store.Store to the ArtifactResolver
// interface dev_read's stash-pointer entrypoint expects. Constructed
// via NewStoreArtifactResolver.
type StoreArtifactResolver struct {
	store *store.Store
}

// NewStoreArtifactResolver wraps the given store as an ArtifactResolver.
// Returns nil when s is nil so callers can pass it straight through to
// WithArtifactResolver without nil-checks.
func NewStoreArtifactResolver(s *store.Store) *StoreArtifactResolver {
	if s == nil {
		return nil
	}
	return &StoreArtifactResolver{store: s}
}

// GetArtifact looks up an artifact by ID and projects it into the small
// ArtifactMeta shape dev_tools consumes. Returns (nil, error) when the
// artifact is not found or the store backend errors — dev_read surfaces
// either as a tool-level error to the agent.
func (r *StoreArtifactResolver) GetArtifact(id string) (*ArtifactMeta, error) {
	if r == nil || r.store == nil {
		return nil, errResolverUnconfigured
	}
	a, err := r.store.GetArtifact(context.TODO() /* TODO(ctx-sweep): no ctx available at this call site */, id)
	if err != nil {
		return nil, err
	}
	if a == nil {
		return nil, errArtifactNotFound
	}
	return &ArtifactMeta{
		ID:          a.ID,
		StoragePath: a.StoragePath,
		MimeType:    a.MimeType,
		SizeBytes:   a.SizeBytes,
	}, nil
}
