package store

import (
	"context"
	"encoding/json"
	"io/fs"
	"strings"
	"testing"

	"github.com/pressly/goose/v3"
)

func TestHostRuntimeFeedCursorReplayRetentionAndIdempotency(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	generationA, err := s.ReserveHostRuntimeRun(ctx, "session-a", "run-a")
	if err != nil || generationA != 1 {
		t.Fatalf("reserve first generation = %d, %v", generationA, err)
	}
	event := func(runID string, generation int64, sourceID string, sequence uint64, kind string) HostRuntimeEvent {
		return HostRuntimeEvent{
			SessionID:         "session-a",
			RuntimeRunID:      runID,
			RuntimeGeneration: generation,
			SourceEventID:     sourceID,
			SourceSequence:    sequence,
			Kind:              kind,
			OccurredAt:        "2026-09-05T12:00:00Z",
			Source:            HostRuntimeEventSource{Channel: "jsonrpc", Confidence: "exact"},
			Payload:           json.RawMessage(`{"state":"ready"}`),
			PayloadVisibility: "public_metadata",
		}
	}

	first, inserted, err := s.AppendHostRuntimeEvent(ctx, event("run-a", generationA, "source-1", 44, "session.ready"), 2)
	if err != nil || !inserted || first.Cursor != 1 {
		t.Fatalf("first append = (%+v, %v, %v), want cursor 1 inserted", first, inserted, err)
	}
	duplicate, inserted, err := s.AppendHostRuntimeEvent(ctx, event("run-a", generationA, "source-1", 44, "session.ready"), 2)
	if err != nil || inserted || duplicate.Cursor != first.Cursor {
		t.Fatalf("duplicate append = (%+v, %v, %v), want existing cursor", duplicate, inserted, err)
	}
	conflict := event("run-a", generationA, "source-1", 44, "turn.failed")
	if _, _, conflictErr := s.AppendHostRuntimeEvent(ctx, conflict, 2); conflictErr == nil || !strings.Contains(conflictErr.Error(), "different contents") {
		t.Fatalf("conflicting duplicate error = %v, want integrity error", conflictErr)
	}

	// A recovered runtime can restart its source sequence. The host cursor
	// remains monotonic and run identity keeps the source identities distinct.
	generationB, err := s.ReserveHostRuntimeRun(ctx, "session-a", "run-b")
	if err != nil || generationB != 2 {
		t.Fatalf("reserve recovery generation = %d, %v", generationB, err)
	}
	second, _, err := s.AppendHostRuntimeEvent(ctx, event("run-b", generationB, "source-2", 1, "process.started"), 2)
	if err != nil || second.Cursor != 2 || second.SourceSequence != 1 || second.RuntimeGeneration != 2 {
		t.Fatalf("relaunch append = (%+v, %v), want host cursor 2/source sequence 1/generation 2", second, err)
	}
	third, _, err := s.AppendHostRuntimeEvent(ctx, event("run-b", generationB, "source-3", 2, "session.ready"), 2)
	if err != nil || third.Cursor != 3 {
		t.Fatalf("third append = (%+v, %v), want cursor 3", third, err)
	}
	dbPath := s.dbPath
	if closeErr := s.Close(ctx); closeErr != nil {
		t.Fatalf("close before replay: %v", closeErr)
	}
	s, err = New(ctx, dbPath)
	if err != nil {
		t.Fatalf("reopen before replay: %v", err)
	}
	defer func() { _ = s.Close(context.Background()) }()
	if generation, reserveErr := s.ReserveHostRuntimeRun(ctx, "session-a", "run-c"); reserveErr != nil || generation != 3 {
		t.Fatalf("generation after reopen = %d, %v, want durable generation 3", generation, reserveErr)
	}

	replay, err := s.HostRuntimeEventsAfter(ctx, "session-a", 0, 512)
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if replay.Gap == nil || replay.Gap.Reason != "retention" || replay.Gap.OldestAvailable != 2 {
		t.Fatalf("gap = %+v, want explicit retention gap through cursor 1", replay.Gap)
	}
	if len(replay.Events) != 2 || replay.Events[0].Cursor != 2 || replay.Events[1].Cursor != 3 {
		t.Fatalf("retained events = %+v, want cursors 2,3", replay.Events)
	}
	// The compact identity survives public-event pruning.
	prunedDuplicate, inserted, err := s.AppendHostRuntimeEvent(ctx, event("run-a", generationA, "source-1", 44, "session.ready"), 2)
	if err != nil || inserted || prunedDuplicate.Cursor != 1 {
		t.Fatalf("duplicate after event prune = (%+v, %v, %v), want original cursor 1", prunedDuplicate, inserted, err)
	}
	prunedConflict := event("run-a", generationA, "source-1", 44, "turn.failed")
	if _, _, conflictErr := s.AppendHostRuntimeEvent(ctx, prunedConflict, 2); conflictErr == nil || !strings.Contains(conflictErr.Error(), "different contents") {
		t.Fatalf("conflict after event prune error = %v, want integrity error", conflictErr)
	}

	afterTwo, err := s.HostRuntimeEventsAfter(ctx, "session-a", 2, 1)
	if err != nil || afterTwo.Gap != nil || len(afterTwo.Events) != 1 || afterTwo.Events[0].Cursor != 3 {
		t.Fatalf("after cursor 2 = %+v, %v", afterTwo, err)
	}

}

