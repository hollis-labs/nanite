package workflowhost

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/hollis-labs/go-workflow/values"
)

// Delete removes one exact local reference. Repeated deletion is successful
// and reports already_absent.
func (store *ArtifactStore) Delete(ctx context.Context, request values.ArtifactDeleteRequest) (values.ArtifactCleanupResult, error) {
	if err := checkArtifactContext(ctx, values.ArtifactOperationDelete, &request.Ref); err != nil {
		return values.ArtifactCleanupResult{}, err
	}
	if err := request.Validate(); err != nil {
		return values.ArtifactCleanupResult{}, err
	}
	if request.Ref.Store == ArtifactStoreName {
		if err := validateArtifactRef(request.Ref); err != nil {
			return values.ArtifactCleanupResult{}, artifactOperationError(values.ArtifactOperationDelete, request.Ref, err)
		}
	} else if request.Ref.Retention != values.RetentionExternal {
		return values.ArtifactCleanupResult{}, artifactError(values.ArtifactOperationDelete, values.ArtifactFailureAuthority, &request.Ref, values.ErrArtifactAuthority)
	}
	if err := store.authorize(ctx, values.ArtifactOperationDelete, request.Access, &request.Ref, nil); err != nil {
		return values.ArtifactCleanupResult{}, err
	}
	if request.Ref.Store != ArtifactStoreName {
		return values.ArtifactCleanupResult{Outcome: values.ArtifactCleanupPreservedExternal}, nil
	}
	if err := store.validateRoots(); err != nil {
		return values.ArtifactCleanupResult{}, artifactOperationError(values.ArtifactOperationDelete, request.Ref, err)
	}
	locator, _ := parseArtifactRef(request.Ref)
	directory := store.artifactDirectory(locator)
	if pathErr := rejectArtifactSymlinkComponents(directory); pathErr != nil {
		return values.ArtifactCleanupResult{}, artifactOperationError(values.ArtifactOperationDelete, request.Ref, pathErr)
	}
	metadata, err := verifyStoredArtifact(directory, locator, &request.Ref)
	if errors.Is(err, os.ErrNotExist) {
		return values.ArtifactCleanupResult{Outcome: values.ArtifactCleanupAlreadyAbsent}, nil
	}
	if err != nil {
		return values.ArtifactCleanupResult{}, artifactOperationError(values.ArtifactOperationDelete, request.Ref, err)
	}
	if err := store.authorize(ctx, values.ArtifactOperationDelete, request.Access, &request.Ref, &metadata.Owner); err != nil {
		return values.ArtifactCleanupResult{}, err
	}
	if err := checkArtifactContext(ctx, values.ArtifactOperationDelete, &request.Ref); err != nil {
		return values.ArtifactCleanupResult{}, err
	}
	if pathErr := rejectArtifactSymlinkComponents(directory); pathErr != nil {
		return values.ArtifactCleanupResult{}, artifactOperationError(values.ArtifactOperationDelete, request.Ref, pathErr)
	}
	if err := os.RemoveAll(directory); err != nil {
		return values.ArtifactCleanupResult{}, artifactOperationError(values.ArtifactOperationDelete, request.Ref, err)
	}
	if err := syncArtifactDirectory(filepath.Dir(directory)); err != nil {
		return values.ArtifactCleanupResult{}, artifactOperationError(values.ArtifactOperationDelete, request.Ref, err)
	}
	return values.ArtifactCleanupResult{Outcome: values.ArtifactCleanupDeleted, DeletedCount: 1}, nil
}

