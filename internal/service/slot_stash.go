// Package service — slot stash implementation.
//
// SP-20260512-0008 W2C (CW-20260512-0110): production-side adapter that
// satisfies contextbroker.SlotStasher by writing oversized slot content
// to the existing artifact store. The decider invokes this for any
// slot whose content exceeds its per-slot budget; the result's
// artifact_id flows back into the pointer envelope the agent sees
// (`<ref:artifact_id=ART-..., tokens=N, available via dev_read>`).
//
// Idempotency: stashed artifact rows use content-addressed IDs
// (contextbroker.DeterministicArtifactID), so re-stashing the same
// (session, slot, content) tuple is a no-op — the existing row is
// returned with Reused=true. This is the cache-stability contract the
// pointer envelope relies on across turns.
//
// Atomicity: filesystem write happens before the DB insert. If the FS
// write fails, no DB row is created and the broker falls back to
// shipping inline. If the DB insert fails after a successful FS write,
// the orphaned file is left in place (the next stash of the same
// content will find an existing row and skip the insert; an orphan
// without a DB row is harmless and can be GC'd by a future cleanup
// job — see follow-ups in the implementer report).
package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/hollis-labs/nanite/internal/config"
	"github.com/hollis-labs/nanite/internal/contextbroker"
	"github.com/hollis-labs/nanite/internal/pathsafe"
	"github.com/hollis-labs/nanite/internal/store"
)

// SlotStashOrigin is the artifact origin string used for context-broker
// slot stashes. Distinguishes them from uploaded/placed/auto artifacts so
// the right-rail UI and `ListArtifactsByOrigin` can filter them out by
// default (a stash artifact is a server-side cache entry, not a
// user-facing file).
const SlotStashOrigin = "stashed"

// artifactStasher implements contextbroker.SlotStasher against the
// existing artifact store. Construct via NewArtifactStasher.
type artifactStasher struct {
	store         *store.Store
	storageRoot   string
	sourceAgentID string // optional — populated as source_agent_id for traceability
}

// ArtifactStasherConfig holds the dependencies the slot stasher needs.
type ArtifactStasherConfig struct {
	// Store is the nanite SQLite-backed store. Required.
	Store *store.Store

	// AppConfig provides the artifacts storage directory. When nil, the
	// stasher falls back to config.DefaultAppConfig().Artifacts.StorageDir
	// — matches the API layer's defaultArtifactsStorageDir constant for
	// consistency across surfaces.
	AppConfig *config.AppConfig

	// SourceAgentID is recorded on each stash row's source_agent_id
	// column. Optional; useful for telemetry and "which agent caused
	// this stash" tracing. Empty string is allowed (NULLed at the SQL
	// boundary).
	SourceAgentID string
}

// NewArtifactStasher wraps the artifact store in a contextbroker.SlotStasher.
// Returns an error when cfg.Store is nil. The returned stasher is safe
// for concurrent use across sessions.
func NewArtifactStasher(cfg ArtifactStasherConfig) (contextbroker.SlotStasher, error) {
	if cfg.Store == nil {
		return nil, errors.New("service: artifact stasher requires non-nil Store")
	}

	root := ""
	if cfg.AppConfig != nil {
		root = cfg.AppConfig.Artifacts.StorageDir
	}
	if root == "" {
		// Match defaultArtifactsStorageDir in internal/api/artifacts.go so
		// the file the API layer serves on /artifacts/<id> download is the
		// same file the dev_read(artifact_id=) entrypoint reads.
		root = config.DefaultAppConfig().Artifacts.StorageDir
	}
	return &artifactStasher{
		store:         cfg.Store,
		storageRoot:   root,
		sourceAgentID: cfg.SourceAgentID,
	}, nil
}

