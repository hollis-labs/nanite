package store

import (
	"context"
	"testing"
)

// TestMigration161RewritesLegacySSERowsToStreamable is the regression test for
// the transport_type rename: every row stored as 'sse' before 161 was in fact
// registered as a JSON-RPC POST client, so 161 has to move it to 'streamable'
// or the rename silently re-points a working server at a protocol its URL does
// not speak.
//
// The rows are planted at schema version 160 rather than found, and that is
// load-bearing: no database on this machine holds an 'sse' row, so a test that
// merely migrated and counted would assert 0 -> 0 and still pass with the
// UPDATE deleted. Rolling back to 160, planting, and replaying forward is the
// only shape that exercises the backfill. Same technique as migration 148's.
func TestMigration161RewritesLegacySSERowsToStreamable(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	provider := newMigrationProvider(t, s)

	if _, err := provider.DownTo(ctx, 160); err != nil {
		t.Fatalf("goose DownTo 160: %v", err)
	}

	plant := func(name, transport string) {
		t.Helper()
		if _, err := s.DB.ExecContext(ctx,
			`INSERT INTO mcp_servers (id, name, transport_type, command, url, args, env, enabled,
			   trust_tier, env_allowlist, headers, created_at, updated_at)
			 VALUES (?, ?, ?, '', 'http://gateway.invalid/servers/x/mcp', '[]', '[]', 1,
			   'third_party_http', '[]', '{}', '2026-09-15T00:00:00Z', '2026-09-15T00:00:00Z')`,
			"mig161-"+name, name, transport,
		); err != nil {
			t.Fatalf("plant pre-161 %q row at schema version 160: %v", transport, err)
		}
	}
	plant("legacy-sse", "sse")
	plant("a-stdio-server", "stdio")

	if _, err := provider.Up(ctx); err != nil {
		t.Fatalf("goose Up (replay migration 161): %v", err)
	}

	if got := transportTypeOf(t, s, "legacy-sse"); got != TransportStreamable {
		t.Fatalf("legacy 'sse' row after Up = %q, want %q — the rename re-pointed it at a protocol it does not speak",
			got, TransportStreamable)
	}
	if got := transportTypeOf(t, s, "a-stdio-server"); got != TransportStdio {
		t.Fatalf("stdio row after Up = %q, want %q — the backfill is not scoped to remote rows", got, TransportStdio)
	}

	assertGooseHasNothingPending(t, s)

	// A second full migrate() must be a clean no-op, as on restart.
	if err := s.migrate(ctx); err != nil {
		t.Fatalf("re-migrate after 161 already applied: %v", err)
	}
}

// TestMigration161DownRestoresTheSingleRemoteSpelling checks the reversal the
// migration tests roll through (up, down, up): Down has to put 'streamable'
// back to 'sse', because pre-161 code knows no other name for a remote server.
func TestMigration161DownRestoresTheSingleRemoteSpelling(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	provider := newMigrationProvider(t, s)

	if err := s.CreateMCPServer(ctx, &MCPServerConfig{
		Name:          "post-161-sse",
		TransportType: TransportStreamable,
		URL:           "http://gateway.invalid/servers/x/mcp",
		Enabled:       true,
	}); err != nil {
		t.Fatalf("create streamable server at head: %v", err)
	}

	if _, err := provider.DownTo(ctx, 160); err != nil {
		t.Fatalf("goose DownTo 160: %v", err)
	}
	if got := transportTypeOf(t, s, "post-161-sse"); got != TransportSSE {
		t.Fatalf("streamable row after Down = %q, want %q", got, TransportSSE)
	}

	if _, err := provider.Up(ctx); err != nil {
		t.Fatalf("goose Up after Down: %v", err)
	}
	if got := transportTypeOf(t, s, "post-161-sse"); got != TransportStreamable {
		t.Fatalf("row after Down/Up round trip = %q, want %q", got, TransportStreamable)
	}
}

func transportTypeOf(t *testing.T, s *Store, name string) string {
	t.Helper()
	var transport string
	if err := s.DB.QueryRow(`SELECT transport_type FROM mcp_servers WHERE name = ?`, name).
		Scan(&transport); err != nil {
		t.Fatalf("read transport_type for %q: %v", name, err)
	}
	return transport
}
