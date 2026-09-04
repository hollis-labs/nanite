package config

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/hollis-labs/go-sqlite/sqlitekit"
	"golang.org/x/sys/unix"

	// Register the SQLite driver used by sqlitekit's read-only validator.
	_ "modernc.org/sqlite"
)

// LegacyMigrationResult describes whether Nanite activated a legacy
// ~/.conduit store at Tesseract's XDG paths.
type LegacyMigrationResult string

const (
	LegacyMigrationNone    LegacyMigrationResult = "no_legacy_store"
	LegacyMigrationCurrent LegacyMigrationResult = "destination_already_current"
	LegacyMigrationCopied  LegacyMigrationResult = "copied"
	LegacyMigrationResumed LegacyMigrationResult = "resumed"
)

type legacyMigrationJournal struct {
	SourceDB      string `json:"source_db"`
	SourceRecords string `json:"source_records"`
	TargetDB      string `json:"target_db"`
	TargetRecords string `json:"target_records"`
}

// MigrateLegacyTesseractData safely copies Nanite's legacy embedded store from
// ~/.conduit into Tesseract's resolved XDG layout. The old tree is never
// modified. Records and SQLite sidecars are published before the main DB, so
// the DB's appearance is the activation point and a failed copy cannot be
// mistaken for a complete new store.
//
// Operators must stop writers before migration. A journal makes a copy
// interrupted after publishing records resumable and makes a pre-existing,
// unrelated records directory fail closed instead of merging datasets.
func MigrateLegacyTesseractData(homeDir, targetDB, targetRecords string) (LegacyMigrationResult, error) {
	sourceDB := filepath.Join(homeDir, ".conduit", "data", "index", "context.db")
	sourceRecords := filepath.Join(homeDir, ".conduit", "data", "records")
	return migrateLegacyTesseractData(sourceDB, sourceRecords, targetDB, targetRecords)
}

