package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/internal/store"
)

// skipIfNoOSSandbox skips a test on Linux when bwrap is unavailable and the
// AD-01 degraded-mode opt-in (TASKS/audit-remediation/
// ARCHITECT-DECISIONS.md) is not set. sandbox.UserExec (which
// handleShellExec calls with Sandboxed: true for ask/session modes) now
// fails closed by default in that case — previously it silently fell back
// to Tier 1 only (GO-SEC4-001, the finding this fix closes). darwin
// (seatbelt always present) and a Linux host WITH bwrap installed are
// unaffected. Does not apply to YOLO-mode tests, which never call
// applyOSSandbox at all regardless of platform.
func skipIfNoOSSandbox(t *testing.T) {
	t.Helper()
	if runtime.GOOS != "linux" {
		return
	}
	switch strings.ToLower(strings.TrimSpace(os.Getenv("NANITE_ALLOW_UNSANDBOXED_AGENT_EXEC"))) {
	case "1", "true", "yes":
		return
	}
	if _, err := exec.LookPath("bwrap"); err != nil {
		t.Skip("bwrap not installed and NANITE_ALLOW_UNSANDBOXED_AGENT_EXEC not set — sandbox.UserExec now fails closed for Sandboxed: true (AD-01); see internal/sandbox/os_linux_test.go for the dedicated fail-closed/degraded regression tests")
	}
}

// TestHandleShellExec_YOLOMode_SandboxIsolatedFalse is AD-01's regression
// test (TASKS/audit-remediation/ARCHITECT-DECISIONS.md) for handleShellExec
// — the fourth production caller (internal/api/shell.go). YOLO mode is an
// explicit, already-disclosed user opt-out of the OS sandbox (Sandboxed:
// false), a different case in kind from a silent degradation per this
// task's own scope notes: sandbox_isolated must read false here, and the
// output must NOT be prefixed with the "degraded mode" warning that's
// reserved for the Sandboxed: true case.
func TestHandleShellExec_YOLOMode_SandboxIsolatedFalse(t *testing.T) {
	a, mux := newTestAPI(t)

	sess := &store.Session{
		ID:       "shell-yolo-sess",
		Title:    "Shell YOLO Test",
		Metadata: `{"shell_mode":"yolo"}`,
	}
	if err := a.Services.Store.CreateSession(context.Background(), sess); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	body, _ := json.Marshal(map[string]any{"command": "echo hello-yolo"})
	req := httptest.NewRequest("POST", "/api/sessions/shell-yolo-sess/shell-exec", bytes.NewReader(body))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v\nbody: %s", err, w.Body.String())
	}
	isolated, ok := resp["sandbox_isolated"].(bool)
	if !ok {
		t.Fatalf("response missing sandbox_isolated field: %v", resp)
	}
	if isolated {
		t.Error("sandbox_isolated = true in YOLO mode, want false — UserExec never applies the OS sandbox when Sandboxed is false")
	}
	output, _ := resp["output"].(string)
	if strings.Contains(output, "degraded mode") {
		t.Errorf("YOLO mode output contains the degraded-mode warning, want none (opt-out is expected here, not a degradation): %q", output)
	}
	if !strings.Contains(output, "hello-yolo") {
		t.Errorf("output = %q, want it to contain the echoed text", output)
	}
}

// TestHandleShellExec_SessionMode_SandboxIsolatedTrue exercises the
// Sandboxed: true path (session/ask modes) end to end through the real
// API handler. On darwin (seatbelt always present) this must read
// sandbox_isolated=true — confirming the new field is wired correctly
// for the case AD-01's "Desired invariant" actually cares about most: a
// caller who DID request isolation being able to tell whether they
// actually got it.
func TestHandleShellExec_SessionMode_SandboxIsolatedTrue(t *testing.T) {
	skipIfNoOSSandbox(t)
	a, mux := newTestAPI(t)

	sess := &store.Session{
		ID:       "shell-session-sess",
		Title:    "Shell Session Mode Test",
		Metadata: `{"shell_mode":"session"}`,
	}
	if err := a.Services.Store.CreateSession(context.Background(), sess); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	body, _ := json.Marshal(map[string]any{"command": "echo hello-session"})
	req := httptest.NewRequest("POST", "/api/sessions/shell-session-sess/shell-exec", bytes.NewReader(body))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v\nbody: %s", err, w.Body.String())
	}
	isolated, ok := resp["sandbox_isolated"].(bool)
	if !ok {
		t.Fatalf("response missing sandbox_isolated field: %v", resp)
	}
	if !isolated {
		t.Error("sandbox_isolated = false in session mode on a platform with OS sandbox support, want true")
	}
	output, _ := resp["output"].(string)
	if !strings.Contains(output, "hello-session") {
		t.Errorf("output = %q, want it to contain the echoed text", output)
	}
}

func TestSetSessionMetadataFieldRejectsCorruptMetadataWithoutOverwrite(t *testing.T) {
	a, _ := newTestAPI(t)
	const corruptMetadata = `{"preserve":`
	sess := &store.Session{
		ID:       "shell-corrupt-metadata",
		Title:    "Corrupt metadata",
		Metadata: corruptMetadata,
	}
	if err := a.Services.Store.CreateSession(context.Background(), sess); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	err := a.setSessionMetadataField(sess.ID, "shell_mode", "session")
	if err == nil || !strings.Contains(err.Error(), "parse session metadata") {
		t.Fatalf("setSessionMetadataField error = %v, want metadata parse error", err)
	}
	got, err := a.Services.Store.GetSession(context.Background(), sess.ID)
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if got.Metadata != corruptMetadata {
		t.Fatalf("metadata = %q, want corrupt value preserved as %q", got.Metadata, corruptMetadata)
	}
}