// Cleanup applies go-workflow's owner, expiry, partial, none, and external cleanup
// boundaries. Nanite does not own external bytes and never deletes them.
func (store *ArtifactStore) Cleanup(ctx context.Context, request values.ArtifactCleanupRequest) (values.ArtifactCleanupResult, error) {
	if err := checkArtifactContext(ctx, values.ArtifactOperationCleanup, request.Ref); err != nil {
		return values.ArtifactCleanupResult{}, err
	}
	if err := request.Validate(); err != nil {
		return values.ArtifactCleanupResult{}, err
	}
	if request.Ref != nil {
		if request.Kind == values.ArtifactCleanupExternal {
			if err := request.Ref.Validate(); err != nil {
				return values.ArtifactCleanupResult{}, artifactError(values.ArtifactOperationCleanup, values.ArtifactFailureInvalid, request.Ref, err)
			}
		} else if err := validateArtifactRef(*request.Ref); err != nil {
			return values.ArtifactCleanupResult{}, artifactOperationError(values.ArtifactOperationCleanup, *request.Ref, err)
		}
	}
	if err := store.authorize(ctx, values.ArtifactOperationCleanup, request.Access, request.Ref, artifactCleanupOwner(request)); err != nil {
		return values.ArtifactCleanupResult{}, err
	}
	switch request.Kind {
	case values.ArtifactCleanupNone:
		return values.ArtifactCleanupResult{Outcome: values.ArtifactCleanupNotStored}, nil
	case values.ArtifactCleanupExternal:
		return values.ArtifactCleanupResult{Outcome: values.ArtifactCleanupPreservedExternal}, nil
	case values.ArtifactCleanupPartials:
		if err := store.validateRoots(); err != nil {
			return values.ArtifactCleanupResult{}, artifactError(values.ArtifactOperationCleanup, values.ArtifactFailureInvalid, nil, err)
		}
		return store.cleanupPartials(ctx, request.Before)
	case values.ArtifactCleanupRun, values.ArtifactCleanupProject:
		if err := store.validateRoots(); err != nil {
			return values.ArtifactCleanupResult{}, artifactError(values.ArtifactOperationCleanup, values.ArtifactFailureInvalid, nil, err)
		}
		return store.cleanupOwner(ctx, request)
	case values.ArtifactCleanupExpired:
		if err := store.validateRoots(); err != nil {
			return values.ArtifactCleanupResult{}, artifactError(values.ArtifactOperationCleanup, values.ArtifactFailureInvalid, nil, err)
		}
		return store.cleanupExpired(ctx, request)
	default:
		return values.ArtifactCleanupResult{}, artifactError(values.ArtifactOperationCleanup, values.ArtifactFailureInvalid, request.Ref, values.ErrArtifactInvalid)
	}
}

type artifactCandidate struct {
	path     string
	metadata values.ArtifactMetadata
}

func (store *ArtifactStore) cleanupOwner(ctx context.Context, request values.ArtifactCleanupRequest) (values.ArtifactCleanupResult, error) {
	ownerDirectory := filepath.Join(store.root, "objects", string(request.Owner.Scope), artifactOwnerHash(request.Owner.ID))
	if pathErr := rejectArtifactSymlinkComponents(ownerDirectory); pathErr != nil {
		return values.ArtifactCleanupResult{}, artifactError(values.ArtifactOperationCleanup, values.ArtifactFailureInvalid, nil, pathErr)
	}
	candidates, err := store.collectOwnerArtifacts(ctx, ownerDirectory)
	if errors.Is(err, os.ErrNotExist) {
		return values.ArtifactCleanupResult{Outcome: values.ArtifactCleanupAlreadyAbsent}, nil
	}
	if err != nil {
		return values.ArtifactCleanupResult{}, artifactError(values.ArtifactOperationCleanup, values.ArtifactFailureInvalid, nil, err)
	}
	for _, candidate := range candidates {
		if err := checkArtifactContext(ctx, values.ArtifactOperationCleanup, &candidate.metadata.Ref); err != nil {
			return values.ArtifactCleanupResult{}, err
		}
		if candidate.metadata.Owner != request.Owner {
			return values.ArtifactCleanupResult{}, artifactError(values.ArtifactOperationCleanup, values.ArtifactFailureInvalid, &candidate.metadata.Ref, values.ErrArtifactInvalid)
		}
		if err := store.authorize(ctx, values.ArtifactOperationCleanup, request.Access, &candidate.metadata.Ref, &candidate.metadata.Owner); err != nil {
			return values.ArtifactCleanupResult{}, err
		}
	}
	return removeArtifactCandidates(ctx, candidates, values.ArtifactCleanupAlreadyAbsent)
}

