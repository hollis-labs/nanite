package store

import (
	"context"
	"testing"
	"time"
)

// TestMigrate126SeedsSelftoolReactionKinds is the regression test for
// TASKS/harness-reactive-self-tools/02-reactive-layer-schema.md: the two
// new reactive-layer tables (selftool_reaction_kinds, selftool_reactions)
// exist, selftool_reaction_kinds is seeded with exactly the four rows
// docs/engineering/architecture/11-harness-reactive-self-tools.md's
// "Definition & reaction shape" section (and this task's own seed table)
// specify byte-for-byte, and selftool_reactions starts out empty.
func TestMigrate126SeedsSelftoolReactionKinds(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	var kindCount int
	if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM selftool_reaction_kinds`).Scan(&kindCount); err != nil {
		t.Fatalf("count selftool_reaction_kinds: %v", err)
	}
	if kindCount != 4 {
		t.Fatalf("selftool_reaction_kinds row count = %d, want 4", kindCount)
	}

	wantKinds := map[string]SelftoolReactionKind{
		"render_card": {
			Slug:        "render_card",
			Category:    "render",
			Implemented: true,
			Description: "Resolves the reaction's config into an envelope payload; the calling self-tool handler embeds it as an <!--ENVELOPE_DATA:...--> marker in its ToolResult text. See 04-render-card-construction.md.",
		},
		"internal_api_call": {
			Slug:        "internal_api_call",
			Category:    "execute",
			Implemented: true,
			Description: "Executes an HTTP call against a trusted, in-process/internal endpoint. See 03-reaction-engine-core.md.",
		},
		"external_api_call": {
			Slug:        "external_api_call",
			Category:    "execute",
			Implemented: false,
			Description: `Reserved for a future trust-gated call to an external endpoint. Not executable in this batch -- see this folder's README, "What this batch does NOT do."`,
		},
		"callback": {
			Slug:        "callback",
			Category:    "execute",
			Implemented: false,
			Description: "Reserved for a future opaque-callback-target reaction. Not executable in this batch.",
		},
	}

	for slug, want := range wantKinds {
		got, err := s.GetSelftoolReactionKind(ctx, slug)
		if err != nil {
			t.Fatalf("GetSelftoolReactionKind(%q): %v", slug, err)
		}
		if *got != want {
			t.Errorf("GetSelftoolReactionKind(%q) = %+v, want %+v", slug, got, want)
		}
	}

	if _, err := s.GetSelftoolReactionKind(ctx, "does_not_exist"); err != ErrSelftoolReactionKindNotFound {
		t.Errorf("GetSelftoolReactionKind(unknown) err = %v, want ErrSelftoolReactionKindNotFound", err)
	}

	var reactionCount int
	if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM selftool_reactions`).Scan(&reactionCount); err != nil {
		t.Fatalf("count selftool_reactions: %v", err)
	}
	if reactionCount != 0 {
		t.Fatalf("selftool_reactions row count = %d, want 0 (no rows seeded by this migration)", reactionCount)
	}

	assertGooseHasNothingPending(t, s)

	// Simulated restart: a second full migrate() must be a clean no-op.
	if err := s.migrate(); err != nil {
		t.Fatalf("re-migrate after 126 already applied: %v", err)
	}
}

// TestSelftoolReactionsRoundTrip exercises InsertSelftoolReaction /
// ListEnabledSelftoolReactions — the read/write path TASKS/harness-
// reactive-self-tools/03-reaction-engine-core.md's Fire() and
// 07-worked-example-task-update-report.md's seed rows both consume.
func TestSelftoolReactionsRoundTrip(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	// Empty ID is generated via uuid.New().String().
	r1 := SelftoolReaction{
		ToolName:       "task_update_report",
		ReactionKindID: "render_card",
		Config:         `{"envelope_type":"info-card","template":{"title":"Task update","body":"{{msg}}"}}`,
		Enabled:        true,
	}
	if err := s.InsertSelftoolReaction(ctx, r1); err != nil {
		t.Fatalf("InsertSelftoolReaction (render_card): %v", err)
	}

	// Explicit ID is preserved as given.
	r2 := SelftoolReaction{
		ID:             "selftool-reaction-explicit-id",
		ToolName:       "task_update_report",
		ReactionKindID: "internal_api_call",
		Config:         `{"endpoint":"/api/example/task-updates","method":"POST"}`,
		Enabled:        true,
		CreatedAt:      time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC),
	}
	if err := s.InsertSelftoolReaction(ctx, r2); err != nil {
		t.Fatalf("InsertSelftoolReaction (internal_api_call): %v", err)
	}

	// A disabled reaction on the same tool must not surface in
	// ListEnabledSelftoolReactions.
	r3 := SelftoolReaction{
		ToolName:       "task_update_report",
		ReactionKindID: "internal_api_call",
		Config:         `{"endpoint":"/api/example/other","method":"POST"}`,
		Enabled:        false,
	}
	if err := s.InsertSelftoolReaction(ctx, r3); err != nil {
		t.Fatalf("InsertSelftoolReaction (disabled): %v", err)
	}

	// A reaction on a different tool must not surface for this tool's query.
	r4 := SelftoolReaction{
		ToolName:       "other_tool",
		ReactionKindID: "render_card",
		Config:         `{}`,
		Enabled:        true,
	}
	if err := s.InsertSelftoolReaction(ctx, r4); err != nil {
		t.Fatalf("InsertSelftoolReaction (other tool): %v", err)
	}

	got, err := s.ListEnabledSelftoolReactions(ctx, "task_update_report")
	if err != nil {
		t.Fatalf("ListEnabledSelftoolReactions: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("ListEnabledSelftoolReactions returned %d rows, want 2 (r1, r2 only): %+v", len(got), got)
	}

	byKind := make(map[string]SelftoolReaction)
	for _, row := range got {
		byKind[row.ReactionKindID] = row
	}

	rc, ok := byKind["render_card"]
	if !ok {
		t.Fatalf("render_card reaction missing from ListEnabledSelftoolReactions result: %+v", got)
	}
	if rc.ID == "" {
		t.Error("render_card reaction ID was not generated (empty)")
	}
	if rc.ToolName != "task_update_report" {
		t.Errorf("render_card reaction ToolName = %q, want %q", rc.ToolName, "task_update_report")
	}
	if rc.Config != r1.Config {
		t.Errorf("render_card reaction Config = %q, want %q", rc.Config, r1.Config)
	}
	if !rc.Enabled {
		t.Error("render_card reaction Enabled = false, want true")
	}
	if rc.CreatedAt.IsZero() {
		t.Error("render_card reaction CreatedAt was not stamped")
	}

	iac, ok := byKind["internal_api_call"]
	if !ok {
		t.Fatalf("internal_api_call reaction missing from ListEnabledSelftoolReactions result: %+v", got)
	}
	if iac.ID != "selftool-reaction-explicit-id" {
		t.Errorf("internal_api_call reaction ID = %q, want explicit %q", iac.ID, "selftool-reaction-explicit-id")
	}
	if !iac.CreatedAt.Equal(r2.CreatedAt) {
		t.Errorf("internal_api_call reaction CreatedAt = %v, want %v", iac.CreatedAt, r2.CreatedAt)
	}

	otherTool, err := s.ListEnabledSelftoolReactions(ctx, "other_tool")
	if err != nil {
		t.Fatalf("ListEnabledSelftoolReactions(other_tool): %v", err)
	}
	if len(otherTool) != 1 {
		t.Fatalf("ListEnabledSelftoolReactions(other_tool) returned %d rows, want 1", len(otherTool))
	}

	none, err := s.ListEnabledSelftoolReactions(ctx, "does_not_exist_tool")
	if err != nil {
		t.Fatalf("ListEnabledSelftoolReactions(unknown tool): %v", err)
	}
	if len(none) != 0 {
		t.Fatalf("ListEnabledSelftoolReactions(unknown tool) returned %d rows, want 0", len(none))
	}
}