// StashSlot persists the slot's content to the artifact store under a
// content-addressed ID and returns the ID for the broker's pointer
// envelope. Re-stashing the same (session, slot, content) tuple is a
// no-op — the existing artifact row is returned with Reused=true.
//
// Returns an error only on truly atomic failures (path resolution,
// filesystem write, non-conflict DB error). The broker translates any
// error into a fallback to ActionShip — the wire never references a
// non-existent artifact.
func (a *artifactStasher) StashSlot(ctx context.Context, req contextbroker.StashRequest) (contextbroker.StashResult, error) {
	if req.SessionID == "" {
		return contextbroker.StashResult{}, errors.New("artifact stasher: SessionID is required")
	}
	if req.Content == "" {
		return contextbroker.StashResult{}, errors.New("artifact stasher: Content is empty")
	}

	artifactID := contextbroker.DeterministicArtifactID(req.SessionID, req.SlotName, req.Content)

	// Idempotency: if an artifact with this ID already exists, return
	// reused=true without writing anything. The content is content-
	// addressed so the existing row's storage_path holds identical bytes.
	if existing, err := a.store.GetArtifact(artifactID); err == nil && existing != nil {
		return contextbroker.StashResult{ArtifactID: artifactID, Reused: true}, nil
	}

	// Resolve the storage directory for this session under the artifacts
	// root. pathsafe.ResolveUnder guards against a malicious or buggy
	// session_id that contains separators or `..` — same guard the
	// upload handler uses.
	sessionDir, err := pathsafe.ResolveUnder(a.storageRoot, req.SessionID)
	if err != nil {
		return contextbroker.StashResult{}, fmt.Errorf("resolve session dir: %w", err)
	}
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		return contextbroker.StashResult{}, fmt.Errorf("create session dir: %w", err)
	}

	// Stash file naming: `<artifact_id>.txt`. Plain text — the content
	// is whatever the slot produced, generally markdown / fenced text.
	// The MIME type is set explicitly below so dev_read returns it as
	// plain text without sniffing.
	filename := artifactID + ".txt"
	storagePath, err := pathsafe.ResolveUnder(sessionDir, filename)
	if err != nil {
		return contextbroker.StashResult{}, fmt.Errorf("resolve storage path: %w", err)
	}

	// FS write first (no DB row yet). If this fails, no orphans are
	// created and the broker falls back to inline shipping.
	if err := os.WriteFile(storagePath, []byte(req.Content), 0o644); err != nil {
		return contextbroker.StashResult{}, fmt.Errorf("write stash file: %w", err)
	}

	// DB row. If this fails, the FS file is orphaned — but harmless
	// (the next stash with the same content finds no row and re-writes
	// the file content-identically). Logged at WARN so operators can
	// reconcile via a future GC pass.
	artifact := &store.Artifact{
		ID:            artifactID,
		SessionID:     req.SessionID,
		Name:          fmt.Sprintf("slot-stash-%s.txt", req.SlotName),
		MimeType:      "text/plain; charset=utf-8",
		SizeBytes:     int64(len(req.Content)),
		StoragePath:   storagePath,
		Origin:        SlotStashOrigin,
		SourceAgentID: a.sourceAgentID,
		Metadata:      fmt.Sprintf(`{"slot":%q,"tokens":%d,"source":"context-broker"}`, req.SlotName, req.Tokens),
	}
	if err := a.store.CreateArtifact(artifact); err != nil {
		// Best-effort cleanup of the orphaned file. The FS write
		// succeeded but the row didn't land; without the row, the
		// agent has no way to address it. Removing the file keeps
		// the artifacts root tidy. If removal fails, log and move on
		// — the GC follow-up will handle stragglers.
		if rmErr := os.Remove(storagePath); rmErr != nil {
			slog.Warn("artifact stasher: orphaned file cleanup failed",
				"artifact_id", artifactID,
				"path", storagePath,
				"err", rmErr,
			)
		}
		return contextbroker.StashResult{}, fmt.Errorf("create artifact row: %w", err)
	}

	slog.Debug("artifact stasher: slot stashed",
		"artifact_id", artifactID,
		"slot", req.SlotName,
		"session_id", req.SessionID,
		"size_bytes", artifact.SizeBytes,
		"tokens", req.Tokens,
		"path", filepath.Base(storagePath),
	)
	return contextbroker.StashResult{ArtifactID: artifactID, Reused: false}, nil
}