func (store *ArtifactStore) cleanupExpired(ctx context.Context, request values.ArtifactCleanupRequest) (values.ArtifactCleanupResult, error) {
	var candidates []artifactCandidate
	for _, scope := range []values.ArtifactOwnerScope{values.ArtifactOwnerRun, values.ArtifactOwnerProject} {
		if err := checkArtifactContext(ctx, values.ArtifactOperationCleanup, nil); err != nil {
			return values.ArtifactCleanupResult{}, err
		}
		scopeRoot := filepath.Join(store.root, "objects", string(scope))
		if pathErr := rejectArtifactSymlinkComponents(scopeRoot); pathErr != nil {
			return values.ArtifactCleanupResult{}, artifactError(values.ArtifactOperationCleanup, values.ArtifactFailureInvalid, nil, pathErr)
		}
		owners, err := os.ReadDir(scopeRoot)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return values.ArtifactCleanupResult{}, artifactError(values.ArtifactOperationCleanup, values.ArtifactFailureInvalid, nil, err)
		}
		for _, ownerEntry := range owners {
			if ownerEntry.Type()&os.ModeSymlink != 0 || !ownerEntry.IsDir() || len(ownerEntry.Name()) != 64 || !artifactLowerHex(ownerEntry.Name()) {
				return values.ArtifactCleanupResult{}, artifactError(values.ArtifactOperationCleanup, values.ArtifactFailureInvalid, nil, values.ErrArtifactInvalid)
			}
			ownerCandidates, err := store.collectOwnerArtifacts(ctx, filepath.Join(scopeRoot, ownerEntry.Name()))
			if err != nil {
				return values.ArtifactCleanupResult{}, artifactError(values.ArtifactOperationCleanup, values.ArtifactFailureInvalid, nil, err)
			}
			for _, candidate := range ownerCandidates {
				if !candidate.metadata.ExpiresAt.IsZero() && !candidate.metadata.ExpiresAt.After(request.Before) {
					candidates = append(candidates, candidate)
				}
			}
		}
	}
	sortArtifactCandidates(candidates)
	for _, candidate := range candidates {
		if err := store.authorize(ctx, values.ArtifactOperationCleanup, request.Access, &candidate.metadata.Ref, &candidate.metadata.Owner); err != nil {
			return values.ArtifactCleanupResult{}, err
		}
	}
	return removeArtifactCandidates(ctx, candidates, values.ArtifactCleanupNotStored)
}

func (store *ArtifactStore) cleanupPartials(ctx context.Context, before time.Time) (values.ArtifactCleanupResult, error) {
	stagingRoot := filepath.Join(store.root, "staging")
	entries, err := os.ReadDir(stagingRoot)
	if err != nil {
		return values.ArtifactCleanupResult{}, artifactError(values.ArtifactOperationCleanup, values.ArtifactFailureInvalid, nil, err)
	}
	var paths []string
	for _, entry := range entries {
		if err := checkArtifactContext(ctx, values.ArtifactOperationCleanup, nil); err != nil {
			return values.ArtifactCleanupResult{}, err
		}
		if entry.Type()&os.ModeSymlink != 0 || !entry.IsDir() || !strings.HasPrefix(entry.Name(), "partial-") {
			return values.ArtifactCleanupResult{}, artifactError(values.ArtifactOperationCleanup, values.ArtifactFailureInvalid, nil, values.ErrArtifactInvalid)
		}
		info, err := entry.Info()
		if err != nil {
			return values.ArtifactCleanupResult{}, artifactError(values.ArtifactOperationCleanup, values.ArtifactFailureInvalid, nil, err)
		}
		if !info.ModTime().After(before) {
			paths = append(paths, filepath.Join(stagingRoot, entry.Name()))
		}
	}
	sort.Strings(paths)
	for _, path := range paths {
		if err := checkArtifactContext(ctx, values.ArtifactOperationCleanup, nil); err != nil {
			return values.ArtifactCleanupResult{}, err
		}
		if err := os.RemoveAll(path); err != nil {
			return values.ArtifactCleanupResult{}, artifactError(values.ArtifactOperationCleanup, values.ArtifactFailureInvalid, nil, err)
		}
	}
	if len(paths) == 0 {
		return values.ArtifactCleanupResult{Outcome: values.ArtifactCleanupNotStored}, nil
	}
	if err := syncArtifactDirectory(stagingRoot); err != nil {
		return values.ArtifactCleanupResult{}, artifactError(values.ArtifactOperationCleanup, values.ArtifactFailureInvalid, nil, err)
	}
	return values.ArtifactCleanupResult{Outcome: values.ArtifactCleanupDeleted, DeletedCount: len(paths)}, nil
}

