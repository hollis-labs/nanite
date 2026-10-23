package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/internal/service"
	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/internal/storetest"
)

const cliEnvSecret = "ghp_notarealtokenbutlongenough" // #nosec G101 -- fake token, not a credential

// The CLI export is the complete file: unlike the HTTP export it keeps env
// values, since reading it needs the database itself.
func TestMCPExportCLIKeepsEnvValues(t *testing.T) {
	dir := t.TempDir()
	db := filepath.Join(dir, "nanite.db")
	// resolveDBPath runs before --db is read; point it at the test DB so the
	// real one is never opened.
	t.Setenv("NANITE_DB_PATH", db)
	s, err := store.New(context.Background(), db)
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	if err = s.CreateMCPServer(context.Background(), &store.MCPServerConfig{
		Name: "cli", TransportType: store.TransportStdio, Command: "true", Enabled: true,
		Env: `["GITHUB_TOKEN=` + cliEnvSecret + `"]`,
	}); err != nil {
		t.Fatalf("CreateMCPServer: %v", err)
	}
	_ = s.Close(context.Background())

	out := filepath.Join(dir, "out.mcp.json")
	mcpExport([]string{out, "--db", db})
	data, err := os.ReadFile(out) // #nosec G304 -- the test reads the file it just wrote under t.TempDir()
	if err != nil {
		t.Fatalf("read export: %v", err)
	}
	if !strings.Contains(string(data), cliEnvSecret) {
		t.Fatalf("CLI export lost the env value: %s", data)
	}
}

// The CLI import drops redaction placeholders, so importing a file exported
// from the UI stores no bullets.
func TestMCPImportCLIDropsPlaceholders(t *testing.T) {
	dir := t.TempDir()
	db := filepath.Join(dir, "nanite.db")
	// resolveDBPath runs before --db is read; point it at the test DB so the
	// real one is never opened.
	t.Setenv("NANITE_DB_PATH", db)
	in := filepath.Join(dir, "in.mcp.json")
	body := `{"mcpServers":{"cli":{"command":"true","env":{"TOKEN":"` + service.RedactedHeaderValue + `","KEEP":"1"}}}}`
	if err := os.WriteFile(in, []byte(body), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	mcpImport([]string{in, "--db", db})

	s, err := store.New(context.Background(), db)
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer func() { _ = s.Close(context.Background()) }()
	row, err := s.GetMCPServer(context.Background(), "cli")
	if err != nil || row == nil {
		t.Fatalf("GetMCPServer: %v %v", row, err)
	}
	if row.Env != `["KEEP=1"]` {
		t.Fatalf("stored env = %s, want only KEEP=1", row.Env)
	}
}

func TestRegisterTetherMessaging(t *testing.T) {
	ctx := context.Background()
	st, err := storetest.New(t, ctx, filepath.Join(t.TempDir(), "n.db"))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	good := filepath.Join(dir, "a.token")
	if err = os.WriteFile(good, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	loose := filepath.Join(dir, "loose.token")
	if err = os.WriteFile(loose, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err = os.Chmod(loose, 0o644); err != nil { // #nosec G302 -- deliberately loose fixture
		t.Fatal(err)
	}

	// A group/other-readable token file is refused before anything is stored.
	if _, err = registerTetherMessaging(ctx, st, loose, nil); err == nil {
		t.Fatal("loose token file registered")
	}
	if row, _ := st.GetMCPServer(ctx, "tether"); row != nil {
		t.Fatal("a refused registration left a row")
	}

	prepared := 0
	res, err := registerTetherMessaging(ctx, st, good, func(*store.MCPServerConfig) { prepared++ })
	if err != nil || len(res.Created) != 1 || prepared != 1 {
		t.Fatalf("register: %+v err=%v prepare calls=%d", res, err, prepared)
	}
	// Same file again is a no-op; a different file under the same name is refused.
	if res, err = registerTetherMessaging(ctx, st, good, nil); err != nil || len(res.Skipped) != 1 {
		t.Fatalf("repeat: %+v %v", res, err)
	}
	other := filepath.Join(dir, "b.token")
	if err = os.WriteFile(other, []byte("y"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err = registerTetherMessaging(ctx, st, other, nil); err == nil {
		t.Fatal("second actor's token file accepted")
	}
}
