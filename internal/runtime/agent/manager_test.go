package agent

import (
	"context"
	"strings"
	"testing"
)

// TestSession_NilGuards confirms each lifecycle method clear-errors on a
// zero-value Session rather than panicking.
func TestSession_NilGuards(t *testing.T) {
	var nilSess *Session
	if err := nilSess.SendInput([]byte("x")); err == nil {
		t.Errorf("nil Session.SendInput should error")
	}
	if err := nilSess.Stop(context.Background()); err == nil {
		t.Errorf("nil Session.Stop should error")
	}
	if err := nilSess.Wait(context.Background()); err == nil {
		t.Errorf("nil Session.Wait should error")
	}
	if _, err := nilSess.Checkpoint(context.Background()); err == nil {
		t.Errorf("nil Session.Checkpoint should error")
	}

	// Session with deps but nil SessionsManager — same pattern.
	zero := &Session{deps: &Dependencies{}}
	if err := zero.SendInput([]byte("x")); err == nil {
		t.Errorf("zero Session.SendInput should error on nil SessionsManager")
	}
	if err := zero.Stop(context.Background()); err == nil ||
		!strings.Contains(err.Error(), "session not initialized") {
		t.Errorf("zero Session.Stop expected init error, got %v", err)
	}
}

// TestSession_StopHappyPath spins up a real Boot session against the codex
// adapter shape (PTY=false, AutoFireFirstTurn=false for ModeLongLived) and
// confirms Stop runs without error and removes the boot dir.
func TestSession_StopHappyPath(t *testing.T) {
	deps, _ := makeBootDeps(t, "codex")
	sess, err := Boot(context.Background(), deps, Options{
		Mode:    ModeLongLived,
		Workdir: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("Boot: %v", err)
	}

	bootDir := sess.BootDir
	if err := sess.Stop(context.Background()); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	// Boot dir should be removed after Stop.
	if _, err := osStatNoErr(bootDir); err == nil {
		t.Errorf("boot dir %s still exists after Stop", bootDir)
	}
}

// TestSession_Checkpoint_StubReturnsEmpty until the lib surfaces a
// Manager-level checkpoint API.
func TestSession_Checkpoint_StubReturnsEmpty(t *testing.T) {
	deps, _ := makeBootDeps(t, "codex")
	sess, err := Boot(context.Background(), deps, Options{
		Mode:    ModeLongLived,
		Workdir: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("Boot: %v", err)
	}
	t.Cleanup(func() { _ = sess.Stop(context.Background()) })

	id, err := sess.Checkpoint(context.Background())
	if err != nil {
		t.Errorf("Checkpoint: %v", err)
	}
	if id != "" {
		t.Errorf("Checkpoint id = %q, want empty stub", id)
	}
}