func migrateLegacyTesseractData(sourceDB, sourceRecords, targetDB, targetRecords string) (LegacyMigrationResult, error) {
	if targetDB == "" || targetRecords == "" {
		return LegacyMigrationNone, fmt.Errorf("tesseract legacy migration: target DB and records paths are required")
	}
	if mkdirErr := os.MkdirAll(filepath.Dir(targetDB), 0o700); mkdirErr != nil {
		return LegacyMigrationNone, fmt.Errorf("tesseract legacy migration: create DB directory: %w", mkdirErr)
	}
	if mkdirErr := os.MkdirAll(filepath.Dir(targetRecords), 0o700); mkdirErr != nil {
		return LegacyMigrationNone, fmt.Errorf("tesseract legacy migration: create records parent: %w", mkdirErr)
	}

	// An advisory lock beside the target DB serializes migration attempts in
	// this and other Nanite processes. The lock file is intentionally retained:
	// flock state is kernel-owned, so crashes release it without stale-lock
	// recovery, while O_NOFOLLOW makes a planted lock symlink fail closed.
	unlock, err := acquireLegacyMigrationLock(filepath.Join(filepath.Dir(targetDB), ".nanite-legacy-conduit-migration.lock"))
	if err != nil {
		return LegacyMigrationNone, fmt.Errorf("tesseract legacy migration: acquire lock: %w", err)
	}
	defer unlock()

	journalPath := filepath.Join(filepath.Dir(targetDB), ".nanite-legacy-conduit-migration.json")
	want := legacyMigrationJournal{
		SourceDB: sourceDB, SourceRecords: sourceRecords,
		TargetDB: targetDB, TargetRecords: targetRecords,
	}
	targetDBExists := false
	if targetInfo, statErr := os.Lstat(targetDB); statErr == nil {
		if !targetInfo.Mode().IsRegular() {
			return LegacyMigrationNone, fmt.Errorf("tesseract legacy migration: target DB is not a regular file: %s", targetDB)
		}
		targetDBExists = true
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return LegacyMigrationNone, fmt.Errorf("tesseract legacy migration: inspect target DB: %w", statErr)
	}
	journalPresent, err := loadMigrationJournal(journalPath, want)
	if err != nil {
		return LegacyMigrationNone, err
	}
	// A target DB with no journal predates this attempt and remains
	// authoritative. A surviving journal means an earlier activation may have
	// crashed or collided; validate the complete pair below instead of
	// converting that collision into destination_already_current.
	if targetDBExists && !journalPresent {
		if validateErr := validateInitializedTesseractDB(targetDB); validateErr != nil {
			return LegacyMigrationNone, fmt.Errorf("tesseract legacy migration: target DB is not a valid initialized Tesseract store: %w", validateErr)
		}
		return LegacyMigrationCurrent, nil
	}
	sourceDBInfo, err := os.Lstat(sourceDB)
	if errors.Is(err, os.ErrNotExist) {
		partialTargets, inspectErr := legacyMigrationPartialTargets(targetDB, targetRecords)
		if inspectErr != nil {
			return LegacyMigrationNone, inspectErr
		}
		if journalPresent {
			detail := ""
			if len(partialTargets) != 0 {
				detail = "; partial targets: " + strings.Join(partialTargets, ", ")
			}
			return LegacyMigrationNone, fmt.Errorf("tesseract legacy migration: source DB is missing while migration journal remains%s", detail)
		}
		if len(partialTargets) != 0 {
			return LegacyMigrationNone, fmt.Errorf("tesseract legacy migration: source DB is missing while partial target state exists: %s", strings.Join(partialTargets, ", "))
		}
		return LegacyMigrationNone, nil
	} else if err != nil {
		return LegacyMigrationNone, fmt.Errorf("tesseract legacy migration: inspect source DB: %w", err)
	}
	if !sourceDBInfo.Mode().IsRegular() {
		return LegacyMigrationNone, fmt.Errorf("tesseract legacy migration: source DB is not a regular file: %s", sourceDB)
	}
	sourceRecordsInfo, recordsErr := os.Lstat(sourceRecords)
	sourceRecordsPresent := recordsErr == nil
	if recordsErr != nil && !errors.Is(recordsErr, os.ErrNotExist) {
		return LegacyMigrationNone, fmt.Errorf("tesseract legacy migration: inspect source records: %w", recordsErr)
	}
	if sourceRecordsPresent && !sourceRecordsInfo.IsDir() {
		return LegacyMigrationNone, fmt.Errorf("tesseract legacy migration: source records is not a directory: %s", sourceRecords)
	}

	resumed, err := loadOrCreateMigrationJournal(journalPath, want, targetRecords)
	if err != nil {
		return LegacyMigrationNone, err
	}

	if sourceRecordsPresent {
		if err := copyDirResumable(sourceRecords, targetRecords, resumed); err != nil {
			return LegacyMigrationNone, fmt.Errorf("tesseract legacy migration: copy records: %w", err)
		}
	} else if targetInfo, targetErr := os.Lstat(targetRecords); targetErr == nil {
		return LegacyMigrationNone, fmt.Errorf("tesseract legacy migration: target records exist but legacy source has none (%s, mode %s)", targetRecords, targetInfo.Mode())
	} else if !errors.Is(targetErr, os.ErrNotExist) {
		return LegacyMigrationNone, fmt.Errorf("tesseract legacy migration: inspect target records: %w", targetErr)
	}

	// WAL/SHM are part of a SQLite snapshot when present. Publish them first;
	// targetDB is linked last and is the only completion signal consumers use.
	for _, suffix := range []string{"-wal", "-shm"} {
		if err := copySQLiteSidecar(sourceDB+suffix, targetDB+suffix); err != nil {
			return LegacyMigrationNone, fmt.Errorf("tesseract legacy migration: copy SQLite sidecar %s: %w", suffix, err)
		}
	}
	activate := copyFileNoReplace
	if targetDBExists {
		activate = copyFileIfPresentNoReplace
	}
	if err := activate(sourceDB, targetDB); err != nil {
		return LegacyMigrationNone, fmt.Errorf("tesseract legacy migration: activate DB: %w", err)
	}
	if err := os.Remove(journalPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return LegacyMigrationNone, fmt.Errorf("tesseract legacy migration: remove completed journal: %w", err)
	}
	if resumed {
		return LegacyMigrationResumed, nil
	}
	return LegacyMigrationCopied, nil
}

