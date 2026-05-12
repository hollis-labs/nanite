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
// Atomicity (CW-20260512-0110 + reviewer-hardening 2026-05-12):
//
//   - Filesystem write happens before the DB insert. If the FS write
//     fails, no DB row is created and the broker falls back to shipping
//     inline.
//   - If the DB insert fails after a successful FS write, the orphaned
//     file is best-effort removed. If removal fails, the orphan is
//     harmless — the next stash of the same content finds no row and
//     re-writes the file content-identically.
//   - The idempotency fast-path (existing row, same artifact_id) stats
//     the referenced file before declaring Reused=true. If the file is
//     missing (manual deletion, partial cleanup, half-state from a
//     prior crash), the stasher re-writes the content at the row's
//     storage_path so the pointer envelope stays addressable. If the
//     stat returns a non-not-exist error or the re-write fails, the
//     stasher returns an error and the broker falls back to inline
//     shipping rather than emitting an unverifiable pointer.
//   - Concurrent stashes of the same (session, slot, content) tuple
//     race on the deterministic artifact_id. Exactly one INSERT wins;
//     the loser hits a constraint error. The loser re-reads the row
//     and, on success, applies the same disk-presence check as the
//     idempotency fast-path before returning Reused=true. The loser's
//     pending FS write is left in place since it is content-identical
//     to the winner's (no cleanup needed).
package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/hollis-labs/nanite/internal/config"
	"github.com/hollis-labs/nanite/internal/contextbroker"
	"github.com/hollis-labs/nanite/internal/pathsafe"
	"github.com/hollis-labs/nanite/internal/store"
)

