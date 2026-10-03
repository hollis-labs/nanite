package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/internal/storetest"
)

func TestWorkflowPreflightCLI(t *testing.T) {
	product, err := storetest.New(t, t.Context(), filepath.Join(t.TempDir(), "copy.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = product.Close(t.Context()) })
	if _, execErr := product.DB.ExecContext(t.Context(), `INSERT INTO workflow_runs(id,definition_name,status,started_at) VALUES('legacy-run','legacy','completed','2026-09-01T00:00:00Z')`); execErr != nil {
		t.Fatal(execErr)
	}
	var stdout, stderr bytes.Buffer
	if commandErr := cmdWorkflow(t.Context(), []string{"preflight", "--db", product.DBPath(t.Context())}, &stdout, &stderr); commandErr != nil {
		t.Fatal(commandErr)
	}
	if !strings.Contains(stdout.String(), "validation=run-bindings rows_scanned=1") || !strings.Contains(stdout.String(), "workflow storage preflight: PASS") || stderr.Len() != 0 {
		t.Fatalf("stdout=%s stderr=%s", &stdout, &stderr)
	}
	for _, args := range [][]string{nil, {"other"}, {"preflight"}, {"preflight", "--db", "copy", "unexpected"}} {
		if commandErr := cmdWorkflow(t.Context(), args, &stdout, &stderr); commandErr == nil {
			t.Fatalf("accepted invalid arguments: %v", args)
		}
	}
	if _, execErr := product.DB.ExecContext(t.Context(), `UPDATE workflow_runs SET engine_kind='go_workflow_v1',engine_contract_version='unsupported'`); execErr != nil {
		t.Fatal(execErr)
	}
	stdout.Reset()
	err = cmdWorkflow(t.Context(), []string{"preflight", "--db", product.DBPath(t.Context())}, &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), `run "legacy-run" blocked`) || strings.Contains(stdout.String(), "PASS") {
		t.Fatalf("refusal=%v stdout=%s", err, &stdout)
	}
}

func TestWorkflowPreflightProcessExit(t *testing.T) {
	if encoded := os.Getenv("NANITE_PREFLIGHT_CLI_ARGS"); encoded != "" {
		var args []string
		if err := json.Unmarshal([]byte(encoded), &args); err != nil {
			t.Fatal(err)
		}
		os.Args = append([]string{"nanite"}, args...)
		main()
		return
	}
	product, err := storetest.New(t, t.Context(), filepath.Join(t.TempDir(), "exit-copy.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = product.Close(t.Context()) })
	if _, err := product.DB.ExecContext(t.Context(), `INSERT INTO workflow_runs(id,definition_name,status,started_at) VALUES('exit-run','legacy','completed','2026-09-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	for _, refusal := range []bool{false, true} {
		if refusal {
			if _, err := product.DB.ExecContext(t.Context(), `UPDATE workflow_runs SET engine_kind='go_workflow_v1',engine_contract_version='unsupported'`); err != nil {
				t.Fatal(err)
			}
		}
		args, err := json.Marshal([]string{"workflow", "preflight", "--db", product.DBPath(t.Context())})
		if err != nil {
			t.Fatal(err)
		}
		executable, err := os.Executable()
		if err != nil {
			t.Fatal(err)
		}
		command := exec.CommandContext(t.Context(), executable, "-test.run=^TestWorkflowPreflightProcessExit$", "-test.timeout=30s") //nolint:gosec // Reexecutes this test binary to verify main's real exit status.
		command.Env = append(os.Environ(), "NANITE_PREFLIGHT_CLI_ARGS="+string(args), "HOME="+filepath.Join(t.TempDir(), "cli-home"))
		var stdout, stderr bytes.Buffer
		command.Stdout, command.Stderr = &stdout, &stderr
		err = command.Run()
		if !refusal {
			if err != nil || !strings.Contains(stdout.String(), "workflow storage preflight: PASS") || stderr.Len() != 0 {
				t.Fatalf("pass: %v stdout=%s stderr=%s", err, &stdout, &stderr)
			}
		} else {
			var exit *exec.ExitError
			if !errors.As(err, &exit) || exit.ExitCode() != 1 || !strings.Contains(stderr.String(), `run "exit-run" blocked`) || !strings.Contains(stderr.String(), "operator: keep the service stopped") || strings.Contains(stdout.String(), "workflow storage preflight: PASS") {
				t.Fatalf("refusal: %v stdout=%s stderr=%s", err, &stdout, &stderr)
			}
		}
	}
}
