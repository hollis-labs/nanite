package install

import (
	"context"
	"errors"
	"fmt"
	"sync"
)

// State is the current position in the install state machine.
type State string

const (
	StateNotInstalled State = "not_installed"
	StateDownloading  State = "downloading"
	StateVerifying    State = "verifying"
	StateExtracting   State = "extracting"
	StateValidating   State = "validating"
	StateLoading      State = "loading"
	StateReady        State = "ready"
	StateFailed       State = "failed"
)

// Event is emitted on each transition and on progress updates within a state.
// Progress is a fraction in [0,1] for states that stream (Downloading,
// Extracting); 0 otherwise.
type Event struct {
	PluginID string
	State    State
	Progress float64
	Message  string
	Err      error
}

// EventFunc consumes install events. Pass nil to ignore.
type EventFunc func(Event)

// Source describes where a plugin archive comes from. A Source is either a
// catalog-backed archive (resolves to signed tar.gz + checksum + signature)
// or a local-path source (already-extracted directory on disk). Concrete
// implementations live in G.2 (archive/catalog) and G.6 (local path).
type Source interface {
	// PluginID is the canonical plugin id this Source resolves to.
	PluginID() string

	// Download fetches the archive (or confirms the local path) and returns
	// a handle the later steps operate on. For archive sources the handle
	// is the path to the downloaded tar.gz in stagingDir. For local-path
	// sources the handle is the source directory.
	Download(ctx context.Context, stagingDir string, emit EventFunc) (Handle, error)
}

// Handle is returned from Source.Download and threaded through the rest of
// the state machine. Kind reports which steps should run.
type Handle struct {
	// Kind is "archive" when Path points at a tar.gz that must be verified
	// and extracted, or "directory" when Path is already an on-disk
	// plugin dir (local --link / dev flow) that bypasses verify+extract.
	Kind string
	Path string

	// ExpectedSHA256 is the hex-encoded sha256 the archive must match.
	// Ignored for "directory" kind.
	ExpectedSHA256 string

	// Signature is the raw Ed25519 signature bytes over the archive.
	// Ignored for "directory" kind.
	Signature []byte

	// SignerKeyID identifies which trusted key signed this archive (for
	// trust lookup). Ignored for "directory" kind.
	SignerKeyID string
}

// Verifier checks archive integrity + signature. Implementation in G.2.
type Verifier interface {
	Verify(ctx context.Context, h Handle) error
}

// Extractor materialises a Handle into targetDir. For archive handles it
// extracts the tar.gz; for directory handles it copies (or symlinks) the
// source directory. Implementation in G.2.
type Extractor interface {
	Extract(ctx context.Context, h Handle, targetDir string, emit EventFunc) error
}

// Validator runs the install-time manifest validation against an on-disk
// plugin directory (the existing ValidateManifest in validate.go).
type Validator interface {
	Validate(ctx context.Context, pluginDir string) error
}

// Loader hands the extracted+validated plugin directory off to the runtime
// plugin host. Implementation wraps internal/plugin.(*Host).LoadPlugin via
// internal/plugin.LoadDiscovered.
type Loader interface {
	Load(ctx context.Context, pluginID, pluginDir string) error
}

// Staging manages the temp dir + atomic rename lifecycle. Implementation in
// G.3 (staging.go).
type Staging interface {
	// Begin reserves a temp dir under the staging root, returns its path,
	// and acquires a per-plugin lock. Caller must call the returned
	// cleanup func on error paths; it removes the staging dir and
	// releases the lock.
	Begin(ctx context.Context, pluginID string) (stagingDir string, cleanup func(), err error)

	// Commit atomically renames stagingDir into the final plugins dir
	// under pluginID. The lock released by cleanup must remain held until
	// after Commit returns.
	Commit(ctx context.Context, stagingDir, pluginID string) (finalDir string, err error)
}

// Installer drives the install state machine.
type Installer struct {
	Verifier  Verifier
	Extractor Extractor
	Validator Validator
	Loader    Loader
	Staging   Staging
	Emit      EventFunc

	mu    sync.Mutex
	state State
}

// State returns the current state. Safe for concurrent read.
func (i *Installer) State() State {
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.state == "" {
		return StateNotInstalled
	}
	return i.state
}

func (i *Installer) setState(s State) {
	i.mu.Lock()
	i.state = s
	i.mu.Unlock()
}

