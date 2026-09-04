package config

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
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
	if writeErr := os.WriteFile(journal, data, 0o600); writeErr != nil {
		t.Fatal(writeErr)
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

func TestMigrateLegacyTesseractDataRefusesTargetDBSymlinks(t *testing.T) {
	for _, dangling := range []bool{false, true} {
		t.Run(map[bool]string{false: "existing", true: "dangling"}[dangling], func(t *testing.T) {
			root := t.TempDir()
			sourceDB := filepath.Join(root, "legacy", "context.db")
			targetDB := filepath.Join(root, "target", "main.db")
			targetRecords := filepath.Join(root, "state", "records")
			if err := os.MkdirAll(filepath.Dir(sourceDB), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(filepath.Dir(targetDB), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(sourceDB, []byte("legacy"), 0o600); err != nil {
				t.Fatal(err)
			}
			realTarget := filepath.Join(root, "real-target.db")
			if !dangling {
				if err := os.WriteFile(realTarget, []byte("unrelated"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.Symlink(realTarget, targetDB); err != nil {
				t.Fatal(err)
			}

			if result, err := migrateLegacyTesseractData(sourceDB, filepath.Join(root, "legacy", "records"), targetDB, targetRecords); err == nil {
				t.Fatalf("migration followed target DB symlink: result=%q", result)
			}
		})
	}
}

func TestMigrateLegacyTesseractDataRefusesJournalSymlink(t *testing.T) {
	root := t.TempDir()
	sourceDB := filepath.Join(root, "legacy", "context.db")
	targetDB := filepath.Join(root, "target", "main.db")
	targetRecords := filepath.Join(root, "state", "records")
	for _, dir := range []string{filepath.Dir(sourceDB), filepath.Dir(targetDB)} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(sourceDB, []byte("legacy"), 0o600); err != nil {
		t.Fatal(err)
	}
	want := legacyMigrationJournal{SourceDB: sourceDB, SourceRecords: filepath.Join(root, "legacy", "records"), TargetDB: targetDB, TargetRecords: targetRecords}
	data, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	realJournal := filepath.Join(root, "real-journal.json")
	if err := os.WriteFile(realJournal, data, 0o600); err != nil {
		t.Fatal(err)
	}
	journal := filepath.Join(filepath.Dir(targetDB), ".nanite-legacy-conduit-migration.json")
	if err := os.Symlink(realJournal, journal); err != nil {
		t.Fatal(err)
	}

	if result, err := migrateLegacyTesseractData(sourceDB, want.SourceRecords, targetDB, targetRecords); err == nil {
		t.Fatalf("migration followed journal symlink: result=%q", result)
	}
}

func TestCopyDirResumableRefusesPublishCollision(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source")
	target := filepath.Join(root, "target")
	for _, dir := range []string{source, target} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(source, "record"), []byte("legacy"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "record"), []byte("raced-unrelated"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := copyDirResumable(source, target, false); err == nil {
		t.Fatal("records publish collision was accepted as success")
	}
}

func TestCopyFileNoReplaceRefusesDBActivationCollision(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "legacy.db")
	target := filepath.Join(root, "main.db")
	if err := os.WriteFile(source, []byte("legacy"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("raced-unrelated"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := copyFileNoReplace(source, target); !errors.Is(err, os.ErrExist) {
		t.Fatalf("DB activation collision error = %v, want fs.ErrExist", err)
	}
	// #nosec G304 -- target is an exact path inside t.TempDir.
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "raced-unrelated" {
		t.Fatalf("activation collision overwrote target DB: %q", got)
	}
}

func TestMigrateLegacyTesseractDataDoesNotConvertJournaledDBCollisionToCurrent(t *testing.T) {
	root := t.TempDir()
	sourceDB := filepath.Join(root, "legacy", "context.db")
	targetDB := filepath.Join(root, "target", "main.db")
	targetRecords := filepath.Join(root, "state", "records")
	for _, dir := range []string{filepath.Dir(sourceDB), filepath.Dir(targetDB)} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(sourceDB, []byte("legacy"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(targetDB, []byte("raced-unrelated"), 0o600); err != nil {
		t.Fatal(err)
	}
	want := legacyMigrationJournal{SourceDB: sourceDB, SourceRecords: filepath.Join(root, "legacy", "records"), TargetDB: targetDB, TargetRecords: targetRecords}
	data, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	journal := filepath.Join(filepath.Dir(targetDB), ".nanite-legacy-conduit-migration.json")
	if writeErr := os.WriteFile(journal, data, 0o600); writeErr != nil {
		t.Fatal(writeErr)
	}

	result, err := migrateLegacyTesseractData(sourceDB, want.SourceRecords, targetDB, targetRecords)
	if err == nil {
		t.Fatalf("journaled DB collision became success: result=%q", result)
	}
	if result == LegacyMigrationCurrent {
		t.Fatalf("journaled DB collision became destination current: %q", result)
	}
}

func TestMigrateLegacyTesseractDataSerializesCompetingMigrations(t *testing.T) {
	for iteration := 0; iteration < 20; iteration++ {
		root := t.TempDir()
		targetDB := filepath.Join(root, "target", "main.db")
		targetRecords := filepath.Join(root, "state", "records")
		type source struct {
			db      string
			records string
			marker  string
		}
		sources := []source{
			{db: filepath.Join(root, "legacy-a", "context.db"), records: filepath.Join(root, "legacy-a", "records"), marker: "A"},
			{db: filepath.Join(root, "legacy-b", "context.db"), records: filepath.Join(root, "legacy-b", "records"), marker: "B"},
		}
		for _, source := range sources {
			if err := os.MkdirAll(source.records, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(source.db, []byte("db-"+source.marker), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(source.records, "record"), []byte("record-"+source.marker), 0o600); err != nil {
				t.Fatal(err)
			}
		}

		start := make(chan struct{})
		results := make(chan LegacyMigrationResult, len(sources))
		errs := make(chan error, len(sources))
		var wg sync.WaitGroup
		for _, source := range sources {
			source := source
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				result, err := migrateLegacyTesseractData(source.db, source.records, targetDB, targetRecords)
				results <- result
				errs <- err
			}()
		}
		close(start)
		wg.Wait()
		close(results)
		close(errs)
		for err := range errs {
			if err != nil {
				t.Fatalf("iteration %d competing migration: %v", iteration, err)
			}
		}
		counts := map[LegacyMigrationResult]int{}
		for result := range results {
			counts[result]++
		}
		if counts[LegacyMigrationCopied] != 1 || counts[LegacyMigrationCurrent] != 1 {
			t.Fatalf("iteration %d results = %v, want one copied and one current", iteration, counts)
		}
		// #nosec G304 -- targetDB is an exact path inside t.TempDir.
		db, err := os.ReadFile(targetDB)
		if err != nil {
			t.Fatal(err)
		}
		// #nosec G304 -- the record path is fixed beneath t.TempDir.
		record, err := os.ReadFile(filepath.Join(targetRecords, "record"))
		if err != nil {
			t.Fatal(err)
		}
		pairA := string(db) == "db-A" && string(record) == "record-A"
		pairB := string(db) == "db-B" && string(record) == "record-B"
		if !pairA && !pairB {
			t.Fatalf("iteration %d mixed migration pair: db=%q record=%q", iteration, db, record)
		}
	}
}

func jsonMarshalForTest(value any) ([]byte, error) {
	return json.Marshal(value)
}