func TestHostRuntimeReplayGapCarriesCurrentGenerationFloor(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	genA, err := s.ReserveHostRuntimeRun(ctx, "session-floor", "run-a")
	if err != nil {
		t.Fatal(err)
	}
	event := func(runID, sourceID, kind string, generation int64) HostRuntimeEvent {
		return HostRuntimeEvent{
			SessionID: "session-floor", RuntimeRunID: runID, RuntimeGeneration: generation,
			SourceEventID: sourceID, SourceSequence: 1, Kind: kind, OccurredAt: "2026-09-05T12:00:00Z",
			Source: HostRuntimeEventSource{Channel: "test"}, Payload: json.RawMessage(`{}`), PayloadVisibility: "public_metadata",
		}
	}
	if _, _, appendErr := s.AppendHostRuntimeEvent(ctx, event("run-a", "a-ready", "session.ready", genA), 1); appendErr != nil {
		t.Fatal(appendErr)
	}
	genB, err := s.ReserveHostRuntimeRun(ctx, "session-floor", "run-b")
	if err != nil || genB != 2 {
		t.Fatalf("reserve successor = %d, %v", genB, err)
	}
	if _, _, appendErr := s.AppendHostRuntimeEvent(ctx, event("run-b", "b-ready", "session.ready", genB), 1); appendErr != nil {
		t.Fatal(appendErr)
	}
	if _, _, appendErr := s.AppendHostRuntimeEvent(ctx, event("run-a", "a-late-exit", "process.exited", genA), 1); appendErr != nil {
		t.Fatal(appendErr)
	}
	replay, err := s.HostRuntimeEventsAfter(ctx, "session-floor", 0, 10)
	if err != nil || replay.Gap == nil || len(replay.Events) != 1 {
		t.Fatalf("floor replay = %+v, %v", replay, err)
	}
	if replay.Gap.RuntimeGenerationFloor != 2 || replay.Gap.CurrentRuntimeRunID != "run-b" || replay.Events[0].RuntimeGeneration != 1 {
		t.Fatalf("floor/current/retained predecessor = %+v / %+v", replay.Gap, replay.Events)
	}
	if replay.Head.RuntimeGenerationFloor != 2 || replay.Head.CurrentRuntimeRunID != "run-b" || replay.Head.LatestCursor != 3 || replay.Head.PrunedThroughCursor != 2 {
		t.Fatalf("transactional replay head = %+v", replay.Head)
	}
}

