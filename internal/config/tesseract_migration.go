package config

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
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
	if _, err := os.Stat(targetDB); err == nil {
		return LegacyMigrationCurrent, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return LegacyMigrationNone, fmt.Errorf("tesseract legacy migration: inspect target DB: %w", err)
	}
	sourceDBInfo, err := os.Lstat(sourceDB)
	if errors.Is(err, os.ErrNotExist) {
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

	if mkdirErr := os.MkdirAll(filepath.Dir(targetDB), 0o700); mkdirErr != nil {
		return LegacyMigrationNone, fmt.Errorf("tesseract legacy migration: create DB directory: %w", mkdirErr)
	}
	if mkdirErr := os.MkdirAll(filepath.Dir(targetRecords), 0o700); mkdirErr != nil {
		return LegacyMigrationNone, fmt.Errorf("tesseract legacy migration: create records parent: %w", mkdirErr)
	}

	want := legacyMigrationJournal{
		SourceDB: sourceDB, SourceRecords: sourceRecords,
		TargetDB: targetDB, TargetRecords: targetRecords,
	}
	journalPath := filepath.Join(filepath.Dir(targetDB), ".nanite-legacy-conduit-migration.json")
	resumed, err := loadOrCreateMigrationJournal(journalPath, want, targetRecords)
	if err != nil {
		return LegacyMigrationNone, err
	}

	if sourceRecordsPresent {
		if targetInfo, targetErr := os.Lstat(targetRecords); errors.Is(targetErr, os.ErrNotExist) {
			if err := copyDirAtomic(sourceRecords, targetRecords); err != nil {
				return LegacyMigrationNone, fmt.Errorf("tesseract legacy migration: copy records: %w", err)
			}
		} else if targetErr != nil {
			return LegacyMigrationNone, fmt.Errorf("tesseract legacy migration: inspect target records: %w", targetErr)
		} else if !targetInfo.IsDir() {
			return LegacyMigrationNone, fmt.Errorf("tesseract legacy migration: target records is not a directory: %s", targetRecords)
		}
	}

	// WAL/SHM are part of a SQLite snapshot when present. Publish them first;
	// targetDB is linked last and is the only completion signal consumers use.
	for _, suffix := range []string{"-wal", "-shm"} {
		if err := copyFileIfPresentNoReplace(sourceDB+suffix, targetDB+suffix); err != nil {
			return LegacyMigrationNone, fmt.Errorf("tesseract legacy migration: copy SQLite sidecar %s: %w", suffix, err)
		}
	}
	if err := copyFileNoReplace(sourceDB, targetDB); err != nil {
		if errors.Is(err, fs.ErrExist) {
			return LegacyMigrationCurrent, nil
		}
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

func loadOrCreateMigrationJournal(path string, want legacyMigrationJournal, targetRecords string) (bool, error) {
	// #nosec G304 -- path is the exact migration journal beside the resolved target DB.
	data, err := os.ReadFile(path)
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
	if _, statErr := os.Stat(targetRecords); statErr == nil {
		return false, fmt.Errorf("tesseract legacy migration: target records exist without a migration journal")
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return false, fmt.Errorf("tesseract legacy migration: inspect target records: %w", statErr)
	}
	data, err = json.Marshal(want)
	if err != nil {
		return false, fmt.Errorf("tesseract legacy migration: encode journal: %w", err)
	}
	if err := writeFileAtomic(path, data, 0o600); err != nil {
		return false, fmt.Errorf("tesseract legacy migration: write journal: %w", err)
	}
	return false, nil
}

func writeFileAtomic(path string, data []byte, mode fs.FileMode) error {
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
	return os.Rename(tmpName, path)
}

func copyDirAtomic(source, target string) error {
	tmp, err := os.MkdirTemp(filepath.Dir(target), ".nanite-conduit-records-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(tmp) }()
	if err := copyDirContents(source, tmp); err != nil {
		return err
	}
	if err := os.Rename(tmp, target); err != nil {
		if _, statErr := os.Stat(target); statErr == nil {
			return nil
		}
		return err
	}
	return nil
}

func copyDirContents(source, target string) error {
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
			if err := os.Mkdir(targetPath, info.Mode().Perm()); err != nil {
				return err
			}
			if err := copyDirContents(sourcePath, targetPath); err != nil {
				return err
			}
			continue
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("refusing non-regular file %s", sourcePath)
		}
		if err := copyFileNoReplace(sourcePath, targetPath); err != nil {
			return err
		}
	}
	return nil
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