// SlotStashOrigin is the artifact origin string used for context-broker
// slot stashes. Distinguishes them from uploaded/placed/auto artifacts so
// downstream consumers (e.g. `ListArtifactsByOrigin`, future FE filters)
// can identify stash entries as server-side cache rows rather than
// user-facing files.
//
// Note (CW-20260512-0110 reviewer feedback): the right-rail Artifacts
// panel currently fetches all session artifacts via
// `GET /api/sessions/{id}/artifacts` without origin filtering, so
// `origin="stashed"` rows surface alongside user-uploaded files today.
// Hiding stash rows by default is tracked as a follow-up (FE filter or
// BE default-exclude with opt-in query param) — see the implementer
// report's follow-up section.
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

	// Resolve the storage directory for this session under the artifacts
	// root. pathsafe.ResolveUnder guards against a malicious or buggy
	// session_id that contains separators or `..` — same guard the
	// upload handler uses.
	//
	// Done before the idempotency fast-path so the disk-loss recovery
	// branch below (existing row but missing file) can re-write to the
	// same canonical path.
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

	// Idempotency: if an artifact with this ID already exists, return
	// reused=true. The content is content-addressed so the existing
	// row's storage_path should hold identical bytes. BUT — reviewer
	// feedback (CW-20260512-0110 Comment 1): a stale row with the
	// referenced file missing (manual deletion, partial cleanup, half-
	// state from a prior crash) would mean the broker emits a pointer
	// that resolves to a non-existent file at dev_read time. Stat the
	// file before declaring "reused"; if missing, re-write the bytes at
	// the same path (idempotent — content-addressed → identical bytes)
	// so the pointer envelope remains addressable. The DB row is left
	// untouched in the recovery case.
	if existing, err := a.store.GetArtifact(artifactID); err == nil && existing != nil {
		recoveryPath := existing.StoragePath
		if recoveryPath == "" {
			recoveryPath = storagePath
		}
		if _, statErr := os.Stat(recoveryPath); statErr == nil {
			return contextbroker.StashResult{ArtifactID: artifactID, Reused: true}, nil
		} else if !errors.Is(statErr, os.ErrNotExist) {
			// Stat failed for a non-not-exist reason (e.g. permission).
			// Treat as stash failure so the broker falls back to inline
			// shipping rather than emitting a pointer we can't verify.
			return contextbroker.StashResult{}, fmt.Errorf("stat existing stash file: %w", statErr)
		}
		// File missing: re-write content-identically at the stored path
		// (or the canonical path if the row had none) and return reused.
		// If re-write fails, treat as a stash failure so the broker
		// falls back to inline shipping rather than emitting a pointer
		// that doesn't resolve.
		if err := writeStashFile(recoveryPath, req.Content); err != nil {
			return contextbroker.StashResult{}, fmt.Errorf("resurrect missing stash file: %w", err)
		}
		slog.Debug("artifact stasher: resurrected missing stash file",
			"artifact_id", artifactID,
			"slot", req.SlotName,
			"session_id", req.SessionID,
			"path", filepath.Base(recoveryPath),
		)
		return contextbroker.StashResult{ArtifactID: artifactID, Reused: true}, nil
	}

	// FS write first (no DB row yet). If this fails, no orphans are
	// created and the broker falls back to inline shipping. Stream the
	// content rather than allocating a full []byte copy (reviewer
	// feedback CW-20260512-0110 Comment 5 — multi-MB slot bodies can
	// otherwise spike memory by an extra full-size copy).
	if err := writeStashFile(storagePath, req.Content); err != nil {
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
		// Reviewer feedback (CW-20260512-0110 Comment 2): the
		// deterministic artifact_id means two concurrent stashes of the
		// same (session, slot, content) tuple race on INSERT. Both
		// callers may miss the pre-check above and exactly one INSERT
		// wins; the loser sees a PRIMARY KEY / UNIQUE constraint error.
		// Without recovery, the broker falls back to inline shipping
		// even though the artifact is already addressable — defeating
		// the idempotency/atomicity contract.
		//
		// On any error: re-read the row by ID. If present, the winning
		// goroutine already persisted it; re-apply the disk-presence
		// check from the idempotency fast-path above (resurrect on
		// missing file) and return Reused=true. Our pending FS write
		// (the one *this* goroutine performed before INSERT) is left
		// in place since it's content-identical to the winner's — no
		// cleanup needed.
		if existing, getErr := a.store.GetArtifact(artifactID); getErr == nil && existing != nil {
			recoveryPath := existing.StoragePath
			if recoveryPath == "" {
				recoveryPath = storagePath
			}
			_, statErr := os.Stat(recoveryPath)
			switch {
			case statErr == nil:
				slog.Debug("artifact stasher: lost insert race, returning reused",
					"artifact_id", artifactID,
					"slot", req.SlotName,
					"session_id", req.SessionID,
					"insert_err", err,
				)
				return contextbroker.StashResult{ArtifactID: artifactID, Reused: true}, nil
			case errors.Is(statErr, os.ErrNotExist):
				// Winner's row landed but its FS file is gone — resurrect
				// at the recovery path (which is the row's storage_path,
				// or our canonical path as fallback). This is the
				// concurrency variant of the Comment 1 disk-loss path.
				if writeErr := writeStashFile(recoveryPath, req.Content); writeErr != nil {
					return contextbroker.StashResult{}, fmt.Errorf("resurrect after lost insert race: %w", writeErr)
				}
				slog.Debug("artifact stasher: lost insert race + resurrected missing file",
					"artifact_id", artifactID,
					"slot", req.SlotName,
					"session_id", req.SessionID,
				)
				return contextbroker.StashResult{ArtifactID: artifactID, Reused: true}, nil
			default:
				// Non-NotExist stat error on a row that exists — treat as
				// real failure rather than silently shipping a pointer we
				// can't verify.
				return contextbroker.StashResult{}, fmt.Errorf("stat after lost insert race: %w", statErr)
			}
		}
		// Genuine non-conflict INSERT failure AND no row exists. Best-
		// effort cleanup of the orphaned file we just wrote, then
		// surface the error so the broker falls back to inline shipping.
		if rmErr := os.Remove(storagePath); rmErr != nil && !errors.Is(rmErr, os.ErrNotExist) {
			slog.Warn("artifact stasher: orphaned file cleanup failed",
				"artifact_id", artifactID,
				"path", storagePath,
				"err", rmErr,
			)
		}
		// Sanity log: surface unexpected error classes (e.g. malformed
		// schema, panic mid-insert) so operators see the unusual case
		// rather than silently degrading to inline ship. The string
		// "constraint" check below is informational only — recovery
		// already attempted above via GetArtifact.
		if strings.Contains(err.Error(), "constraint") {
			slog.Warn("artifact stasher: constraint error without recoverable row — investigate",
				"artifact_id", artifactID,
				"err", err,
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

// writeStashFile streams content to path, preserving 0644 permissions
// without forcing the extra string→[]byte allocation that os.WriteFile
// requires. Multi-MB slot bodies otherwise spike memory by a full-size
// copy at the call site. Truncates if the file already exists (matches
// os.WriteFile semantics).
//
// Reviewer feedback (CW-20260512-0110 Comment 5): the original
// implementation used os.WriteFile(path, []byte(content), 0o644) which
// forces an extra full-size allocation+copy of the stashed body. With
// io.WriteString, the runtime can write the underlying string bytes
// directly through *os.File's io.StringWriter implementation.
//
// Note on durability: the artifactStasher's contract is "FS-first, DB-
// second, idempotency re-write reconciles" — see the package-level
// atomicity discussion. We intentionally do NOT f.Sync() here because:
//   (a) a crash between FS-write and DB-insert produces an orphan file
//       that the next stash of the same content rewrites identically
//       (orphans are harmless), and
//   (b) a crash after DB-insert leaves both file and row consistent on
//       any reasonable filesystem with default journaling.
// Adding fsync would impose a per-stash latency cost without changing
// the recovery semantics the broker relies on.
func writeStashFile(path, content string) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	if _, err := io.WriteString(f, content); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}