func TestHostRuntimeReplayHeadPrecedesGapFreeDelayedPredecessor(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	genA, err := s.ReserveHostRuntimeRun(ctx, "session-head", "run-a")
	if err != nil {
		t.Fatal(err)
	}
	event := func(sourceID, kind string) HostRuntimeEvent {
		return HostRuntimeEvent{
			SessionID: "session-head", RuntimeRunID: "run-a", RuntimeGeneration: genA,
			SourceEventID: sourceID, SourceSequence: 1, Kind: kind, OccurredAt: "2026-09-05T12:00:00Z",
			Source: HostRuntimeEventSource{Channel: "test"}, Payload: json.RawMessage(`{}`), PayloadVisibility: "public_metadata",
		}
	}
	if _, _, appendErr := s.AppendHostRuntimeEvent(ctx, event("a-ready", "session.ready"), 10); appendErr != nil {
		t.Fatal(appendErr)
	}
	genB, err := s.ReserveHostRuntimeRun(ctx, "session-head", "run-b")
	if err != nil || genB != 2 {
		t.Fatalf("reserve successor = %d, %v", genB, err)
	}
	if _, _, appendErr := s.AppendHostRuntimeEvent(ctx, event("a-late-exit", "process.exited"), 10); appendErr != nil {
		t.Fatal(appendErr)
	}
	replay, err := s.HostRuntimeEventsAfter(ctx, "session-head", 0, 10)
	if err != nil || replay.Gap != nil || len(replay.Events) != 2 {
		t.Fatalf("gap-free replay = %+v, %v", replay, err)
	}
	if replay.Head.SchemaVersion != "host_runtime.head.v1" || replay.Head.RuntimeGenerationFloor != 2 || replay.Head.CurrentRuntimeRunID != "run-b" || replay.Head.LatestCursor != 2 {
		t.Fatalf("authoritative head = %+v", replay.Head)
	}
	if replay.Events[1].RuntimeGeneration != 1 || replay.Events[1].Kind != "process.exited" {
		t.Fatalf("retained delayed predecessor = %+v", replay.Events[1])
	}
}

func TestHostRuntimeEmptyReplayRewindsCursorAhead(t *testing.T) {
	s := newTestStore(t)
	replay, err := s.HostRuntimeEventsAfter(context.Background(), "empty-session", 99, 10)
	if err != nil || replay.Gap == nil {
		t.Fatalf("empty cursor-ahead replay = %+v, %v", replay, err)
	}
	if replay.Head.SchemaVersion != "host_runtime.head.v1" || replay.Head.LatestCursor != 0 || replay.Gap.Reason != "cursor_ahead" || replay.Gap.OldestAvailable != 1 || replay.Gap.LatestCursor != 0 {
		t.Fatalf("empty replay authority = head %+v gap %+v", replay.Head, replay.Gap)
	}
}

