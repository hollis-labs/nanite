package toolclient

import (
	"bytes"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/internal/store"
)

// newStoreForTest builds a fresh on-disk SQLite store under t.TempDir. The
// toolclient package is separate from internal/store, so it cannot reuse
// store.newTestStore (unexported); this helper is the minimal equivalent.
func newStoreForTest(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.New(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func captureLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo})))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return &buf
}

// CW-20260417-0487: file-based agents have no agent_profiles row by design.
// GetPermissions must consult an injected resolver (set by the service layer
// after the AgentService is built). When the resolver returns ok, those
// permissions are authoritative — no DB lookup, no WARN.
func TestGetPermissions_FileAgentResolverShortCircuitsStore(t *testing.T) {
	resolved := ToolPermissions{
		AllowList:       []string{"mcp__dev__*"},
		MaxCallsPerTurn: 9,
	}
	tb := New(nil, nil, DefaultConfig())
	tb.PermissionResolver = func(agentID string) (ToolPermissions, bool) {
		if agentID == "file-default" {
			return resolved, true
		}
		return ToolPermissions{}, false
	}

	got := tb.GetPermissions("file-default")
	if got.MaxCallsPerTurn != 9 {
		t.Errorf("MaxCallsPerTurn = %d, want 9 (resolver should win)", got.MaxCallsPerTurn)
	}
	if len(got.AllowList) != 1 || got.AllowList[0] != "mcp__dev__*" {
		t.Errorf("AllowList = %v", got.AllowList)
	}
}

// A resolver-miss for a file-based ID followed by a real store lookup that
// returns sql.ErrNoRows must fall through to default-permit *without* a WARN.
// This is the exact noise the ticket was filed to eliminate.
func TestGetPermissions_FileAgentStoreMissIsDebug(t *testing.T) {
	buf := captureLogs(t)

	s := newStoreForTest(t)
	tb := New(nil, s, DefaultConfig())

	got := tb.GetPermissions("file-default")
	if got.MaxCallsPerTurn != DefaultMaxCallsPerTurn {
		t.Errorf("MaxCallsPerTurn = %d, want %d (default-permit fallback)",
			got.MaxCallsPerTurn, DefaultMaxCallsPerTurn)
	}
	if strings.Contains(buf.String(), "could not load agent for permissions") {
		t.Errorf("expected no WARN for file-based agent miss, got log: %s", buf.String())
	}
}

// A non-file agent ID that misses in the store is a real signal (stale
// session_agents binding or deleted profile) and must WARN. This is the test
// that used to be misnamed; it now exercises the actual WARN path via a real
// store whose GetAgent returns sql.ErrNoRows.
func TestGetPermissions_NonFileAgentStoreMissWarns(t *testing.T) {
	buf := captureLogs(t)

	s := newStoreForTest(t)
	tb := New(nil, s, DefaultConfig())

	got := tb.GetPermissions("mentat-001")
	if got.MaxCallsPerTurn != DefaultMaxCallsPerTurn {
		t.Errorf("MaxCallsPerTurn = %d, want %d (default-permit fallback)",
			got.MaxCallsPerTurn, DefaultMaxCallsPerTurn)
	}
	if !strings.Contains(buf.String(), "could not load agent for permissions") {
		t.Errorf("expected WARN on DB-backed agent miss, got log: %s", buf.String())
	}
}

// When the store is returning a non-ErrNoRows error (busy, corruption, I/O)
// even a file-based ID must log WARN so operational issues are visible. We
// simulate this by closing the store before querying, which makes every
// subsequent GetAgent return a driver error distinct from sql.ErrNoRows.
func TestGetPermissions_FileAgentDriverErrorStillWarns(t *testing.T) {
	buf := captureLogs(t)

	s := newStoreForTest(t)
	// Force driver errors for subsequent GetAgent calls. This mimics the
	// "busy/corruption/IO" class of failures that the ticket reviewer flagged
	// as not-to-be-silenced.
	if err := s.DB.Close(); err != nil {
		t.Fatalf("DB.Close: %v", err)
	}

	tb := New(nil, s, DefaultConfig())
	got := tb.GetPermissions("file-default")
	if got.MaxCallsPerTurn != DefaultMaxCallsPerTurn {
		t.Errorf("MaxCallsPerTurn = %d, want default", got.MaxCallsPerTurn)
	}
	if !strings.Contains(buf.String(), "could not load agent for permissions") {
		t.Errorf("expected WARN on file-based agent driver error, got log: %s", buf.String())
	}
}
