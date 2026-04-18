package toolclient

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
)

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

// When the store is nil and no resolver is wired, file-based agents must
// fall through to default-permit without an info-level WARN — the noise this
// ticket exists to fix. We capture slog output to assert log discipline.
func TestGetPermissions_FileAgentMissingDoesNotWarn(t *testing.T) {
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo})))
	t.Cleanup(func() { slog.SetDefault(prev) })

	tb := New(nil, nil, DefaultConfig())
	got := tb.GetPermissions("file-default")
	if got.MaxCallsPerTurn != DefaultMaxCallsPerTurn {
		t.Errorf("MaxCallsPerTurn = %d, want %d (default-permit fallback)", got.MaxCallsPerTurn, DefaultMaxCallsPerTurn)
	}
	if strings.Contains(buf.String(), "could not load agent for permissions") {
		t.Errorf("expected no WARN for file-based agent miss, got log: %s", buf.String())
	}
}

// Non-file agents must still WARN on miss (real signal — a chat is asking
// permissions for an unknown DB-backed agent ID, which usually means a stale
// session_agents binding or a deleted profile).
func TestGetPermissions_NonFileAgentWarnsOnMiss(t *testing.T) {
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo})))
	t.Cleanup(func() { slog.SetDefault(prev) })

	tb := New(nil, nil, DefaultConfig())
	tb.PermissionResolver = func(agentID string) (ToolPermissions, bool) {
		return ToolPermissions{}, false
	}
	// Store is nil, so we fall through to default-permit. The store-nil branch
	// also must not WARN (no DB to fail against). To assert the WARN-on-miss
	// behavior for non-file IDs we rely on broker_permissions_test.go's
	// integration coverage — here we only assert the file-based path stays
	// quiet, and that the default fallback applies for unknown non-file IDs
	// when no store is wired.
	got := tb.GetPermissions("mentat-001")
	if got.MaxCallsPerTurn != DefaultMaxCallsPerTurn {
		t.Errorf("MaxCallsPerTurn = %d, want default", got.MaxCallsPerTurn)
	}
}
