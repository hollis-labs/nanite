package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/hollis-labs/nanite/internal/agentdefs"
	"github.com/hollis-labs/nanite/internal/agentpolicy"
	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/substrate/mesh/agentdef"
)

// StoredDefinitionResolver resolves installed immutable artifacts. The embedded
// revision remains an exact authored artifact, including for audited historical
// retirement; neither a mutable profile nor a label-only fallback is consulted.
type StoredDefinitionResolver struct{ Store *store.Store }

func (r *StoredDefinitionResolver) Resolve(ctx context.Context, pin DefinitionRef) (VerifiedDefinition, error) {
	if err := pin.Validate(); err != nil {
		return VerifiedDefinition{}, err
	}
	a, err := r.Store.GetAgentDefinitionArtifact(ctx, pin.MeshRef())
	if errors.Is(err, sql.ErrNoRows) {
		return VerifiedDefinition{}, ErrDefinitionNotFound
	}
	if errors.Is(err, store.ErrDefinitionContent) {
		return VerifiedDefinition{}, ErrDefinitionDigestMismatch
	}
	if err != nil {
		return VerifiedDefinition{}, err
	}
	d, err := agentdef.Parse(a.Data, agentpolicy.Option())
	if err != nil {
		return VerifiedDefinition{}, err
	}
	digest, err := agentdef.Digest(d)
	if err != nil {
		return VerifiedDefinition{}, err
	}
	if d.DefinitionID != pin.DefinitionID || d.Revision != pin.Revision || digest != pin.SemanticDigest {
		return VerifiedDefinition{}, ErrDefinitionDigestMismatch
	}
	return VerifiedDefinition{Ref: pin, Definition: d, ReadResource: func(ctx context.Context, ref agentdef.Ref) ([]byte, error) {
		return r.Store.GetAgentDefinitionResource(ctx, pin.MeshRef(), ref)
	}}, nil
}

func installEmbeddedChatDefinition(ctx context.Context, st *store.Store) error {
	_, err := st.InstallAgentDefinition(ctx, embeddedChatDefinition, nil)
	if err != nil {
		return fmt.Errorf("install authored embedded definition: %w", err)
	}
	catalog, err := agentdefs.Catalog()
	if err != nil {
		return err
	}
	for _, artifact := range catalog {
		resources := make([]store.DefinitionResource, 0, len(artifact.Resources))
		for _, r := range artifact.Resources {
			resources = append(resources, store.DefinitionResource{URI: r.URI, Content: r.Content})
		}
		if _, err = st.InstallAgentDefinition(ctx, artifact.Data, resources); err != nil {
			return err
		}
	}
	return nil
}
