package store

import "testing"

// Plant a real historical row at the previous schema, then migrate and replay.
// A fresh empty fixture would not catch a migration rewriting old estimates.
func TestMigration171PreservesHistoricalUsage(t *testing.T) {
	st := newTestStore(t)
	ctx := t.Context()
	provider := newMigrationProvider(t, st)
	if _, err := provider.DownTo(ctx, 169); err != nil {
		t.Fatal(err)
	}
	session := &Session{}
	if err := st.CreateSession(ctx, session); err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB.ExecContext(ctx, `INSERT INTO token_usage (session_id,message_id,model,input_tokens,output_tokens,total_tokens,estimated_cost_usd) VALUES (?,?,?,?,?,?,?)`, session.ID, "historical", "claude-sonnet-4-20250514", 1000, 500, 1500, .125); err != nil {
		t.Fatal(err)
	}
	if _, err := provider.Up(ctx); err != nil {
		t.Fatal(err)
	}
	assert := func() {
		t.Helper()
		var cost float64
		var total int
		var status, raw, providerID string
		if err := st.DB.QueryRowContext(ctx, `SELECT estimated_cost_usd,total_tokens,cost_status,cost_snapshot,provider FROM token_usage WHERE message_id=?`, "historical").Scan(&cost, &total, &status, &raw, &providerID); err != nil {
			t.Fatal(err)
		}
		if cost != .125 || total != 1500 || status != "PARTIAL" || raw != "" || providerID != "" {
			t.Fatalf("history changed: cost=%v total=%d status=%s snapshot=%s provider=%s", cost, total, status, raw, providerID)
		}
	}
	assert()
	if _, err := provider.DownTo(ctx, 169); err != nil {
		t.Fatal(err)
	}
	if _, err := provider.Up(ctx); err != nil {
		t.Fatal(err)
	}
	assert()
	if err := st.migrate(ctx); err != nil {
		t.Fatal(err)
	}
	assert()
}
