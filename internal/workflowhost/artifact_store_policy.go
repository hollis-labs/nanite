package workflowhost

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"

	"github.com/hollis-labs/go-workflow/values"
)

// ArtifactAccessAuthorizer is Nanite's strict default artifact ownership
// policy. A principal can access a run/project artifact only with the exact
// corresponding claim. Global cleanup is reserved for the maintenance
// principal ("system" by default).
type ArtifactAccessAuthorizer struct {
	MaintenancePrincipal string
}

func (authorizer ArtifactAccessAuthorizer) AuthorizeArtifact(_ context.Context, request values.ArtifactAuthorization) error {
	if err := request.Access.Validate(request.Operation); err != nil {
		return err
	}
	maintenance := authorizer.MaintenancePrincipal
	if maintenance == "" {
		maintenance = "system"
	}
	if request.Access.Principal == maintenance {
		return nil
	}
	if request.Owner != nil {
		switch request.Owner.Scope {
		case values.ArtifactOwnerRun:
			if request.Access.RunID != request.Owner.ID {
				return values.ErrArtifactUnauthorized
			}
		case values.ArtifactOwnerProject:
			if request.Access.ProjectID != request.Owner.ID {
				return values.ErrArtifactUnauthorized
			}
		default:
			return values.ErrArtifactUnauthorized
		}
		return nil
	}
	if request.Ref != nil && request.Ref.Store == ArtifactStoreName {
		locator, err := parseArtifactRef(*request.Ref)
		if err != nil {
			return values.ErrArtifactUnauthorized
		}
		switch locator.scope {
		case values.ArtifactOwnerRun:
			if request.Access.RunID == "" || artifactOwnerHash(request.Access.RunID) != locator.ownerHash {
				return values.ErrArtifactUnauthorized
			}
		case values.ArtifactOwnerProject:
			if request.Access.ProjectID == "" || artifactOwnerHash(request.Access.ProjectID) != locator.ownerHash {
				return values.ErrArtifactUnauthorized
			}
		default:
			return values.ErrArtifactUnauthorized
		}
		return nil
	}
	if request.Operation == values.ArtifactOperationCleanup {
		return values.ErrArtifactUnauthorized
	}
	return nil
}

func (store *ArtifactStore) authorize(ctx context.Context, operation values.ArtifactOperation, access values.ArtifactAccess, ref *values.ArtifactRef, owner *values.ArtifactOwner) error {
	if err := checkArtifactContext(ctx, operation, ref); err != nil {
		return err
	}
	if err := access.Validate(operation); err != nil {
		return err
	}
	var refCopy *values.ArtifactRef
	if ref != nil {
		copyValue := *ref
		refCopy = &copyValue
	}
	var ownerCopy *values.ArtifactOwner
	if owner != nil {
		copyValue := *owner
		ownerCopy = &copyValue
	}
	if err := store.authorizer.AuthorizeArtifact(ctx, values.ArtifactAuthorization{
		Operation: operation,
		Access:    access,
		Ref:       refCopy,
		Owner:     ownerCopy,
	}); err != nil {
		return artifactError(operation, values.ArtifactFailureUnauthorized, ref, err)
	}
	return nil
}

func (store *ArtifactStore) artifactDirectory(locator artifactLocator) string {
	return filepath.Join(store.root, "objects", string(locator.scope), locator.ownerHash, locator.artifactID)
}

func (store *ArtifactStore) validateRoots() error {
	rootInfo, err := os.Lstat(store.root)
	if err != nil || !rootInfo.IsDir() || rootInfo.Mode()&os.ModeSymlink != 0 || !os.SameFile(store.rootIdentity, rootInfo) {
		return values.ErrArtifactInvalid
	}
	for _, entry := range []struct {
		name     string
		identity os.FileInfo
	}{
		{name: "objects", identity: store.objectsIdentity},
		{name: "staging", identity: store.stagingIdentity},
	} {
		info, err := os.Lstat(filepath.Join(store.root, entry.name))
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || !os.SameFile(entry.identity, info) {
			return values.ErrArtifactInvalid
		}
	}
	return nil
}

func verifyArtifactPayload(ctx context.Context, operation values.ArtifactOperation, file *os.File, ref values.ArtifactRef) error {
	hasher := sha256.New()
	size, err := io.Copy(hasher, artifactContextReader{ctx: ctx, reader: file})
	if err != nil {
		return artifactError(operation, values.ArtifactFailureInvalid, &ref, err)
	}
	actual := "sha256:" + hex.EncodeToString(hasher.Sum(nil))
	if size != ref.SizeBytes || actual != ref.Digest {
		return artifactError(operation, values.ArtifactFailureDigest, &ref, values.ErrArtifactDigest)
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return artifactError(operation, values.ArtifactFailureInvalid, &ref, err)
	}
	return nil
}

func snapshotArtifactPutRequest(request values.ArtifactPutRequest) values.ArtifactPutRequest {
	if request.ExpectedSize != nil {
		expected := *request.ExpectedSize
		request.ExpectedSize = &expected
	}
	return request
}

func checkArtifactContext(ctx context.Context, operation values.ArtifactOperation, ref *values.ArtifactRef) error {
	if ctx == nil {
		return artifactError(operation, values.ArtifactFailureInvalid, ref, values.ErrArtifactInvalid)
	}
	if err := ctx.Err(); err != nil {
		return artifactError(operation, values.ArtifactFailureInvalid, ref, err)
	}
	return nil
}

func artifactOperationError(operation values.ArtifactOperation, ref values.ArtifactRef, cause error) error {
	return artifactError(operation, artifactFailure(cause), &ref, cause)
}

func artifactError(operation values.ArtifactOperation, failure values.ArtifactFailure, ref *values.ArtifactRef, cause error) error {
	return values.NewArtifactError(operation, failure, ref, cause)
}

func artifactFailure(cause error) values.ArtifactFailure {
	switch {
	case errors.Is(cause, values.ErrArtifactAuthority):
		return values.ArtifactFailureAuthority
	case errors.Is(cause, values.ErrArtifactUnauthorized):
		return values.ArtifactFailureUnauthorized
	case errors.Is(cause, os.ErrNotExist), errors.Is(cause, values.ErrArtifactNotFound):
		return values.ArtifactFailureNotFound
	case errors.Is(cause, values.ErrArtifactDigest):
		return values.ArtifactFailureDigest
	case errors.Is(cause, values.ErrArtifactExpired):
		return values.ArtifactFailureExpired
	case errors.Is(cause, values.ErrArtifactSizeLimit):
		return values.ArtifactFailureSize
	case errors.Is(cause, values.ErrArtifactRetention):
		return values.ArtifactFailureRetention
	default:
		return values.ArtifactFailureInvalid
	}
}

var (
	_ values.ArtifactStore      = (*ArtifactStore)(nil)
	_ values.ArtifactAuthorizer = ArtifactAccessAuthorizer{}
)
