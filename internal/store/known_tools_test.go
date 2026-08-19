package store

import (
	"context"
	"errors"
	"testing"
)

func TestUpsertKnownTool_InsertThenUpdate(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	id, err := s.UpsertKnownTool(ctx, "dev_read", "builtin", "available", "Reads a file.")
	if err != nil {
		t.Fatalf("UpsertKnownTool (insert): %v", err)
	}
	if id == "" {
		t.Fatal("UpsertKnownTool did not return an id")
	}

	got, err := s.GetKnownToolByName(ctx, "dev_read")
	if err != nil {
		t.Fatalf("GetKnownToolByName: %v", err)
	}
	if got.ID != id || got.Source != "builtin" || got.Status != "available" || got.Description != "Reads a file." {
		t.Fatalf("GetKnownToolByName mismatch: %+v", got)
	}

	// Re-upsert with a changed description/status keeps the same id.
	id2, err := s.UpsertKnownTool(ctx, "dev_read", "builtin", "unavailable", "Reads a file (updated).")
	if err != nil {
		t.Fatalf("UpsertKnownTool (update): %v", err)
	}
	if id2 != id {
		t.Fatalf("UpsertKnownTool changed id across an update: got %s, want %s", id2, id)
	}

	got2, err := s.GetKnownToolByName(ctx, "dev_read")
	if err != nil {
		t.Fatalf("GetKnownToolByName (after update): %v", err)
	}
	if got2.Status != "unavailable" || got2.Description != "Reads a file (updated)." {
		t.Fatalf("UpsertKnownTool did not refresh status/description: %+v", got2)
	}
}

func TestUpsertKnownTool_PreservesAlwaysIncluded(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	// request_tools is seeded by migration 116 with always_included=TRUE.
	got, err := s.GetKnownToolByName(ctx, "request_tools")
	if err != nil {
		t.Fatalf("GetKnownToolByName(request_tools): %v", err)
	}
	if !got.AlwaysIncluded {
		t.Fatalf("request_tools seed row expected always_included=true, got %+v", got)
	}

	// A routine catalog re-sync (UpsertKnownTool) must not clear the flag.
	if _, err := s.UpsertKnownTool(ctx, "request_tools", "builtin", "available", "Progressive tool discovery meta-tool."); err != nil {
		t.Fatalf("UpsertKnownTool(request_tools): %v", err)
	}
	got2, err := s.GetKnownToolByName(ctx, "request_tools")
	if err != nil {
		t.Fatalf("GetKnownToolByName(request_tools) after re-sync: %v", err)
	}
	if !got2.AlwaysIncluded {
		t.Fatalf("UpsertKnownTool cleared always_included on re-sync: %+v", got2)
	}
}

func TestGetKnownToolByName_NotFound(t *testing.T) {
	s := newTestStore(t)
	_, err := s.GetKnownToolByName(context.Background(), "no_such_tool")
	if !errors.Is(err, ErrKnownToolNotFound) {
		t.Fatalf("GetKnownToolByName: expected ErrKnownToolNotFound, got %v", err)
	}
}

func TestMarkKnownToolsUnavailableExcept(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	if _, err := s.UpsertKnownTool(ctx, "dev_read", "builtin", "available", ""); err != nil {
		t.Fatalf("UpsertKnownTool(dev_read): %v", err)
	}
	if _, err := s.UpsertKnownTool(ctx, "dev_write", "builtin", "available", ""); err != nil {
		t.Fatalf("UpsertKnownTool(dev_write): %v", err)
	}

	// Simulate a re-sync where dev_write is no longer registered.
	n, err := s.MarkKnownToolsUnavailableExcept(ctx, []string{"dev_read", "request_tools", "tool_list", "tool_describe"})
	if err != nil {
		t.Fatalf("MarkKnownToolsUnavailableExcept: %v", err)
	}
	if n != 1 {
		t.Fatalf("MarkKnownToolsUnavailableExcept: expected 1 row marked, got %d", n)
	}

	got, err := s.GetKnownToolByName(ctx, "dev_write")
	if err != nil {
		t.Fatalf("GetKnownToolByName(dev_write): %v", err)
	}
	if got.Status != "unavailable" {
		t.Fatalf("dev_write expected status=unavailable, got %q", got.Status)
	}

	// Row still exists -- never deleted.
	all, err := s.ListKnownTools(ctx)
	if err != nil {
		t.Fatalf("ListKnownTools: %v", err)
	}
	found := false
	for _, kt := range all {
		if kt.Name == "dev_write" {
			found = true
		}
	}
	if !found {
		t.Fatal("MarkKnownToolsUnavailableExcept deleted a row instead of marking it unavailable")
	}

	got2, err := s.GetKnownToolByName(ctx, "dev_read")
	if err != nil {
		t.Fatalf("GetKnownToolByName(dev_read): %v", err)
	}
	if got2.Status != "available" {
		t.Fatalf("dev_read should remain available, got %q", got2.Status)
	}
}

// TestAlwaysIncludedToolsSurviveZeroExplicitGrants is this task's Done-means
// bullet, verified directly: "At least one default-always-included tool
// flag exists and is verified to survive a fresh agent creation with zero
// explicit tool grants." always_included is a property of the known_tools
// catalog row itself, not of any per-agent agent_tools grant -- so a freshly
// created agent with zero agent_tools rows still has the escape-hatch tools
// available via ListAlwaysIncludedKnownTools, independent of whatever
// (empty) grant set ListAgentToolNames returns for it.
func TestAlwaysIncludedToolsSurviveZeroExplicitGrants(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	agent := makeTestAgent(t, s, "fresh-agent-zero-grants")

	grants, err := s.ListAgentToolNames(ctx, agent.ID)
	if err != nil {
		t.Fatalf("ListAgentToolNames: %v", err)
	}
	if len(grants) != 0 {
		t.Fatalf("expected a freshly created agent to have zero explicit agent_tools grants, got %v", grants)
	}

	always, err := s.ListAlwaysIncludedKnownTools(ctx)
	if err != nil {
		t.Fatalf("ListAlwaysIncludedKnownTools: %v", err)
	}
	names := map[string]bool{}
	for _, kt := range always {
		names[kt.Name] = true
	}
	for _, want := range []string{"request_tools", "tool_list", "tool_describe"} {
		if !names[want] {
			t.Errorf("expected always_included tool %q to survive for an agent with zero explicit grants, got %+v", want, always)
		}
	}
}

func TestListAlwaysIncludedKnownTools(t *testing.T) {
	s := newTestStore(t)
	list, err := s.ListAlwaysIncludedKnownTools(context.Background())
	if err != nil {
		t.Fatalf("ListAlwaysIncludedKnownTools: %v", err)
	}
	names := map[string]bool{}
	for _, kt := range list {
		names[kt.Name] = true
	}
	for _, want := range []string{"request_tools", "tool_list", "tool_describe"} {
		if !names[want] {
			t.Errorf("ListAlwaysIncludedKnownTools missing seeded default %q, got %+v", want, list)
		}
	}
}