// Install runs the full state machine for src: NotInstalled → Downloading →
// Verifying → Extracting → Validating → Loading → Ready. Every failure
// drives the installer to StateFailed (observable via State()) and runs
// any registered staging cleanup to remove half-written artifacts before
// returning the error.
//
// Returns (finalDir, nil) on success, ("", err) on failure. A successful
// return means the plugin directory is in place under the staging root's
// sibling plugins dir and has been handed to the Loader.
func (i *Installer) Install(ctx context.Context, src Source) (string, error) {
	if src == nil {
		return "", errors.New("install: Source is nil")
	}
	if i.Staging == nil {
		return "", errors.New("install: Staging is nil")
	}
	if i.Validator == nil {
		return "", errors.New("install: Validator is nil")
	}
	if i.Loader == nil {
		return "", errors.New("install: Loader is nil")
	}

	pluginID := src.PluginID()
	if pluginID == "" {
		return "", errors.New("install: Source.PluginID is empty")
	}

	i.setState(StateNotInstalled)

	// Begin staging — reserve temp dir + lock.
	stagingDir, cleanup, err := i.Staging.Begin(ctx, pluginID)
	if err != nil {
		return "", i.fail(pluginID, StateNotInstalled, fmt.Errorf("staging begin: %w", err))
	}
	rolledBack := false
	defer func() {
		if !rolledBack {
			return
		}
		cleanup()
	}()

	// Downloading.
	i.transition(pluginID, StateDownloading, "fetching archive")
	handle, err := src.Download(ctx, stagingDir, i.Emit)
	if err != nil {
		rolledBack = true
		return "", i.fail(pluginID, StateDownloading, fmt.Errorf("download: %w", err))
	}

	// Directory handles (local --link, dev flow) skip verify + extract and
	// treat the handle path as the "extracted" plugin dir directly. The
	// staging dir itself is still used as the swap source; extraction for
	// directory handles is a symlink placement done in the Extractor impl.
	isArchive := handle.Kind == "archive"

	// Verifying.
	if isArchive {
		i.transition(pluginID, StateVerifying, "verifying signature")
		if i.Verifier == nil {
			rolledBack = true
			return "", i.fail(pluginID, StateVerifying, errors.New("Verifier is nil"))
		}
		if err := i.Verifier.Verify(ctx, handle); err != nil {
			rolledBack = true
			return "", i.fail(pluginID, StateVerifying, fmt.Errorf("verify: %w", err))
		}
	}

	// Extracting.
	i.transition(pluginID, StateExtracting, "extracting archive")
	if i.Extractor == nil {
		rolledBack = true
		return "", i.fail(pluginID, StateExtracting, errors.New("Extractor is nil"))
	}
	if err := i.Extractor.Extract(ctx, handle, stagingDir, i.Emit); err != nil {
		rolledBack = true
		return "", i.fail(pluginID, StateExtracting, fmt.Errorf("extract: %w", err))
	}

	// Validating.
	i.transition(pluginID, StateValidating, "validating manifest")
	if err := i.Validator.Validate(ctx, stagingDir); err != nil {
		rolledBack = true
		return "", i.fail(pluginID, StateValidating, fmt.Errorf("validate: %w", err))
	}

	// Atomic swap into the final plugins dir. Done before Loading so that
	// the loaded plugin refers to its final-path files rather than the
	// staging path (subprocess plugins resolve entrypoint relative to
	// their install dir).
	finalDir, err := i.Staging.Commit(ctx, stagingDir, pluginID)
	if err != nil {
		rolledBack = true
		return "", i.fail(pluginID, StateValidating, fmt.Errorf("commit: %w", err))
	}

	// Loading. After Commit, the staging dir no longer exists — on Load
	// failure we still emit Failed but we don't try to roll back the
	// committed final dir (caller can uninstall explicitly).
	i.transition(pluginID, StateLoading, "loading plugin")
	if err := i.Loader.Load(ctx, pluginID, finalDir); err != nil {
		return "", i.fail(pluginID, StateLoading, fmt.Errorf("load: %w", err))
	}

	i.transition(pluginID, StateReady, "ready")
	return finalDir, nil
}

func (i *Installer) transition(pluginID string, s State, msg string) {
	i.setState(s)
	if i.Emit != nil {
		i.Emit(Event{PluginID: pluginID, State: s, Message: msg})
	}
}

func (i *Installer) fail(pluginID string, from State, err error) error {
	i.setState(StateFailed)
	if i.Emit != nil {
		i.Emit(Event{
			PluginID: pluginID,
			State:    StateFailed,
			Message:  fmt.Sprintf("failed in %s", from),
			Err:      err,
		})
	}
	return err
}
