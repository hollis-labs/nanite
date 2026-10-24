package store

import (
	"context"
	"errors"
	"sync"
	"testing"
)

func TestMigration184_UpDownAndNoRows(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := newTestStoreAtVersion(t, 182)
	if _, err := s.DB.ExecContext(ctx, `SELECT install_id FROM install_identity`); err == nil {
		t.Fatal("install_identity exists at version 182")
	}
	if _, err := gooseProviderFor(t, s).UpTo(ctx, 184); err != nil {
		t.Fatalf("Up: %v", err)
	}
	var n int
	if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM install_identity`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("migration must create the table empty: n=%d err=%v", n, err)
	}
	if _, err := s.DB.ExecContext(ctx, `INSERT INTO install_identity (id, install_id) VALUES (2, 'x')`); err == nil {
		t.Fatal("singleton CHECK allowed id=2")
	}
	if _, err := gooseProviderFor(t, s).DownTo(ctx, 182); err != nil {
		t.Fatalf("Down: %v", err)
	}
	if _, err := s.DB.ExecContext(ctx, `SELECT install_id FROM install_identity`); err == nil {
		t.Fatal("install_identity survived Down")
	}
}

func TestEnsureInstallID_MintsOnFirstBootAndIsStable(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbPath := t.TempDir() + "/a.db"
	s, err := New(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	first, err := s.InstallID(ctx) // minted by New, no explicit Ensure call
	if err != nil || len(first) != 36 {
		t.Fatalf("install id after first boot = %q, %v", first, err)
	}
	if again, _ := s.EnsureInstallID(ctx); again != first {
		t.Fatalf("Ensure changed the id: %q -> %q", first, again)
	}
	_ = s.Close(ctx)
	s2, err := New(ctx, dbPath) // second boot
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s2.Close(ctx) }()
	if got, _ := s2.InstallID(ctx); got != first {
		t.Fatalf("second boot changed id: %q -> %q", first, got)
	}
}

func TestEnsureInstallID_UpgradeFromBeforeMigrationMintsOnBoot(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := newTestStoreAtVersion(t, 182)
	if _, err := gooseProviderFor(t, s).UpTo(ctx, 184); err != nil {
		t.Fatal(err)
	}
	id, err := s.EnsureInstallID(ctx)
	if err != nil || id == "" {
		t.Fatalf("ensure = %q, %v", id, err)
	}
}

func TestEnsureInstallID_Concurrent(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := newTestStoreAtVersion(t, 182)
	if _, err := gooseProviderFor(t, s).UpTo(ctx, 184); err != nil {
		t.Fatal(err)
	}
	const n = 16
	ids := make([]string, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ids[i], _ = s.EnsureInstallID(ctx)
		}()
	}
	wg.Wait()
	for _, id := range ids {
		if id == "" || id != ids[0] {
			t.Fatalf("concurrent ensure diverged: %v", ids)
		}
	}
	var rows int
	_ = s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM install_identity`).Scan(&rows)
	if rows != 1 {
		t.Fatalf("rows = %d, want 1", rows)
	}
}

func TestTetherEnrollmentKey(t *testing.T) {
	t.Parallel()
	got, err := TetherEnrollmentKey("11111111-2222-3333-4444-555555555555", "host-abc")
	if err != nil || got != "nanite/11111111-2222-3333-4444-555555555555/host-abc" {
		t.Fatalf("key = %q, %v", got, err)
	}
	for _, bad := range [][2]string{{"", "h"}, {"i", ""}, {"a/b", "h"}, {"i", "h/x"}, {"i", " "}} {
		if _, err := TetherEnrollmentKey(bad[0], bad[1]); err == nil {
			t.Fatalf("accepted %q", bad)
		}
	}
	s := newTestStoreAtVersion(t, 182)
	if _, err := gooseProviderFor(t, s).UpTo(context.Background(), 184); err != nil {
		t.Fatal(err)
	}
	id, _ := s.EnsureInstallID(context.Background())
	if k, err := s.EnrollmentKey(context.Background(), "h1"); err != nil || k != "nanite/"+id+"/h1" {
		t.Fatalf("store key = %q, %v", k, err)
	}
}

