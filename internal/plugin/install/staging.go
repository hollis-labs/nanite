package install

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"
)

// DirStaging manages a staging root (temp dirs + lockfiles) sitting next to
// the installed-plugins root. Commit atomically renames a staged dir into
// its final position under PluginsRoot/<pluginID>.
//
// Zero value is invalid — set StagingRoot and PluginsRoot. Both roots must
// live on the same filesystem for os.Rename to be atomic; Commit detects
// cross-FS failures and returns a clear error.
type DirStaging struct {
	StagingRoot string
	PluginsRoot string
}

// Begin reserves a fresh staging dir under StagingRoot and acquires a
// lockfile keyed on pluginID. The caller must run the returned cleanup
// func on failure paths; on success paths, Commit takes over ownership
// of the staging dir and cleanup is a no-op.
func (s *DirStaging) Begin(ctx context.Context, pluginID string) (string, func(), error) {
	if err := s.validate(); err != nil {
		return "", nil, err
	}
	if err := ValidatePluginID(pluginID); err != nil {
		return "", nil, err
	}
	if err := os.MkdirAll(s.StagingRoot, 0o755); err != nil {
		return "", nil, fmt.Errorf("staging: mkdir staging root: %w", err)
	}
	if err := os.MkdirAll(s.PluginsRoot, 0o755); err != nil {
		return "", nil, fmt.Errorf("staging: mkdir plugins root: %w", err)
	}

	lockPath := filepath.Join(s.StagingRoot, pluginID+".lock")
	lock, err := os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return "", nil, fmt.Errorf("staging: install already in progress for %q (lock %s)", pluginID, lockPath)
		}
		return "", nil, fmt.Errorf("staging: acquire lock: %w", err)
	}
	_ = lock.Close()

	stagingDir, err := os.MkdirTemp(s.StagingRoot, pluginID+"-*")
	if err != nil {
		_ = os.Remove(lockPath)
		return "", nil, fmt.Errorf("staging: mkdtemp: %w", err)
	}

	cleanup := func() {
		_ = os.RemoveAll(stagingDir)
		_ = os.Remove(lockPath)
	}
	return stagingDir, cleanup, nil
}

// Commit renames stagingDir into PluginsRoot/<pluginID>. If the final dir
// already exists, the previous contents are first moved aside (for
// rollback on failure) and then removed on success. This gives both
// Unix and Windows a prep-delete-rename path; plain os.Rename onto a
// populated dir fails on both platforms when the target is non-empty.
//
// The staging lockfile is removed as part of a successful Commit so the
// caller's Begin cleanup becomes a no-op.
func (s *DirStaging) Commit(ctx context.Context, stagingDir, pluginID string) (string, error) {
	if err := s.validate(); err != nil {
		return "", err
	}
	if err := ValidatePluginID(pluginID); err != nil {
		return "", err
	}
	if stagingDir == "" {
		return "", errors.New("staging: empty stagingDir")
	}
	if !isUnder(s.StagingRoot, stagingDir) {
		return "", fmt.Errorf("staging: stagingDir %q not under staging root", stagingDir)
	}

	finalDir := filepath.Join(s.PluginsRoot, pluginID)

	// Move aside an existing plugin install, if any.
	var backup string
	if _, err := os.Stat(finalDir); err == nil {
		backup = finalDir + ".backup-" + timestampSuffix()
		if err := os.Rename(finalDir, backup); err != nil {
			return "", fmt.Errorf("staging: move aside existing %s: %w", finalDir, err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("staging: stat final dir: %w", err)
	}

	if err := os.Rename(stagingDir, finalDir); err != nil {
		// Attempt to restore the backup on failure.
		if backup != "" {
			_ = os.Rename(backup, finalDir)
		}
		if isCrossDeviceError(err) {
			return "", fmt.Errorf("staging: cross-filesystem rename from %s to %s — staging root and plugins root must live on the same filesystem", s.StagingRoot, s.PluginsRoot)
		}
		return "", fmt.Errorf("staging: atomic rename: %w", err)
	}

	// Success — clean up backup and lock.
	if backup != "" {
		if err := os.RemoveAll(backup); err != nil {
			// Non-fatal; the backup is orphaned but the install succeeded.
			_ = err
		}
	}
	_ = os.Remove(filepath.Join(s.StagingRoot, pluginID+".lock"))
	return finalDir, nil
}

func (s *DirStaging) validate() error {
	if s.StagingRoot == "" {
		return errors.New("staging: StagingRoot not set")
	}
	if s.PluginsRoot == "" {
		return errors.New("staging: PluginsRoot not set")
	}
	return nil
}

// ValidatePluginID guards against path traversal via the pluginID. Pattern
// matches the v1 manifest schema + subprocess validator:
// ^[a-z][a-z0-9-]{1,62}$ — must start with a letter, only lowercase
// alnum and '-', total length 2-63.
//
// Exported so callers that need to confine a catalog- or manifest-derived
// name before doing anything else with it (e.g. internal/api's catalog
// install handler) can reuse the exact allowlist DirStaging.Begin/Commit
// enforce internally, rather than reimplementing it or adding a second,
// different confinement mechanism (AD-04 item 1,
// TASKS/audit-remediation/01-plugin-install-convergence/01-unify-plugin-catalog-install-pipeline.md).
func ValidatePluginID(id string) error {
	if id == "" {
		return errors.New("staging: empty plugin id")
	}
	if len(id) < 2 || len(id) > 63 {
		return fmt.Errorf("staging: plugin id %q length %d out of range [2,63]", id, len(id))
	}
	first := rune(id[0])
	if first < 'a' || first > 'z' {
		return fmt.Errorf("staging: plugin id %q must start with lowercase letter", id)
	}
	for _, r := range id[1:] {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= '0' && r <= '9':
		case r == '-':
		default:
			return fmt.Errorf("staging: invalid plugin id %q", id)
		}
	}
	return nil
}

// isCrossDeviceError reports whether err stems from a rename across
// filesystems (EXDEV on Unix, ERROR_NOT_SAME_DEVICE on Windows).
func isCrossDeviceError(err error) bool {
	var linkErr *os.LinkError
	if errors.As(err, &linkErr) {
		err = linkErr.Err
	}
	if errors.Is(err, syscall.EXDEV) {
		return true
	}
	// On Windows, the syscall error is wrapped; string-match as a fallback.
	if runtime.GOOS == "windows" && err != nil && strings.Contains(err.Error(), "different") {
		return true
	}
	return false
}

func timestampSuffix() string {
	return fmt.Sprintf("%d", time.Now().UnixNano())
}

// isUnder reports whether child is lexically under parent using a
// path-aware check (filepath.Rel) that is not fooled by sibling
// directories whose names share a prefix (e.g. "/root" vs "/root-evil").
func isUnder(parent, child string) bool {
	p := filepath.Clean(parent)
	c := filepath.Clean(child)
	rel, err := filepath.Rel(p, c)
	if err != nil {
		return false
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return false
	}
	return true
}
