package workflowhost

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestPreflightCopyReadOnlyProcess(t *testing.T) {
	path := os.Getenv("NANITE_PREFLIGHT_READER_COPY")
	if path == "" {
		return
	}
	dsn := url.URL{Scheme: "file", Path: path, RawQuery: "mode=ro"}
	db, err := sql.Open("sqlite", dsn.String())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	tx, err := db.BeginTx(t.Context(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	var count int
	if err := tx.QueryRowContext(t.Context(), `SELECT count(*) FROM workflow_runs`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("reader count=%d error=%v", count, err)
	}
	fmt.Println("reader-ready")
	if _, err := io.Copy(io.Discard, os.Stdin); err != nil {
		t.Fatal(err)
	}
}

func TestPreflightCopyWALAndBackupPreserveBytes(t *testing.T) {
	for _, method := range []string{"stopped-writer-wal-copy", "sqlite-backup"} {
		t.Run(method, func(t *testing.T) {
			f := newSQLFacadeFixture(t, false, true)
			if _, err := f.product.DB.ExecContext(t.Context(), `PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
				t.Fatal(err)
			}
			if _, err := f.product.DB.ExecContext(t.Context(), `PRAGMA wal_autocheckpoint=0`); err != nil {
				t.Fatal(err)
			}
			sqlFacadeStartAttempt(t, f)
			source := f.product.DBPath(t.Context())
			path := filepath.Join(t.TempDir(), "offline copy.db")
			if method == "stopped-writer-wal-copy" {
				for _, suffix := range []string{"", "-wal", "-shm"} {
					if err := copyPreflightFile(t.Context(), source+suffix, path+suffix, suffix != ""); err != nil {
						t.Fatal(err)
					}
				}
				wal, err := os.Stat(path + "-wal")
				if err != nil || wal.Size() <= 32 {
					t.Fatalf("fixture has no committed WAL: %v", err)
				}
				// The run is WAL-only: validating immutable/main-file-only would
				// silently miss exactly the production rows being rehearsed.
				mainOnly := url.URL{Scheme: "file", Path: path, RawQuery: "mode=ro&immutable=1"}
				db, err := sql.Open("sqlite", mainOnly.String())
				if err != nil {
					t.Fatal(err)
				}
				var count int
				err = db.QueryRowContext(t.Context(), `SELECT count(*) FROM workflow_runs`).Scan(&count)
				_ = db.Close()
				if err != nil || count != 0 {
					t.Fatalf("main-file-only control count=%d error=%v", count, err)
				}
			} else {
				// Use the real supported operator copy method, including WAL.
				command := exec.CommandContext(t.Context(), "sqlite3", source, ".backup \""+path+"\"") //nolint:gosec // Fixed SQLite CLI, arguments are private fixture paths.
				if output, err := command.CombinedOutput(); err != nil {
					t.Fatalf("sqlite3 .backup: %v %s", err, output)
				}
			}
			stopReader := holdPreflightCopyReader(t, path)
			defer stopReader()
			before := preflightCopyFileBytes(t, path)
			var output bytes.Buffer
			if err := PreflightWorkflowStorageCopy(t.Context(), path, &output); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(before, preflightCopyFileBytes(t, path)) {
				t.Fatal("preflight changed supplied DB/WAL/SHM bytes or file presence")
			}
			for _, expected := range []string{"validation=migration-ledger rows_scanned=", "validation=schema-shape rows_scanned=", "validation=foreign-keys rows_scanned=", "validation=run-bindings rows_scanned=1", "validation=records:nodes rows_scanned=1", "validation=records:attempts rows_scanned=1", "validation=claim-lease-generations rows_scanned=1", "validation=frozen-material rows_scanned=1", "validation=run-start-replay rows_scanned=1", "validation=claim-replay rows_scanned=1", "workflow storage preflight: PASS"} {
				if !strings.Contains(output.String(), expected) {
					t.Fatalf("missing coverage %q: %s", expected, &output)
				}
			}
		})
	}
}

func holdPreflightCopyReader(t *testing.T, path string) func() {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	command := exec.CommandContext(t.Context(), executable, "-test.run=^TestPreflightCopyReadOnlyProcess$", "-test.timeout=30s") //nolint:gosec // Reexecutes this test binary with fixed test name.
	command.Env = append(os.Environ(), "NANITE_PREFLIGHT_READER_COPY="+path, "HOME="+filepath.Join(t.TempDir(), "reader-home"))
	input, err := command.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	output, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	command.Stderr = &stderr
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	ready := make([]byte, len("reader-ready\n"))
	if _, err := io.ReadFull(output, ready); err != nil || string(ready) != "reader-ready\n" {
		_ = input.Close()
		_ = command.Wait()
		t.Fatalf("reader startup: %v %q %s", err, ready, &stderr)
	}
	return func() {
		_ = input.Close()
		if err := command.Wait(); err != nil {
			t.Errorf("read-only holder: %v %s", err, &stderr)
		}
	}
}

func preflightCopyFileBytes(t *testing.T, path string) map[string][]byte {
	t.Helper()
	files := make(map[string][]byte)
	for _, suffix := range []string{"", "-wal", "-shm"} {
		data, err := os.ReadFile(path + suffix) //nolint:gosec // Private fixture files whose preservation is the assertion.
		if os.IsNotExist(err) && suffix != "" {
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		files[suffix] = data
	}
	return files
}

func TestPreflightCopyRefusalPreservesBytes(t *testing.T) {
	f := newSQLFacadeFixture(t, false, true)
	if _, _, err := f.state.CreateRun(t.Context(), f.createRequest()); err != nil {
		t.Fatal(err)
	}
	if _, err := f.product.DB.ExecContext(t.Context(), `UPDATE workflow_runs SET engine_contract_version='unsupported'`); err != nil {
		t.Fatal(err)
	}
	path := f.product.DBPath(t.Context())
	before := preflightCopyFileBytes(t, path)
	var output bytes.Buffer
	err := PreflightWorkflowStorageCopy(context.Background(), path, &output)
	if err == nil || !strings.Contains(err.Error(), `run "host-run" blocked`) || !strings.Contains(err.Error(), "unsupported engine identity") || !strings.Contains(err.Error(), "operator: keep the service stopped") {
		t.Fatalf("refusal: %v", err)
	}
	if !reflect.DeepEqual(before, preflightCopyFileBytes(t, path)) || strings.Contains(output.String(), "PASS") || !strings.Contains(output.String(), "validation=run-bindings rows_scanned=1") {
		t.Fatalf("refusal mutated copy or misreported coverage: %s", &output)
	}
}