// A cloned database carries the source install's id; regen on the clone, before
// any enrollment, gives it its own id and leaves the source untouched.
func TestRegenerateInstallID_ClonedDatabase(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	src := t.TempDir() + "/src.db"
	s, err := New(ctx, src)
	if err != nil {
		t.Fatal(err)
	}
	srcID, _ := s.InstallID(ctx)
	if _, err := s.DB.ExecContext(ctx, `PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
		t.Fatal(err)
	}
	clone := t.TempDir() + "/clone.db"
	if err := copyFile(clone, src); err != nil {
		t.Fatal(err)
	}
	c, err := New(ctx, clone)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close(ctx) }()
	if id, _ := c.InstallID(ctx); id != srcID {
		t.Fatalf("clone should carry the source id; got %q want %q", id, srcID)
	}
	ii, err := c.RegenerateInstallID(ctx, false, "test")
	if err != nil {
		t.Fatalf("regen: %v", err)
	}
	if ii.InstallID == srcID || ii.PreviousInstallID != srcID || ii.RegeneratedAt == "" {
		t.Fatalf("regen result = %+v", ii)
	}
	if id, _ := s.InstallID(ctx); id != srcID {
		t.Fatal("source install id changed")
	}
	_ = s.Close(ctx)
	var audits int
	_ = c.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM event_log WHERE event_type='install_identity_regenerated'`).Scan(&audits)
	if audits != 1 {
		t.Fatalf("audit rows = %d", audits)
	}
}

func TestRegenerateInstallID_RefusesWhenEnrolled(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := newTestStore(t)
	before, _ := s.EnsureInstallID(ctx)
	iiExec(t, s, `PRAGMA foreign_keys=OFF`)
	iiExec(t, s, `INSERT INTO agent_host_settings (id,slug,title,definition_id,definition_revision,semantic_digest,settings_json,source,revision) VALUES ('hs1','s','t','d','1','x','{}','test','1')`)
	iiExec(t, s, `INSERT INTO agent_actor_bindings (actor_uri, host_settings_id, binding_receipt) VALUES ('msg://agent/tether/a', 'hs1', 'r')`)

	_, err := s.RegenerateInstallID(ctx, false, "test")
	if !errors.Is(err, ErrInstallHasEnrolledAgents) {
		t.Fatalf("err = %v, want ErrInstallHasEnrolledAgents", err)
	}
	if after, _ := s.InstallID(ctx); after != before {
		t.Fatal("refused regen changed the id")
	}
	ii, err := s.RegenerateInstallID(ctx, true, "test")
	if err != nil || ii.InstallID == before || ii.PreviousInstallID != before {
		t.Fatalf("forced regen = %+v, %v", ii, err)
	}
	var urn string
	_ = s.DB.QueryRowContext(ctx, `SELECT actor_uri FROM agent_actor_bindings`).Scan(&urn)
	if urn != "msg://agent/tether/a" {
		t.Fatalf("stored URN changed: %q", urn)
	}
}

func TestEnrolledAgentCount_CountsDisabledBindings(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := newTestStore(t)
	if n, err := s.EnrolledAgentCount(ctx); err != nil || n != 0 {
		t.Fatalf("empty count = %d, %v", n, err)
	}
	iiExec(t, s, `PRAGMA foreign_keys=OFF`)
	iiExec(t, s, `INSERT INTO agent_host_settings (id,slug,title,definition_id,definition_revision,semantic_digest,settings_json,source,revision,enabled) VALUES ('hs1','s','t','d','1','x','{}','test','1',0)`)
	iiExec(t, s, `INSERT INTO agent_actor_bindings (actor_uri, host_settings_id, binding_receipt, enabled) VALUES ('u', 'hs1', 'r', 0)`)
	if n, _ := s.EnrolledAgentCount(ctx); n != 1 {
		t.Fatalf("disabled binding not counted: %d", n)
	}
}

func iiExec(t *testing.T, s *Store, q string) {
	t.Helper()
	if _, err := s.DB.ExecContext(context.Background(), q); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
}
