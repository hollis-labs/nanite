package selftools

// Retained mutable dispatch rules are historical data; the matcher must leave them inert.

import (
	"bytes"
	"log/slog"
	"testing"

	"github.com/hollis-labs/nanite/internal/store"
)

func TestMatchDispatchToAgentReflex_KindLookupDegraded_LogsMultiCandidateCanary(t *testing.T) {
	s := newTestStore(t)
	historical := retainedSelftoolsDispatchProfile(t, s, "retained-dispatch-multi")
	for i, name := range []string{"retained-candidate-a", "retained-candidate-b"} {
		retainedSelftoolsDispatchRule(t, s, store.AgentReflex{Name: name, Priority: int64(50 - i), FiredCount: int64(i + 2)})
	}
	// Private corruption control: kind lookup cannot re-enable retained evaluation.
	if _, err := s.DB.ExecContext(t.Context(), `PRAGMA foreign_keys=OFF`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.ExecContext(t.Context(), `DELETE FROM reflex_action_kinds WHERE name='dispatch_to_agent'`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.ExecContext(t.Context(), `PRAGMA foreign_keys=ON`); err != nil {
		t.Fatal(err)
	}
	before := selftoolsDispatchSnapshot(t, s)
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })
	st := newTestSelfToolsTransport(s)
	if hints := st.matchDispatchToAgentReflex(t.Context(), "sess-multi", historical.ID, "probe-multi-candidate-token please route this"); hints != nil {
		t.Fatalf("degraded kind lookup evaluated retained candidates: %+v", hints)
	}
	if logged := buf.String(); logged != "" {
		t.Fatalf("retired matcher emitted evaluation/canary telemetry: %s", logged)
	}
	requireSelftoolsDispatchUnchanged(t, s, before)
}