func TestHostRuntimeIdentityLedgerHasDocumentedBound(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	if generation, err := s.ReserveHostRuntimeRun(ctx, "session-identities", "run-a"); err != nil || generation != 1 {
		t.Fatalf("reserve = %d, %v", generation, err)
	}
	first := HostRuntimeEvent{
		SessionID: "session-identities", RuntimeRunID: "run-a", RuntimeGeneration: 1,
		SourceEventID: "expired-source", SourceSequence: 1, Kind: "session.ready", OccurredAt: "2026-09-05T12:00:00Z",
		Source: HostRuntimeEventSource{Channel: "test"}, Payload: json.RawMessage(`{}`), PayloadVisibility: "public_metadata",
	}
	if _, _, err := s.AppendHostRuntimeEvent(ctx, first, 2); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.ExecContext(ctx, `UPDATE host_runtime_feed_heads SET last_cursor = ? WHERE session_id = ?`, HostRuntimeFeedIdentityRetention, first.SessionID); err != nil {
		t.Fatal(err)
	}
	newer := first
	newer.SourceEventID = "newer-source"
	if _, _, err := s.AppendHostRuntimeEvent(ctx, newer, 2); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM host_runtime_feed_identities WHERE session_id = ?`, first.SessionID).Scan(&count); err != nil || count > HostRuntimeFeedIdentityRetention {
		t.Fatalf("identity rows = %d, %v", count, err)
	}
	accepted, inserted, err := s.AppendHostRuntimeEvent(ctx, first, 2)
	if err != nil || !inserted || accepted.Cursor <= HostRuntimeFeedIdentityRetention+1 {
		t.Fatalf("identity beyond documented horizon = (%+v, %v, %v), want new event", accepted, inserted, err)
	}
}

func TestMigration157HostRuntimeFeedDownAndUp(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	migrationsDir, err := fs.Sub(migrationsFS, "migrations")
	if err != nil {
		t.Fatalf("migration filesystem: %v", err)
	}
	provider, err := goose.NewProvider(goose.DialectSQLite3, s.DB, migrationsDir, goose.WithVerbose(false))
	if err != nil {
		t.Fatalf("migration provider: %v", err)
	}
	if _, err := provider.DownTo(ctx, 156); err != nil {
		t.Fatalf("down migration 157: %v", err)
	}
	for _, table := range []string{"host_runtime_feed_identities", "host_runtime_feed_events", "host_runtime_feed_heads"} {
		exists, err := s.tableExists(ctx, table)
		if err != nil || exists {
			t.Fatalf("table %s after down = %v, %v", table, exists, err)
		}
	}
	if _, err := provider.Up(ctx); err != nil {
		t.Fatalf("reapply migration 157: %v", err)
	}
	if generation, err := s.ReserveHostRuntimeRun(ctx, "session-after-up", "run-after-up"); err != nil || generation != 1 {
		t.Fatalf("reserve after migration reapply = %d, %v", generation, err)
	}
}

func TestHostRuntimeFeedSessionIsolationAndCursorAheadGap(t *testing.T) {
	s := newTestStore(t)
	if generation, err := s.ReserveHostRuntimeRun(context.Background(), "session-b", "run-b"); err != nil || generation != 1 {
		t.Fatalf("reserve session-b run = %d, %v", generation, err)
	}
	event := HostRuntimeEvent{
		SessionID:         "session-b",
		RuntimeRunID:      "run-b",
		RuntimeGeneration: 1,
		SourceEventID:     "source-b",
		SourceSequence:    1,
		Kind:              "session.ready",
		OccurredAt:        "2026-09-05T12:00:00Z",
		Source:            HostRuntimeEventSource{Channel: "stdio"},
		Payload:           json.RawMessage(`{}`),
		PayloadVisibility: "public_metadata",
	}
	if _, _, err := s.AppendHostRuntimeEvent(context.Background(), event, 10); err != nil {
		t.Fatalf("append: %v", err)
	}
	empty, err := s.HostRuntimeEventsAfter(context.Background(), "session-a", 0, 10)
	if err != nil || len(empty.Events) != 0 || empty.Gap != nil {
		t.Fatalf("cross-session replay = %+v, %v", empty, err)
	}
	ahead, err := s.HostRuntimeEventsAfter(context.Background(), "session-b", 99, 10)
	if err != nil || ahead.Gap == nil || ahead.Gap.Reason != "cursor_ahead" {
		t.Fatalf("ahead replay = %+v, %v", ahead, err)
	}
}

func TestHostRuntimeFeedRejectsOversizePublicEventAtPersistenceBoundary(t *testing.T) {
	s := newTestStore(t)
	event := HostRuntimeEvent{
		SessionID:         "session-bound",
		RuntimeRunID:      "run-bound",
		RuntimeGeneration: 1,
		SourceEventID:     "source-bound",
		Kind:              "future.kind",
		OccurredAt:        "2026-09-05T12:00:00Z",
		Source:            HostRuntimeEventSource{Channel: "test"},
		Payload:           json.RawMessage(`{"content":"` + strings.Repeat("x", HostRuntimeFeedMaxEventBytes) + `"}`),
		PayloadVisibility: "test",
	}
	if _, _, err := s.AppendHostRuntimeEvent(context.Background(), event, 10); err == nil || !strings.Contains(err.Error(), "public bound") {
		t.Fatalf("oversize append error = %v, want public bound rejection", err)
	}
}
