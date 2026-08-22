package api

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
	apiTestStoreTemplateOnce sync.Once
	apiTestStoreTemplatePath string
	apiTestStoreTemplateErr  error
)

// prepareAPIStoreDB copies a blank, fully migrated database to dbPath. Tests
// still call store.New afterward so its normal connection setup, pragmas,
// migration-ledger checks, and backfills run for every Store instance.
func prepareAPIStoreDB(t *testing.T, dbPath string) {
	t.Helper()
	if err := copyAPIStoreTemplate(dbPath, apiTestStoreTemplate(t)); err != nil {
		t.Fatalf("copy API test store template: %v", err)
	}
}

func apiTestStoreTemplate(t *testing.T) string {
	t.Helper()
	apiTestStoreTemplateOnce.Do(func() {
		dir, err := os.MkdirTemp("", "nanite-api-store-template-*")
		if err != nil {
			apiTestStoreTemplateErr = err
			return
		}
		apiTestStoreTemplatePath = filepath.Join(dir, "template.db")
		s, err := store.New(context.Background(), apiTestStoreTemplatePath)
		if err != nil {
			apiTestStoreTemplateErr = err
			return
		}
		if err := s.Close(context.Background()); err != nil {
			apiTestStoreTemplateErr = err
			return
		}
	})
	if apiTestStoreTemplateErr != nil {
		t.Fatalf("create API test store template: %v", apiTestStoreTemplateErr)
	}
	return apiTestStoreTemplatePath
}

func copyAPIStoreTemplate(dst, src string) error {
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
		return fmt.Errorf("chmod copied API test store: %w", err)
	}
	return nil
}