func (store *ArtifactStore) collectOwnerArtifacts(ctx context.Context, ownerDirectory string) ([]artifactCandidate, error) {
	if pathErr := rejectArtifactSymlinkComponents(ownerDirectory); pathErr != nil {
		return nil, pathErr
	}
	ownerInfo, err := os.Lstat(ownerDirectory)
	if err != nil {
		return nil, err
	}
	if !ownerInfo.IsDir() || ownerInfo.Mode()&os.ModeSymlink != 0 {
		return nil, values.ErrArtifactInvalid
	}
	entries, err := os.ReadDir(ownerDirectory)
	if err != nil {
		return nil, err
	}
	candidates := make([]artifactCandidate, 0, len(entries))
	for _, entry := range entries {
		if err := checkArtifactContext(ctx, values.ArtifactOperationCleanup, nil); err != nil {
			return nil, err
		}
		if entry.Type()&os.ModeSymlink != 0 || !entry.IsDir() || len(entry.Name()) != 64 || !artifactLowerHex(entry.Name()) {
			return nil, values.ErrArtifactInvalid
		}
		directory := filepath.Join(ownerDirectory, entry.Name())
		manifest, err := readArtifactManifest(directory)
		if err != nil {
			return nil, err
		}
		locator, err := parseArtifactRef(manifest.Metadata.Ref)
		if err != nil || locator.artifactID != entry.Name() || locator.ownerHash != filepath.Base(ownerDirectory) ||
			string(locator.scope) != filepath.Base(filepath.Dir(ownerDirectory)) {
			return nil, values.ErrArtifactInvalid
		}
		metadata, err := verifyStoredArtifact(directory, locator, &manifest.Metadata.Ref)
		if err != nil {
			return nil, err
		}
		candidates = append(candidates, artifactCandidate{path: directory, metadata: metadata})
	}
	sortArtifactCandidates(candidates)
	return candidates, nil
}

func removeArtifactCandidates(ctx context.Context, candidates []artifactCandidate, empty values.ArtifactCleanupOutcome) (values.ArtifactCleanupResult, error) {
	if len(candidates) == 0 {
		return values.ArtifactCleanupResult{Outcome: empty}, nil
	}
	for _, candidate := range candidates {
		if err := checkArtifactContext(ctx, values.ArtifactOperationCleanup, &candidate.metadata.Ref); err != nil {
			return values.ArtifactCleanupResult{}, err
		}
		if pathErr := rejectArtifactSymlinkComponents(candidate.path); pathErr != nil {
			return values.ArtifactCleanupResult{}, artifactError(values.ArtifactOperationCleanup, values.ArtifactFailureInvalid, &candidate.metadata.Ref, pathErr)
		}
		if err := os.RemoveAll(candidate.path); err != nil {
			return values.ArtifactCleanupResult{}, artifactError(values.ArtifactOperationCleanup, values.ArtifactFailureInvalid, &candidate.metadata.Ref, err)
		}
		if err := syncArtifactDirectory(filepath.Dir(candidate.path)); err != nil {
			return values.ArtifactCleanupResult{}, artifactError(values.ArtifactOperationCleanup, values.ArtifactFailureInvalid, &candidate.metadata.Ref, err)
		}
	}
	return values.ArtifactCleanupResult{Outcome: values.ArtifactCleanupDeleted, DeletedCount: len(candidates)}, nil
}

func sortArtifactCandidates(candidates []artifactCandidate) {
	sort.Slice(candidates, func(left, right int) bool {
		if candidates[left].metadata.Ref.Digest == candidates[right].metadata.Ref.Digest {
			return candidates[left].metadata.Ref.URI < candidates[right].metadata.Ref.URI
		}
		return candidates[left].metadata.Ref.Digest < candidates[right].metadata.Ref.Digest
	})
}

func artifactCleanupOwner(request values.ArtifactCleanupRequest) *values.ArtifactOwner {
	if request.Kind != values.ArtifactCleanupRun && request.Kind != values.ArtifactCleanupProject {
		return nil
	}
	owner := request.Owner
	return &owner
}
