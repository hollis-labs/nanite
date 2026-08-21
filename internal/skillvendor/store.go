package skillvendor

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/hollis-labs/nanite/internal/fsutil"
	"github.com/hollis-labs/nanite/internal/pathsafe"
)

// stagingDirName is the sibling directory under Store.root that Write
// stages new packages into before publishing them via atomic rename.
// Kept as a fixed, non-address-shaped name so it's trivially excluded from
// anything that ever needs to enumerate real addresses under root.
const stagingDirName = ".staging"

// Store is the content-addressed vendored skill store rooted at a single
// filesystem directory. Safe for concurrent use — see the package doc's
// "Write ordering / crash safety" section for the concurrency contract.
//
// Construct via New. The zero value is not usable.
type Store struct {
	root string
}

// WriteResult is what Write returns on success.
type WriteResult struct {
	// Address is the content address the package was written to (or
	// already existed at).
	Address string
	// Reused is true when the address already existed with matching
	// content and no new write occurred — either because the caller
	// re-submitted unchanged content, or because a concurrent writer won
	// the race to publish this address first.
	Reused bool
}

// New constructs a Store rooted at root, creating the directory if it
// doesn't already exist. Returns an error if root is empty or cannot be
// created.
func New(root string) (*Store, error) {
	if root == "" {
		return nil, errors.New("skillvendor: empty root")
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, fmt.Errorf("skillvendor: create root %q: %w", root, err)
	}
	return &Store{root: root}, nil
}

// Root returns the store's filesystem root, for callers that need to
// display or log it (e.g. an admin diagnostics surface). Not intended for
// direct filesystem access — use Path/ReadFiles for that so the
// corruption/missing checks always run.
func (s *Store) Root() string { return s.root }

// Write computes files' content address and, if it doesn't already exist
// in the store, publishes it atomically. If the address already exists,
// Write verifies the on-disk content still matches (ErrCorrupted if not)
// and returns Reused=true without touching the filesystem again — this is
// the idempotency fast-path and also how a concurrent double-write of
// identical content converges without error (see package doc).
//
// Write never mutates a previously-written address's contents in place.
// Because the address is a hash of the content, there is no way to submit
// "different content at an existing address" through this API — a content
// difference always produces a different address. The only way an
// existing address's on-disk bytes could fail to match it is out-of-band
// disk corruption, which Write surfaces as ErrCorrupted rather than
// silently overwriting.
func (s *Store) Write(ctx context.Context, files FileMap) (WriteResult, error) {
	if err := ctx.Err(); err != nil {
		return WriteResult{}, err
	}

	norm, err := normalizeFileMap(files)
	if err != nil {
		return WriteResult{}, err
	}
	address := computeAddress(norm)

	addressDir, err := pathsafe.ResolveUnder(s.root, address)
	if err != nil {
		return WriteResult{}, fmt.Errorf("skillvendor: resolve address dir: %w", err)
	}

	if reused, err := s.checkExisting(addressDir, address); err != nil {
		return WriteResult{}, err
	} else if reused {
		return WriteResult{Address: address, Reused: true}, nil
	}

	stagingDir, err := s.stageFiles(address, norm)
	if err != nil {
		return WriteResult{}, err
	}

	if err := os.Rename(stagingDir, addressDir); err != nil {
		// Someone else may have won the race and published this exact
		// address between our existence check above and this rename. Our
		// staging directory is discarded either way (it's a duplicate of
		// whatever's now at addressDir, or genuinely useless on real
		// failure); fall back to the same existence/verify path.
		_ = os.RemoveAll(stagingDir)
		if reused, checkErr := s.checkExisting(addressDir, address); checkErr != nil {
			return WriteResult{}, checkErr
		} else if reused {
			return WriteResult{Address: address, Reused: true}, nil
		}
		return WriteResult{}, fmt.Errorf("skillvendor: publish %s: %w", address, err)
	}

	return WriteResult{Address: address, Reused: false}, nil
}

