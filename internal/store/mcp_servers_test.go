package store

import (
	"context"
	"database/sql"
	"testing"
)

// TestMCPServerTrust_DefaultsAndRoundTrip exercises the S4b trust-tier and
// env-allowlist columns: defaults are fail-closed when callers don't set
// them, explicit values round-trip via Create→Get and Update→Get.
func TestMCPServerTrust_DefaultsAndRoundTrip(t *testing.T) {
	s := newTestStore(t)

	// Empty TrustTier and EnvAllowlist should be defaulted on Create.
	cfg := &MCPServerConfig{
		Name:          "default-tier",
		TransportType: "stdio",
		Command:       "echo",
		Enabled:       true,
	}
	if err := s.CreateMCPServer(context.Background(), cfg); err != nil {
		t.Fatalf("CreateMCPServer: %v", err)
	}
	if cfg.TrustTier != TrustTierThirdPartyHTTP {
		t.Errorf("default TrustTier: got %q want %q", cfg.TrustTier, TrustTierThirdPartyHTTP)
	}
	if cfg.EnvAllowlist != "[]" {
		t.Errorf("default EnvAllowlist: got %q want %q", cfg.EnvAllowlist, "[]")
	}

	got, err := s.GetMCPServer(context.Background(), "default-tier")
	if err != nil {
		t.Fatalf("GetMCPServer: %v", err)
	}
	if got == nil {
		t.Fatal("GetMCPServer: nil")
	}
	if got.TrustTier != TrustTierThirdPartyHTTP {
		t.Errorf("round-trip default TrustTier: got %q want %q", got.TrustTier, TrustTierThirdPartyHTTP)
	}
	if got.EnvAllowlist != "[]" {
		t.Errorf("round-trip default EnvAllowlist: got %q want %q", got.EnvAllowlist, "[]")
	}

	// Explicit values round-trip on Create.
	explicit := &MCPServerConfig{
		Name:          "explicit-tier",
		TransportType: "sse",
		URL:           "http://example.test/mcp",
		Enabled:       true,
		TrustTier:     TrustTierBuiltin,
		EnvAllowlist:  `["PATH","HOME","USER","LANG"]`,
	}
	if err2 := s.CreateMCPServer(context.Background(), explicit); err2 != nil {
		t.Fatalf("CreateMCPServer explicit: %v", err2)
	}

	gotEx, err := s.GetMCPServer(context.Background(), "explicit-tier")
	if err != nil {
		t.Fatalf("GetMCPServer explicit: %v", err)
	}
	if gotEx.TrustTier != TrustTierBuiltin {
		t.Errorf("explicit TrustTier: got %q want %q", gotEx.TrustTier, TrustTierBuiltin)
	}
	if gotEx.EnvAllowlist != `["PATH","HOME","USER","LANG"]` {
		t.Errorf("explicit EnvAllowlist: got %q", gotEx.EnvAllowlist)
	}

	// Update should round-trip the new tier and allowlist.
	gotEx.TrustTier = TrustTierPluginHTTP
	gotEx.EnvAllowlist = `["PATH"]`
	if err2 := s.UpdateMCPServer(context.Background(), gotEx); err2 != nil {
		t.Fatalf("UpdateMCPServer: %v", err2)
	}

	gotUpd, err := s.GetMCPServer(context.Background(), "explicit-tier")
	if err != nil {
		t.Fatalf("GetMCPServer after update: %v", err)
	}
	if gotUpd.TrustTier != TrustTierPluginHTTP {
		t.Errorf("updated TrustTier: got %q want %q", gotUpd.TrustTier, TrustTierPluginHTTP)
	}
	if gotUpd.EnvAllowlist != `["PATH"]` {
		t.Errorf("updated EnvAllowlist: got %q", gotUpd.EnvAllowlist)
	}

	// List should also round-trip both rows with their respective values.
	all, err := s.ListMCPServers(context.Background())
	if err != nil {
		t.Fatalf("ListMCPServers: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("ListMCPServers: got %d rows, want 2", len(all))
	}
	for _, row := range all {
		switch row.Name {
		case "default-tier":
			if row.TrustTier != TrustTierThirdPartyHTTP {
				t.Errorf("list default TrustTier: got %q", row.TrustTier)
			}
			if row.EnvAllowlist != "[]" {
				t.Errorf("list default EnvAllowlist: got %q", row.EnvAllowlist)
			}
		case "explicit-tier":
			if row.TrustTier != TrustTierPluginHTTP {
				t.Errorf("list explicit TrustTier: got %q", row.TrustTier)
			}
			if row.EnvAllowlist != `["PATH"]` {
				t.Errorf("list explicit EnvAllowlist: got %q", row.EnvAllowlist)
			}
		default:
			t.Errorf("unexpected row %q", row.Name)
		}
	}
}

// TestMCPServerTrust_MigrationIdempotent re-applies the migration runner
// (via repeated New on the same path) to confirm the ALTER TABLE statements
// in 012 don't error on the duplicate-column path.
func TestMCPServerTrust_MigrationIdempotent(t *testing.T) {
	dir := t.TempDir()
	dbPath := dir + "/test.db"

	s1, err := New(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("first New: %v", err)
	}
	if closeErr := s1.Close(context.Background()); closeErr != nil {
		t.Fatalf("close s1: %v", closeErr)
	}

	s2, err := New(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("second New (idempotent migrations): %v", err)
	}
	defer s2.Close(context.

		// Verify the columns exist.
		Background())

	rows, err := s2.DB.Query("PRAGMA table_info(mcp_servers)")
	if err != nil {
		t.Fatalf("PRAGMA table_info: %v", err)
	}
	defer rows.Close()

	cols := map[string]string{}
	for rows.Next() {
		var cid int
		var name, ctype string
		var notnull, pk int
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			t.Fatalf("scan PRAGMA row: %v", err)
		}
		cols[name] = ctype
	}
	if _, ok := cols["trust_tier"]; !ok {
		t.Error("trust_tier column missing after migration 012")
	}
	if _, ok := cols["env_allowlist"]; !ok {
		t.Error("env_allowlist column missing after migration 012")
	}
}
