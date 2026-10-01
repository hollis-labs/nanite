package plugin_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/hollis-labs/nanite/internal/plugin"
	"github.com/hollis-labs/nanite/internal/service"
	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/internal/storetest"
)

func TestPluginArtifactPlacementSharesServiceRules(t *testing.T) {
	ctx := context.Background()
	parent := t.TempDir()
	root := filepath.Join(parent, "artifacts")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	db, err := storetest.New(t, ctx, filepath.Join(parent, "store.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := db.Close(ctx); err != nil {
			t.Error(err)
		}
	})
	session := &store.Session{ID: "artifact-placement", Title: "Placement test"}
	if err := db.CreateSession(ctx, session); err != nil {
		t.Fatal(err)
	}
	host := plugin.NewHostWithStore(db)
	host.SetArtifactStorageRoot(root)
	t.Cleanup(func() {
		if err := host.Shutdown(); err != nil {
			t.Error(err)
		}
	})
	svc := service.NewArtifactService(db, func() string { return root })
	report := filepath.Join(root, "report.txt")
	if err := os.WriteFile(report, []byte("REPORT"), 0o600); err != nil {
		t.Fatal(err)
	}
	for i, input := range []string{"report.txt", report} {
		expected, err := svc.Place(ctx, service.PlaceInput{SessionID: session.ID, Name: "report.txt", StoragePath: input})
		if err != nil {
			t.Fatal(err)
		}
		if placeErr := host.PlaceArtifact(session.ID, "", "report.txt", "", input); placeErr != nil {
			t.Fatal(placeErr)
		}
		rows, err := db.ListArtifacts(ctx, session.ID)
		if err != nil {
			t.Fatal(err)
		}
		if len(rows) != 2*(i+1) {
			t.Fatalf("got %d rows", len(rows))
		}
		for _, row := range rows {
			if row.StoragePath != expected.StoragePath || row.MimeType != expected.MimeType || row.SizeBytes != 6 || row.Origin != store.ArtifactOriginPlaced {
				t.Fatalf("transport policy mismatch: %+v vs %+v", row, expected)
			}
		}
	}
	outside := filepath.Join(parent, "secret.txt")
	if err := os.WriteFile(outside, []byte("SECRET"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "escape.txt")); err != nil {
		t.Fatal(err)
	}
	for _, input := range []string{outside, "../secret.txt", "escape.txt", "missing.txt", "."} {
		if _, err := svc.Place(ctx, service.PlaceInput{SessionID: session.ID, Name: "report.txt", StoragePath: input}); err == nil {
			t.Fatalf("service accepted %q", input)
		}
		if err := host.PlaceArtifact(session.ID, "", "report.txt", "", input); err == nil {
			t.Fatalf("plugin accepted %q", input)
		}
		rows, err := db.ListArtifacts(ctx, session.ID)
		if err != nil || len(rows) != 4 {
			t.Fatalf("rejected placement persisted rows: %+v, %v", rows, err)
		}
	}
}
