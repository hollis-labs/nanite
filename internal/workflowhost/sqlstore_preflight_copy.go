package workflowhost

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
)

type preflightCoverageKey struct{}

type preflightCoverage struct {
	order []string
	rows  map[string]int64
}

func preflightScanned(ctx context.Context, validation string, rows int64) {
	coverage, _ := ctx.Value(preflightCoverageKey{}).(*preflightCoverage)
	if coverage == nil {
		return
	}
	if _, present := coverage.rows[validation]; !present {
		coverage.order = append(coverage.order, validation)
	}
	coverage.rows[validation] += rows
}

// PreflightWorkflowStorageCopy validates a stable, offline SQLite copy without
// opening the supplied files through SQLite. WAL readers may write SHM even in
// mode=ro, so only private staging files are exposed to SQLite. Copy the DB and
// its WAL together while the service is stopped, or use SQLite's .backup. SHM
// is transient and is reconstructed privately; immutable=1 would miss WAL data.
func PreflightWorkflowStorageCopy(ctx context.Context, path string, output io.Writer) (result error) {
	if err := checkWorkflowContext(ctx); err != nil {
		return err
	}
	if output == nil {
		return fmt.Errorf("workflow preflight requires a coverage output writer")
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	if info, statErr := os.Stat(absolute + "-journal"); statErr == nil && info.Size() > 0 {
		return fmt.Errorf("workflow preflight: rollback journal present; operator: obtain a completed SQLite .backup before rehearsal")
	} else if statErr != nil && !os.IsNotExist(statErr) {
		return statErr
	}
	staging, err := os.MkdirTemp("", "nanite-workflow-preflight-")
	if err != nil {
		return err
	}
	defer func() {
		if cleanupErr := os.RemoveAll(staging); cleanupErr != nil && result == nil {
			result = fmt.Errorf("remove workflow preflight staging: %w", cleanupErr)
		}
	}()
	stagedPath := filepath.Join(staging, "copy.db")
	for _, suffix := range []string{"", "-wal"} {
		if copyErr := copyPreflightFile(ctx, absolute+suffix, stagedPath+suffix, suffix != ""); copyErr != nil {
			return copyErr
		}
	}
	dsn := url.URL{Scheme: "file", Path: stagedPath}
	parameters := url.Values{"mode": {"ro"}, "_pragma": {"foreign_keys(1)", "query_only(1)", "busy_timeout(5000)"}}
	dsn.RawQuery = parameters.Encode()
	db, err := sql.Open("sqlite", dsn.String())
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	db.SetMaxOpenConns(1)
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return fmt.Errorf("workflow preflight: open read-only snapshot: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	coverage := &preflightCoverage{rows: make(map[string]int64)}
	validationErr := preflightWorkflowSnapshot(context.WithValue(ctx, preflightCoverageKey{}, coverage), tx)
	for _, validation := range coverage.order {
		if _, writeErr := fmt.Fprintf(output, "validation=%s rows_scanned=%d\n", validation, coverage.rows[validation]); writeErr != nil {
			return fmt.Errorf("write workflow preflight coverage: %w", writeErr)
		}
	}
	if validationErr != nil {
		return validationErr
	}
	if rollbackErr := tx.Rollback(); rollbackErr != nil {
		return fmt.Errorf("workflow preflight: release read-only snapshot: %w", rollbackErr)
	}
	if closeErr := db.Close(); closeErr != nil {
		return fmt.Errorf("close workflow preflight snapshot: %w", closeErr)
	}
	if cleanupErr := os.RemoveAll(staging); cleanupErr != nil {
		return fmt.Errorf("remove workflow preflight staging: %w", cleanupErr)
	}
	_, err = fmt.Fprintln(output, "workflow storage preflight: PASS (read-only; no migrations, seeding, cutover or workers)")
	return err
}

func copyPreflightFile(ctx context.Context, source, target string, optional bool) error {
	input, err := os.Open(source) //nolint:gosec // Reads the explicitly supplied offline database/WAL; never writes the source.
	if optional && os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("workflow preflight: read copy %q: %w", source, err)
	}
	defer func() { _ = input.Close() }()
	before, err := input.Stat()
	if err != nil {
		return err
	}
	if !before.Mode().IsRegular() {
		return fmt.Errorf("workflow preflight: %q is not a regular database file", source)
	}
	output, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600) //nolint:gosec // Target is inside private MkdirTemp staging, or an isolated fixture directory.
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(output, input)
	closeErr := output.Close()
	if copyErr != nil {
		return copyErr
	}
	if closeErr != nil {
		return closeErr
	}
	after, err := input.Stat()
	if err != nil {
		return err
	}
	if before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) {
		return fmt.Errorf("workflow preflight: copy changed while reading %q; operator: stop writers or obtain a SQLite .backup", source)
	}
	return ctx.Err()
}
