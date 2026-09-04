package config

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/hollis-labs/tesseract"
	tesseractMemory "github.com/hollis-labs/tesseract/memory"
)

func TestMigrateLegacyTesseractDataContinuityRestartAndIdempotence(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	legacyRoot := filepath.Join(home, ".conduit")
	legacy, openErr := tesseract.Open(ctx, tesseract.Config{RootDir: legacyRoot})
	if openErr != nil {
		t.Fatalf("open legacy: %v", openErr)
	}
	namespace := "user/migration/memory/notes"
	if _, writeErr := legacy.MemoryStore().WriteRevision(ctx, tesseractMemory.WriteInput{
		Domain: tesseractMemory.DomainMemory, Namespace: namespace, MemoryKey: "continuity",
		Status:  tesseractMemory.StatusReviewed,
		Author:  tesseractMemory.Author{AgentID: "migration-test", AgentVersion: "1"},
		Trigger: tesseractMemory.TriggerManual, SessionID: "migration-test",
		Origin: tesseractMemory.OriginUser, Confidence: 0.9,
		Payload: tesseractMemory.Payload{Summary: "survives migration", Body: "full body"},
	}); writeErr != nil {
		_ = legacy.Close()
		t.Fatalf("seed legacy: %v", writeErr)
	}
	if closeErr := legacy.Close(); closeErr != nil {
		t.Fatalf("close legacy: %v", closeErr)
	}

	targetDB := filepath.Join(t.TempDir(), "data", "main.db")
	targetRecords := filepath.Join(t.TempDir(), "state", "records")
	result, err := MigrateLegacyTesseractData(home, targetDB, targetRecords)
	if err != nil || result != LegacyMigrationCopied {
		t.Fatalf("migration result=%q err=%v", result, err)
	}
	legacyDB := filepath.Join(legacyRoot, "data", "index", "context.db")
	if _, statErr := os.Stat(legacyDB); statErr != nil {
		t.Fatalf("legacy source was not preserved: %v", statErr)
	}

	verify := func(label string) {
		t.Helper()
		instance, targetOpenErr := tesseract.Open(ctx, tesseract.Config{
			RootDir: filepath.Join(t.TempDir(), "inert"), DBPath: targetDB, RecordsDir: targetRecords,
		})
		if targetOpenErr != nil {
			t.Fatalf("%s open target: %v", label, targetOpenErr)
		}
		page, recallErr := instance.MemoryStore().RecallPaged(ctx, tesseractMemory.RecallInput{
			Namespaces: []string{namespace}, Ranking: tesseractMemory.RankingChronological,
			Filters: tesseractMemory.RecallFilters{Domains: []tesseractMemory.Domain{tesseractMemory.DomainMemory}},
		}, tesseractMemory.PageRequest{Limit: 10, PayloadMode: tesseractMemory.PayloadModeFull})
		if recallErr != nil {
			_ = instance.Close()
			t.Fatalf("%s recall: %v", label, recallErr)
		}
		if len(page.Kept) != 1 || page.Kept[0].Revision.Payload.Body != "full body" {
			_ = instance.Close()
			t.Fatalf("%s migrated page = %+v", label, page)
		}
		if targetCloseErr := instance.Close(); targetCloseErr != nil {
			t.Fatalf("%s close target: %v", label, targetCloseErr)
		}
	}
	verify("first")
	verify("restart")

	result, err = MigrateLegacyTesseractData(home, targetDB, targetRecords)
	if err != nil || result != LegacyMigrationCurrent {
		t.Fatalf("idempotent result=%q err=%v", result, err)
	}
}

func TestMigrateLegacyTesseractDataResumesPublishedRecords(t *testing.T) {
	root := t.TempDir()
	sourceDB := filepath.Join(root, "legacy", "context.db")
	sourceRecords := filepath.Join(root, "legacy", "records")
	targetDB := filepath.Join(root, "target", "main.db")
	targetRecords := filepath.Join(root, "state", "records")
	for _, dir := range []string{filepath.Dir(sourceDB), sourceRecords, filepath.Dir(targetDB), targetRecords} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(sourceDB, []byte("db"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sourceDB+"-wal", []byte("wal"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(targetDB+"-wal", []byte("wal"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sourceRecords, "record"), []byte("payload"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(targetRecords, "record"), []byte("payload"), 0o600); err != nil {
		t.Fatal(err)
	}
	want := legacyMigrationJournal{SourceDB: sourceDB, SourceRecords: sourceRecords, TargetDB: targetDB, TargetRecords: targetRecords}
	journal := filepath.Join(filepath.Dir(targetDB), ".nanite-legacy-conduit-migration.json")
	data, _ := jsonMarshalForTest(want)
	if err := os.WriteFile(journal, data, 0o600); err != nil {
		t.Fatal(err)
	}

	result, err := migrateLegacyTesseractData(sourceDB, sourceRecords, targetDB, targetRecords)
	if err != nil || result != LegacyMigrationResumed {
		t.Fatalf("resume result=%q err=%v", result, err)
	}
	// #nosec G304 -- targetDB is an exact path inside t.TempDir.
	got, err := os.ReadFile(targetDB)
	if err != nil || string(got) != "db" {
		t.Fatalf("target DB=%q err=%v", got, err)
	}
	// #nosec G304 -- the WAL is the exact sidecar of targetDB inside t.TempDir.
	if got, err := os.ReadFile(targetDB + "-wal"); err != nil || string(got) != "wal" {
		t.Fatalf("resumed WAL=%q err=%v", got, err)
	}
}

func TestMigrateLegacyTesseractDataRefusesUnjournaledTargetRecords(t *testing.T) {
	root := t.TempDir()
	sourceDB := filepath.Join(root, "legacy", "context.db")
	targetDB := filepath.Join(root, "target", "main.db")
	targetRecords := filepath.Join(root, "state", "records")
	for _, dir := range []string{filepath.Dir(sourceDB), filepath.Dir(targetDB), targetRecords} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(sourceDB, []byte("db"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := migrateLegacyTesseractData(sourceDB, filepath.Join(root, "legacy", "records"), targetDB, targetRecords)
	if err == nil {
		t.Fatal("migration unexpectedly merged into unjournaled records")
	}
	if _, statErr := os.Stat(targetDB); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("target DB activated after refusal: %v", statErr)
	}
}

func TestMigrateLegacyTesseractDataRefusesSymlinkedSourceRecords(t *testing.T) {
	root := t.TempDir()
	sourceDB := filepath.Join(root, "legacy", "context.db")
	realRecords := filepath.Join(root, "real-records")
	sourceRecords := filepath.Join(root, "legacy", "records")
	targetDB := filepath.Join(root, "target", "main.db")
	targetRecords := filepath.Join(root, "state", "records")
	for _, dir := range []string{filepath.Dir(sourceDB), realRecords} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(sourceDB, []byte("db"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(realRecords, sourceRecords); err != nil {
		t.Fatal(err)
	}

	_, err := migrateLegacyTesseractData(sourceDB, sourceRecords, targetDB, targetRecords)
	if err == nil {
		t.Fatal("migration unexpectedly followed symlinked source records")
	}
	if _, statErr := os.Stat(targetDB); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("target DB activated after refusal: %v", statErr)
	}
}

func jsonMarshalForTest(value any) ([]byte, error) {
	return json.Marshal(value)
}
