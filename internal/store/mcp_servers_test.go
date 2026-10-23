package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"
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
	t.Parallel()
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

func tetherGateway(name, tokenFile string) *MCPServerConfig {
	return &MCPServerConfig{Name: name, TransportType: TransportStdio, Command: "tether", Enabled: true,
		Args: `["mcp","--no-discover","--token-file","` + tokenFile + `"]`}
}

// One Tether actor per host is enforced where the row is written, so plain
// import, the API and the CLI all meet it under any server name.
func TestMCPServers_OneTetherGatewayPerHost(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	if err := s.CreateMCPServer(ctx, tetherGateway("tether", "/a")); err != nil {
		t.Fatal(err)
	}
	err := s.CreateMCPServer(ctx, tetherGateway("tether2", "/b"))
	if !errors.Is(err, ErrTetherGatewayConflict) {
		t.Fatalf("second gateway under another name: %v", err)
	}
	if row, _ := s.GetMCPServer(ctx, "tether2"); row != nil {
		t.Fatal("refused gateway was stored")
	}
	// Another command is unaffected, and so is editing the one gateway.
	if err := s.CreateMCPServer(ctx, &MCPServerConfig{Name: "files", TransportType: TransportStdio, Command: "npx", Args: `["--token-file","x"]`, Enabled: true}); err != nil {
		t.Fatalf("non-tether server: %v", err)
	}
	if err := s.UpdateMCPServer(ctx, tetherGateway("tether", "/c")); err != nil {
		t.Fatalf("update of the one gateway: %v", err)
	}
	// An existing row cannot be turned into a second gateway either.
	files := tetherGateway("files", "/d")
	if err := s.UpdateMCPServer(ctx, files); !errors.Is(err, ErrTetherGatewayConflict) {
		t.Fatalf("update into a second gateway: %v", err)
	}
	// Removing the first frees the slot.
	if err := s.DeleteMCPServer(ctx, "tether"); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateMCPServer(ctx, tetherGateway("tether2", "/b")); err != nil {
		t.Fatalf("after delete: %v", err)
	}
}

// The rule is the database's, so concurrent writers cannot both win: a
// read-then-write check let 7 of 20 through.
func TestMCPServers_TetherGatewayRuleIsAtomic(t *testing.T) {
	ctx := context.Background()

	race := func(t *testing.T, n int, op func(i int) error) (ok, conflict int) {
		t.Helper()
		var wg sync.WaitGroup
		var mu sync.Mutex
		start := make(chan struct{})
		for i := 0; i < n; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				err := op(i)
				mu.Lock()
				defer mu.Unlock()
				switch {
				case err == nil:
					ok++
				case errors.Is(err, ErrTetherGatewayConflict):
					conflict++
				default:
					t.Errorf("writer %d: unexpected error: %v", i, err)
				}
			}()
		}
		close(start)
		wg.Wait()
		return ok, conflict
	}
	gateways := func(t *testing.T, s *Store) int {
		t.Helper()
		var n int
		if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM mcp_servers WHERE tether_gateway = 1`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}

	t.Run("create/create", func(t *testing.T) {
		s := newTestStore(t)
		ok, conflict := race(t, 20, func(i int) error {
			return s.CreateMCPServer(ctx, tetherGateway(fmt.Sprintf("tether-%d", i), fmt.Sprintf("/tok/%d", i)))
		})
		if ok != 1 || conflict != 19 || gateways(t, s) != 1 {
			t.Fatalf("ok=%d conflict=%d stored=%d, want exactly one", ok, conflict, gateways(t, s))
		}
	})

	t.Run("update/update", func(t *testing.T) {
		s := newTestStore(t)
		for i := 0; i < 20; i++ {
			if err := s.CreateMCPServer(ctx, &MCPServerConfig{Name: fmt.Sprintf("plain-%d", i), TransportType: TransportStdio, Command: "npx", Enabled: true}); err != nil {
				t.Fatal(err)
			}
		}
		ok, conflict := race(t, 20, func(i int) error {
			return s.UpdateMCPServer(ctx, tetherGateway(fmt.Sprintf("plain-%d", i), fmt.Sprintf("/tok/%d", i)))
		})
		if ok != 1 || conflict != 19 || gateways(t, s) != 1 {
			t.Fatalf("ok=%d conflict=%d stored=%d, want exactly one", ok, conflict, gateways(t, s))
		}
	})

	t.Run("create/update", func(t *testing.T) {
		s := newTestStore(t)
		for i := 0; i < 10; i++ {
			if err := s.CreateMCPServer(ctx, &MCPServerConfig{Name: fmt.Sprintf("plain-%d", i), TransportType: TransportStdio, Command: "npx", Enabled: true}); err != nil {
				t.Fatal(err)
			}
		}
		ok, conflict := race(t, 20, func(i int) error {
			if i%2 == 0 {
				return s.CreateMCPServer(ctx, tetherGateway(fmt.Sprintf("new-%d", i), fmt.Sprintf("/tok/c%d", i)))
			}
			return s.UpdateMCPServer(ctx, tetherGateway(fmt.Sprintf("plain-%d", i/2), fmt.Sprintf("/tok/u%d", i)))
		})
		if ok != 1 || conflict != 19 || gateways(t, s) != 1 {
			t.Fatalf("ok=%d conflict=%d stored=%d, want exactly one", ok, conflict, gateways(t, s))
		}
	})

	t.Run("unrelated servers are unaffected", func(t *testing.T) {
		s := newTestStore(t)
		ok, conflict := race(t, 20, func(i int) error {
			return s.CreateMCPServer(ctx, &MCPServerConfig{Name: fmt.Sprintf("srv-%d", i), TransportType: TransportStdio, Command: "npx", Args: `["--token-file","x"]`, Enabled: true})
		})
		if ok != 20 || conflict != 0 {
			t.Fatalf("ok=%d conflict=%d", ok, conflict)
		}
	})

	t.Run("a gateway turned into a plain server frees the slot", func(t *testing.T) {
		s := newTestStore(t)
		if err := s.CreateMCPServer(ctx, tetherGateway("tether", "/a")); err != nil {
			t.Fatal(err)
		}
		if err := s.UpdateMCPServer(ctx, &MCPServerConfig{Name: "tether", TransportType: TransportStdio, Command: "npx", Args: "[]", Enabled: true}); err != nil {
			t.Fatal(err)
		}
		if err := s.CreateMCPServer(ctx, tetherGateway("tether2", "/b")); err != nil {
			t.Fatalf("slot not freed: %v", err)
		}
	})
}