// checkExisting reports whether addressDir already exists and, if so,
// verifies its content matches address. Returns (true, nil) when an
// existing, verified directory is found; (false, nil) when nothing exists
// yet; and a non-nil error for anything else (stat failure, non-directory
// entry, or a verified content mismatch — ErrCorrupted).
func (s *Store) checkExisting(addressDir, address string) (bool, error) {
	info, err := os.Stat(addressDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, fmt.Errorf("skillvendor: stat %s: %w", address, err)
	}
	if !info.IsDir() {
		return false, fmt.Errorf("%w: %s exists on disk but is not a directory", ErrCorrupted, address)
	}
	if err := verifyAddress(addressDir, address); err != nil {
		return false, err
	}
	return true, nil
}

// stageFiles writes norm's contents into a fresh temporary directory under
// s.root, using fsutil's crash-safe atomic-write-then-rename primitive for
// each individual file. Returns the staging directory's path, ready to be
// published via a single os.Rename onto the final address directory.
// Cleans up the staging directory itself on any failure.
func (s *Store) stageFiles(address string, norm FileMap) (string, error) {
	stagingRoot := filepath.Join(s.root, stagingDirName)
	if err := os.MkdirAll(stagingRoot, 0o755); err != nil {
		return "", fmt.Errorf("skillvendor: create staging root: %w", err)
	}
	stagingDir, err := os.MkdirTemp(stagingRoot, address+"-*")
	if err != nil {
		return "", fmt.Errorf("skillvendor: create staging dir: %w", err)
	}

	for relPath, content := range norm {
		target := filepath.Join(stagingDir, filepath.FromSlash(relPath))
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			_ = os.RemoveAll(stagingDir)
			return "", fmt.Errorf("skillvendor: mkdir for %q: %w", relPath, err)
		}
		if err := fsutil.AtomicWriteFile(target, content, 0o644); err != nil {
			_ = os.RemoveAll(stagingDir)
			return "", fmt.Errorf("skillvendor: write %q: %w", relPath, err)
		}
	}
	return stagingDir, nil
}

// Path returns the live filesystem directory for address. Callers should
// treat the returned path as read-only — walk it, os.ReadFile individual
// files, exec scripts beneath it — and never write under it directly; the
// only supported mutation path is through this package's own Write/Delete.
//
// Every call re-verifies the on-disk content against address (see package
// doc's "Corruption detection"), so this is intentionally not a cheap,
// cached lookup — it's the "materialization always reads the vendored copy
// live" contract from docs/engineering/architecture/20-skills.md made
// concrete.
//
// Returns ErrAddressMissing if no directory exists for address, or
// ErrCorrupted if the directory exists but its content no longer hashes to
// address.
func (s *Store) Path(address string) (string, error) {
	if err := ValidateAddress(address); err != nil {
		return "", err
	}
	addressDir, err := pathsafe.ResolveUnder(s.root, address)
	if err != nil {
		return "", fmt.Errorf("skillvendor: resolve address dir: %w", err)
	}

	info, err := os.Stat(addressDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("%w: %s", ErrAddressMissing, address)
		}
		return "", fmt.Errorf("skillvendor: stat %s: %w", address, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("%w: %s exists on disk but is not a directory", ErrCorrupted, address)
	}
	if err := verifyAddress(addressDir, address); err != nil {
		return "", err
	}
	return addressDir, nil
}

// ReadFiles returns address's full content as a FileMap. Convenience
// wrapper over Path + a directory walk for callers that want the bytes
// in-memory rather than a filesystem path (e.g. a preview/diff surface).
// Carries the same ErrAddressMissing/ErrCorrupted contract as Path.
func (s *Store) ReadFiles(address string) (FileMap, error) {
	dir, err := s.Path(address)
	if err != nil {
		return nil, err
	}
	return readTree(dir)
}

// Delete removes address's directory entirely. This is the only supported
// mutation against a previously-written address — full removal (for
// uninstall, TASKS/skills/12), never a partial or in-place content
// replacement. Safe to call on an address that doesn't exist (no-op,
// matching os.RemoveAll's own semantics).
func (s *Store) Delete(address string) error {
	if err := ValidateAddress(address); err != nil {
		return err
	}
	addressDir, err := pathsafe.ResolveUnder(s.root, address)
	if err != nil {
		return fmt.Errorf("skillvendor: resolve address dir: %w", err)
	}
	if err := os.RemoveAll(addressDir); err != nil {
		return fmt.Errorf("skillvendor: delete %s: %w", address, err)
	}
	return nil
}
