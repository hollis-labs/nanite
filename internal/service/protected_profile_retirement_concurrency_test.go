package service

import (
	"database/sql"
	"errors"
	"testing"

	"github.com/hollis-labs/nanite/internal/store"
)

func TestProtectedProfileRetirementConcurrentExportsCommitOneAudit(t *testing.T) {
	svc, st, _, host, p, req := protectedRetirementFixture(t, "system", "")
	ctx := t.Context()
	other, err := store.New(ctx, st.DBPath(ctx))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = other.Close(ctx) })
	first, err := svc.ExportProtectedProfile(ctx, host, p.ID, req)
	if err != nil {
		t.Fatal(err)
	}
	second, err := svc.ExportProtectedProfile(ctx, host, p.ID, req)
	if err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	type outcome struct {
		exportID string
		err      error
	}
	results := make(chan outcome, 2)
	for i, receipt := range []ProtectedRetirementReceipt{first, second} {
		writer := st
		if i == 1 {
			writer = other
		}
		go func() {
			<-start
			results <- outcome{receipt.ExportID, writer.RetireExportedProtectedProfile(ctx, p.ID, receipt.ExportID, receipt.Digest, receipt.Authority)}
		}()
	}
	close(start)
	winner := ""
	for range 2 {
		result := <-results
		if result.err == nil {
			if winner != "" {
				t.Fatal("competing exports both committed")
			}
			winner = result.exportID
		} else if !errors.Is(result.err, sql.ErrNoRows) {
			t.Fatal("competing export did not refuse changed state", result.err)
		}
	}
	if winner == "" {
		t.Fatal("neither unchanged export committed")
	}
	var auditedExport string
	if queryErr := st.DB.QueryRowContext(ctx, `SELECT export_id FROM profile_ingestion_retirements WHERE profile_id=?`, p.ID).Scan(&auditedExport); queryErr != nil || auditedExport != winner {
		t.Fatal("audit does not identify the winning transaction", queryErr)
	}
	if _, lookupErr := st.GetHistoricalAgentProfile(ctx, p.ID); !errors.Is(lookupErr, sql.ErrNoRows) {
		t.Fatal("committed retirement left the historical profile", lookupErr)
	}
	keep, err := st.GetHistoricalAgentProfile(ctx, req.KeepProfileID)
	if err != nil || keep.Revision != req.KeepRevision {
		t.Fatal("competing retirement changed the exact keep identity", err)
	}
}
