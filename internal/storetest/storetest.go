// Package storetest provides isolated, fully migrated SQLite stores for tests.
package storetest

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/hollis-labs/nanite/internal/store"
)

var (
	templateOnce sync.Once
	templatePath string
	templateErr  error
)

// New opens a Store at dbPath using a fully migrated template when dbPath is
// missing or empty. Existing nonempty databases are left untouched so tests
// that exercise reopen and migration behavior retain their original setup.
//
// Every destination is a separate file chosen by the caller. New still calls
// store.New after copying so normal connection setup, pragmas,
// migration-ledger checks, and backfills run for every Store instance.
func New(t testing.TB, ctx context.Context, dbPath string) (*store.Store, error) {
	t.Helper()
	if err := prepare(dbPath); err != nil {
		return nil, fmt.Errorf("prepare test store: %w", err)
	}
	return store.New(ctx, dbPath)
}

func prepare(dbPath string) error {
	info, err := os.Stat(dbPath)
	switch {
	case err == nil && info.Size() > 0:
		return nil
	case err == nil:
	case os.IsNotExist(err):
	default:
		return fmt.Errorf("stat destination: %w", err)
	}

	src, err := migratedTemplate()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		return fmt.Errorf("create destination directory: %w", err)
	}
	if err := copyFile(dbPath, src); err != nil {
		return fmt.Errorf("copy migrated template: %w", err)
	}
	return nil
}

func migratedTemplate() (string, error) {
	templateOnce.Do(func() {
		dir, err := os.MkdirTemp("", "nanite-store-template-*")
		if err != nil {
			templateErr = err
			return
		}
		templatePath = filepath.Join(dir, "template.db")
		s, err := store.New(context.Background(), templatePath)
		if err != nil {
			templateErr = err
			return
		}
		if err := s.Close(context.Background()); err != nil {
			templateErr = err
		}
	})
	if templateErr != nil {
		return "", fmt.Errorf("create migrated template: %w", templateErr)
	}
	return templatePath, nil
}

func copyFile(dst, src string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	info, err := in.Stat()
	if err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, info.Mode())
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	if err := os.Chmod(dst, info.Mode()); err != nil {
		return fmt.Errorf("chmod copied file: %w", err)
	}
	return nil
}