// validateInitializedTesseractDB distinguishes an authoritative existing
// Tesseract store from a regular file left by a partial copy or unrelated
// SQLite user. OpenReadOnly prevents this validation from initializing or
// migrating the candidate. quick_check validates the existing SQLite image;
// contiguous version history plus version-gated schema/object probes provide
// Tesseract's durable identity while still allowing an older initialized store
// to be upgraded by tesseract.Open after migration classifies it as current.
func validateInitializedTesseractDB(path string) error {
	ctx := context.Background()
	db, err := sqlitekit.OpenReadOnly(ctx, path, sqlitekit.OpenOptions{})
	if err != nil {
		return fmt.Errorf("open read-only: %w", err)
	}
	defer func() { _ = db.Close() }()

	rows, err := db.QueryContext(ctx, `PRAGMA quick_check(1)`)
	if err != nil {
		return fmt.Errorf("SQLite quick_check: %w", err)
	}
	quickCheckOK := false
	for rows.Next() {
		var result string
		if scanErr := rows.Scan(&result); scanErr != nil {
			_ = rows.Close()
			return fmt.Errorf("scan SQLite quick_check: %w", scanErr)
		}
		if result != "ok" {
			_ = rows.Close()
			return fmt.Errorf("SQLite quick_check: %s", result)
		}
		quickCheckOK = true
	}
	if rowsErr := rows.Err(); rowsErr != nil {
		_ = rows.Close()
		return fmt.Errorf("read SQLite quick_check: %w", rowsErr)
	}
	if closeErr := rows.Close(); closeErr != nil {
		return fmt.Errorf("close SQLite quick_check: %w", closeErr)
	}
	if !quickCheckOK {
		return errors.New("SQLite quick_check returned no result")
	}

	var version int
	if err := db.QueryRowContext(ctx, `SELECT COALESCE(MAX(version), 0) FROM schema_version`).Scan(&version); err != nil {
		return fmt.Errorf("read Tesseract schema version: %w", err)
	}
	if version < 1 {
		return fmt.Errorf("tesseract schema version must be positive, got %d", version)
	}
	var minimumVersion, versionRows int
	if err := db.QueryRowContext(ctx, `SELECT COALESCE(MIN(version), 0), COUNT(*) FROM schema_version`).Scan(&minimumVersion, &versionRows); err != nil {
		return fmt.Errorf("read Tesseract schema history: %w", err)
	}
	if minimumVersion != 1 || versionRows != version {
		return fmt.Errorf("tesseract schema history is incomplete: min=%d max=%d rows=%d", minimumVersion, version, versionRows)
	}

	coreSchemaProbes := []struct {
		minimumVersion int
		name           string
		query          string
	}{
		{minimumVersion: 1, name: "schema_version", query: `SELECT version, applied_at FROM schema_version LIMIT 0`},
		{minimumVersion: 1, name: "records v1", query: `SELECT record_id, namespace, key_name, revision, actor, created_at, file_path FROM records LIMIT 0`},
		{minimumVersion: 1, name: "heads v1", query: `SELECT namespace, key_name, head_revision, head_record_id, updated_at FROM heads LIMIT 0`},
		{minimumVersion: 2, name: "audit_events", query: `SELECT id, event_type, actor, namespace, key_name, revision, created_at FROM audit_events LIMIT 0`},
		{minimumVersion: 3, name: "auth_tokens v3", query: `SELECT token_id, token_hash, label, created_at FROM auth_tokens LIMIT 0`},
		{minimumVersion: 4, name: "namespace_policies", query: `SELECT namespace, owner_type, owner_id, policy_json, updated_at FROM namespace_policies LIMIT 0`},
		{minimumVersion: 5, name: "records v5", query: `SELECT metadata_json FROM records LIMIT 0`},
		{minimumVersion: 5, name: "record_tags", query: `SELECT record_id, tag FROM record_tags LIMIT 0`},
		{minimumVersion: 6, name: "auth_tokens v6", query: `SELECT client_id, scopes, namespace_globs FROM auth_tokens LIMIT 0`},
		{minimumVersion: 7, name: "records v7", query: `SELECT record_type, status, ttl, content_version, pointers_json, provenance_json FROM records LIMIT 0`},
		{minimumVersion: 7, name: "heads v7", query: `SELECT record_type, status FROM heads LIMIT 0`},
		{minimumVersion: 8, name: "embeddings", query: `SELECT record_id, model, dimensions, vector, created_at FROM embeddings LIMIT 0`},
		{minimumVersion: 9, name: "memory_state v9", query: `SELECT memory_id, namespace, memory_key, current_revision, activation, access_count, last_accessed_at, created_at FROM memory_state LIMIT 0`},
		{minimumVersion: 9, name: "memory_revisions v9", query: `SELECT revision_id, memory_id, namespace, memory_key, status, supersedes, created_at, author_agent_id, author_version, "trigger", session_id, origin, confidence, tags, ttl_seconds, expires_at, payload_summary, payload_body, embedding_model, embedding_vector FROM memory_revisions LIMIT 0`},
		{minimumVersion: 10, name: "memory domains", query: `SELECT s.domain, r.domain FROM memory_state AS s, memory_revisions AS r LIMIT 0`},
		{minimumVersion: 11, name: "knowledge facets", query: `SELECT facet_kind, facet_source, facet_pointer_scheme, facet_pointer_locator, facet_pointer_resolved_at FROM memory_revisions LIMIT 0`},
		{minimumVersion: 12, name: "memory FTS v12", query: `SELECT payload_summary, payload_body, tags FROM memory_revisions_fts LIMIT 0`},
		{minimumVersion: 13, name: "pointer_verifications", query: `SELECT id, revision_id, scheme, locator, outcome, checked_at, detail FROM pointer_verifications LIMIT 0`},
		{minimumVersion: 14, name: "decay baseline", query: `SELECT last_decayed_at FROM memory_state LIMIT 0`},
		{minimumVersion: 15, name: "memory FTS v15", query: `SELECT memory_key FROM memory_revisions_fts LIMIT 0`},
	}
	for _, probe := range coreSchemaProbes {
		if version < probe.minimumVersion {
			continue
		}
		statement, prepareErr := db.PrepareContext(ctx, probe.query)
		if prepareErr != nil {
			return fmt.Errorf("validate Tesseract %s schema: %w", probe.name, prepareErr)
		}
		if closeErr := statement.Close(); closeErr != nil {
			return fmt.Errorf("close Tesseract %s schema probe: %w", probe.name, closeErr)
		}
	}
	requiredObjects := []struct {
		minimumVersion int
		objectType     string
		name           string
	}{
		{minimumVersion: 12, objectType: "trigger", name: "memory_revisions_fts_ai"},
		{minimumVersion: 12, objectType: "trigger", name: "memory_revisions_fts_ad"},
		{minimumVersion: 15, objectType: "trigger", name: "memory_revisions_fts_au"},
		{minimumVersion: 16, objectType: "index", name: "idx_memory_revisions_supersedes"},
		{minimumVersion: 16, objectType: "index", name: "idx_pointer_verifications_checked_at"},
	}
	for _, object := range requiredObjects {
		if version < object.minimumVersion {
			continue
		}
		var count int
		if err := db.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM sqlite_master WHERE type = ? AND name = ?`, object.objectType, object.name,
		).Scan(&count); err != nil {
			return fmt.Errorf("validate Tesseract %s %s: %w", object.objectType, object.name, err)
		}
		if count != 1 {
			return fmt.Errorf("tesseract %s %s is missing", object.objectType, object.name)
		}
	}
	return nil
}

func legacyMigrationPartialTargets(targetDB, targetRecords string) ([]string, error) {
	candidates := []struct {
		label string
		path  string
	}{
		{label: "database", path: targetDB},
		{label: "database WAL", path: targetDB + "-wal"},
		{label: "database SHM", path: targetDB + "-shm"},
		{label: "records", path: targetRecords},
	}
	var present []string
	for _, candidate := range candidates {
		info, err := os.Lstat(candidate.path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("tesseract legacy migration: inspect partial target %s: %w", candidate.label, err)
		}
		present = append(present, fmt.Sprintf("%s %s (mode %s)", candidate.label, candidate.path, info.Mode()))
	}
	return present, nil
}

func loadOrCreateMigrationJournal(path string, want legacyMigrationJournal, targetRecords string) (bool, error) {
	resumed, err := loadMigrationJournal(path, want)
	if err != nil || resumed {
		return resumed, err
	}
	if targetInfo, statErr := os.Lstat(targetRecords); statErr == nil {
		if !targetInfo.IsDir() {
			return false, fmt.Errorf("tesseract legacy migration: target records is not a directory: %s", targetRecords)
		}
		return false, fmt.Errorf("tesseract legacy migration: target records exist without a migration journal")
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return false, fmt.Errorf("tesseract legacy migration: inspect target records: %w", statErr)
	}
	data, err := json.Marshal(want)
	if err != nil {
		return false, fmt.Errorf("tesseract legacy migration: encode journal: %w", err)
	}
	if err := writeFileNoReplace(path, data, 0o600); err != nil {
		return false, fmt.Errorf("tesseract legacy migration: write journal: %w", err)
	}
	return false, nil
}

func loadMigrationJournal(path string, want legacyMigrationJournal) (bool, error) {
	data, err := readRegularFileNoFollow(path)
	if err == nil {
		var got legacyMigrationJournal
		if decodeErr := json.Unmarshal(data, &got); decodeErr != nil {
			return false, fmt.Errorf("tesseract legacy migration: decode journal: %w", decodeErr)
		}
		if got != want {
			return false, fmt.Errorf("tesseract legacy migration: journal paths do not match current migration")
		}
		return true, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return false, fmt.Errorf("tesseract legacy migration: read journal: %w", err)
	}
	return false, nil
}

func writeFileNoReplace(path string, data []byte, mode fs.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".nanite-migration-journal-")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Link(tmpName, path)
}

// copyDirResumable claims a new records root with mkdir (which cannot replace
// a raced directory), or resumes only when a matching journal was loaded.
// Individual files use no-replace publication and the completed trees are
// compared before the database activation point.
func copyDirResumable(source, target string, resumed bool) error {
	sourceInfo, err := os.Lstat(source)
	if err != nil {
		return err
	}
	if !sourceInfo.IsDir() {
		return fmt.Errorf("source records is not a directory: %s", source)
	}
	targetInfo, targetErr := os.Lstat(target)
	switch {
	case errors.Is(targetErr, os.ErrNotExist):
		if mkdirErr := os.Mkdir(target, sourceInfo.Mode().Perm()); mkdirErr != nil {
			return mkdirErr
		}
	case targetErr != nil:
		return targetErr
	case !targetInfo.IsDir():
		return fmt.Errorf("target records is not a directory: %s", target)
	case !resumed:
		return fmt.Errorf("target records appeared during migration: %s", target)
	}
	if copyErr := copyDirContents(source, target, resumed); copyErr != nil {
		return copyErr
	}
	equal, err := dirsHaveEqualContents(source, target)
	if err != nil {
		return err
	}
	if !equal {
		return fmt.Errorf("published target records differ from legacy source: %s", target)
	}
	return nil
}

func copyDirContents(source, target string, resumed bool) error {
	entries, err := os.ReadDir(source)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		sourcePath := filepath.Join(source, entry.Name())
		targetPath := filepath.Join(target, entry.Name())
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("refusing symlink %s", sourcePath)
		}
		if info.IsDir() {
			targetInfo, targetErr := os.Lstat(targetPath)
			switch {
			case errors.Is(targetErr, os.ErrNotExist):
				if err := os.Mkdir(targetPath, info.Mode().Perm()); err != nil {
					return err
				}
			case targetErr != nil:
				return targetErr
			case !targetInfo.IsDir():
				return fmt.Errorf("target records entry is not a directory: %s", targetPath)
			case !resumed:
				return fmt.Errorf("target records entry appeared during migration: %s", targetPath)
			}
			if err := copyDirContents(sourcePath, targetPath, resumed); err != nil {
				return err
			}
			continue
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("refusing non-regular file %s", sourcePath)
		}
		copyFile := copyFileNoReplace
		if resumed {
			copyFile = copyFileIfPresentNoReplace
		}
		if err := copyFile(sourcePath, targetPath); err != nil {
			return err
		}
	}
	return nil
}

func dirsHaveEqualContents(left, right string) (bool, error) {
	leftEntries, err := os.ReadDir(left)
	if err != nil {
		return false, err
	}
	rightEntries, err := os.ReadDir(right)
	if err != nil {
		return false, err
	}
	if len(leftEntries) != len(rightEntries) {
		return false, nil
	}
	for i, leftEntry := range leftEntries {
		rightEntry := rightEntries[i]
		if leftEntry.Name() != rightEntry.Name() {
			return false, nil
		}
		leftInfo, err := leftEntry.Info()
		if err != nil {
			return false, err
		}
		rightInfo, err := rightEntry.Info()
		if err != nil {
			return false, err
		}
		if leftInfo.Mode()&os.ModeSymlink != 0 || rightInfo.Mode()&os.ModeSymlink != 0 {
			return false, fmt.Errorf("refusing symlink while comparing records: %s or %s", filepath.Join(left, leftEntry.Name()), filepath.Join(right, rightEntry.Name()))
		}
		if leftInfo.IsDir() != rightInfo.IsDir() || leftInfo.Mode().IsRegular() != rightInfo.Mode().IsRegular() {
			return false, nil
		}
		leftPath := filepath.Join(left, leftEntry.Name())
		rightPath := filepath.Join(right, rightEntry.Name())
		if leftInfo.IsDir() {
			equal, compareErr := dirsHaveEqualContents(leftPath, rightPath)
			if compareErr != nil || !equal {
				return equal, compareErr
			}
			continue
		}
		if !leftInfo.Mode().IsRegular() || leftInfo.Size() != rightInfo.Size() {
			return false, nil
		}
		equal, err := filesHaveEqualSHA256(leftPath, rightPath)
		if err != nil || !equal {
			return equal, err
		}
	}
	return true, nil
}

func copySQLiteSidecar(source, target string) error {
	_, sourceErr := os.Lstat(source)
	if errors.Is(sourceErr, os.ErrNotExist) {
		if targetInfo, targetErr := os.Lstat(target); targetErr == nil {
			return fmt.Errorf("target sidecar exists without legacy source: %s (mode %s)", target, targetInfo.Mode())
		} else if !errors.Is(targetErr, os.ErrNotExist) {
			return targetErr
		}
		return nil
	}
	if sourceErr != nil {
		return sourceErr
	}
	return copyFileIfPresentNoReplace(source, target)
}

func acquireLegacyMigrationLock(path string) (func(), error) {
	fd, err := unix.Open(path, unix.O_CREAT|unix.O_RDWR|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0o600)
	if err != nil {
		return nil, err
	}
	// #nosec G115 -- unix.Open returned a valid native file descriptor; the
	// conversion is the exact representation os.NewFile requires.
	file := os.NewFile(uintptr(fd), path)
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, err
	}
	if !info.Mode().IsRegular() {
		_ = file.Close()
		return nil, fmt.Errorf("lock is not a regular file: %s", path)
	}
	if err := unix.Flock(fd, unix.LOCK_EX); err != nil {
		_ = file.Close()
		return nil, err
	}
	return func() {
		_ = unix.Flock(fd, unix.LOCK_UN)
		_ = file.Close()
	}, nil
}

func readRegularFileNoFollow(path string) ([]byte, error) {
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, err
	}
	// #nosec G115 -- unix.Open returned a valid native file descriptor; the
	// conversion is the exact representation os.NewFile requires.
	file := os.NewFile(uintptr(fd), path)
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("file is not regular: %s", path)
	}
	return io.ReadAll(file)
}

func copyFileIfPresentNoReplace(source, target string) error {
	sourceInfo, err := os.Lstat(source)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	if !sourceInfo.Mode().IsRegular() {
		return fmt.Errorf("source is not a regular file: %s", source)
	}
	targetInfo, err := os.Lstat(target)
	if err == nil {
		if !targetInfo.Mode().IsRegular() {
			return fmt.Errorf("target is not a regular file: %s", target)
		}
		if sourceInfo.Size() != targetInfo.Size() {
			return fmt.Errorf("existing target differs from source: %s", target)
		}
		equal, compareErr := filesHaveEqualSHA256(source, target)
		if compareErr != nil {
			return compareErr
		}
		if !equal {
			return fmt.Errorf("existing target differs from source: %s", target)
		}
		return nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return copyFileNoReplace(source, target)
}

func filesHaveEqualSHA256(left, right string) (bool, error) {
	digest := func(path string) ([sha256.Size]byte, error) {
		// #nosec G304 -- callers pass validated migration source/target paths.
		file, err := os.Open(path)
		if err != nil {
			return [sha256.Size]byte{}, err
		}
		defer func() { _ = file.Close() }()
		hash := sha256.New()
		if _, err := io.Copy(hash, file); err != nil {
			return [sha256.Size]byte{}, err
		}
		var sum [sha256.Size]byte
		copy(sum[:], hash.Sum(nil))
		return sum, nil
	}
	leftSum, err := digest(left)
	if err != nil {
		return false, err
	}
	rightSum, err := digest(right)
	if err != nil {
		return false, err
	}
	return leftSum == rightSum, nil
}

func copyFileNoReplace(source, target string) error {
	info, err := os.Lstat(source)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("source is not a regular file: %s", source)
	}
	// #nosec G304 -- source was lstat-validated as a regular migration file.
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	tmp, err := os.CreateTemp(filepath.Dir(target), ".nanite-conduit-file-")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()
	if err := tmp.Chmod(info.Mode().Perm()); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := io.Copy(tmp, in); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Link(tmpName, target)
}
