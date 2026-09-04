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
	"strings"

	"golang.org/x/sys/unix"
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
