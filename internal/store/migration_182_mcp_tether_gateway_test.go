package store

import (
	"context"
	"strings"
	"testing"
)

func plantLegacyMCPRow(t *testing.T, s *Store, name, command, args string, enabled int) {
	t.Helper()
	if _, err := s.DB.ExecContext(context.Background(),
		`INSERT INTO mcp_servers (id, name, transport_type, command, args, enabled, created_at, updated_at)
		 VALUES (?, ?, 'stdio', ?, ?, ?, '2026-10-01T00:00:00Z', '2026-10-01T00:00:00Z')`,
		"id-"+name, name, command, args, enabled); err != nil {
		t.Fatalf("plant %s: %v", name, err)
	}
}

// An install that already holds two enabled gateways is not silently
// exempted: the migration refuses, names the cause, and changes nothing.
func TestMigration182_RefusesMoreThanOneEnabledGateway(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := newTestStoreAtVersion(t, 181)
	plantLegacyMCPRow(t, s, "tether", "tether", `["mcp","--token-file","/a"]`, 1)
	plantLegacyMCPRow(t, s, "tether2", "/usr/bin/tether", `["mcp","--token-file=/b"]`, 1)
	plantLegacyMCPRow(t, s, "other", "npx", `["--token-file","x"]`, 1)

	_, err := gooseProviderFor(t, s).Up(ctx)
	if err == nil || !strings.Contains(err.Error(), "more than one enabled Tether gateway") {
		t.Fatalf("Up error = %v, want the named refusal", err)
	}
	var n int
	if scanErr := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM mcp_servers`).Scan(&n); scanErr != nil || n != 3 {
		t.Fatalf("rows after refusal = %d (%v), want all 3 untouched", n, scanErr)
	}
	if _, colErr := s.DB.ExecContext(ctx, `SELECT tether_gateway FROM mcp_servers`); colErr == nil {
		t.Fatal("the refused migration left its column behind")
	}
}

// Upgrading with exactly one enabled gateway marks it; a disabled legacy
// gateway is left unmarked and does not block.
func TestMigration182_MarksTheSingleEnabledGateway(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := newTestStoreAtVersion(t, 181)
	plantLegacyMCPRow(t, s, "tether", "tether", `["mcp","--token-file","/a"]`, 1)
	plantLegacyMCPRow(t, s, "tether-old", "tether", `["mcp","--token-file","/b"]`, 0)
	plantLegacyMCPRow(t, s, "other", "npx", `["--token-file","x"]`, 1)

	if _, err := gooseProviderFor(t, s).Up(ctx); err != nil {
		t.Fatalf("Up: %v", err)
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT name, tether_gateway FROM mcp_servers ORDER BY name`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	got := map[string]int{}
	for rows.Next() {
		var name string
		var flag int
		if err := rows.Scan(&name, &flag); err != nil {
			t.Fatal(err)
		}
		got[name] = flag
	}
	if got["tether"] != 1 || got["tether-old"] != 0 || got["other"] != 0 {
		t.Fatalf("flags = %v", got)
	}
	if err := s.CreateMCPServer(ctx, tetherGateway("tether2", "/c")); err == nil {
		t.Fatal("a second enabled gateway was accepted after the upgrade")
	}
}
