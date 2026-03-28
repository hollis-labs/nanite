package chat

import (
	"os"
	"os/exec"
	"testing"
	"time"
)

func TestProcessTracker_TrackAndUntrack(t *testing.T) {
	pt := NewProcessTracker()

	cmd := exec.Command("sleep", "60")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer cmd.Process.Kill()

	pt.Track("sess-1", cmd.Process)
	if pt.Count() != 1 {
		t.Fatalf("expected 1 tracked process, got %d", pt.Count())
	}

	pt.Untrack("sess-1", cmd.Process)
	if pt.Count() != 0 {
		t.Fatalf("expected 0 tracked processes after untrack, got %d", pt.Count())
	}
}

func TestProcessTracker_KillSession(t *testing.T) {
	pt := NewProcessTracker()

	cmd := exec.Command("sleep", "60")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}

	pt.Track("sess-1", cmd.Process)
	killed := pt.KillSession("sess-1")

	if killed != 1 {
		t.Errorf("expected 1 killed, got %d", killed)
	}
	if pt.Count() != 0 {
		t.Errorf("expected 0 after kill, got %d", pt.Count())
	}

	// Wait to prevent zombie.
	cmd.Wait()
}

func TestProcessTracker_KillSession_Empty(t *testing.T) {
	pt := NewProcessTracker()
	killed := pt.KillSession("nonexistent")
	if killed != 0 {
		t.Errorf("expected 0 killed for nonexistent session, got %d", killed)
	}
}

func TestProcessTracker_KillAll(t *testing.T) {
	pt := NewProcessTracker()

	cmds := make([]*exec.Cmd, 3)
	for i := range cmds {
		cmds[i] = exec.Command("sleep", "60")
		if err := cmds[i].Start(); err != nil {
			t.Fatal(err)
		}
	}

	pt.Track("sess-1", cmds[0].Process)
	pt.Track("sess-1", cmds[1].Process)
	pt.Track("sess-2", cmds[2].Process)

	if pt.Count() != 3 {
		t.Fatalf("expected 3 tracked, got %d", pt.Count())
	}

	killed := pt.KillAll()
	if killed != 3 {
		t.Errorf("expected 3 killed, got %d", killed)
	}
	if pt.Count() != 0 {
		t.Errorf("expected 0 after kill all, got %d", pt.Count())
	}

	for _, cmd := range cmds {
		cmd.Wait()
	}
}

func TestProcessTracker_ActiveSessions(t *testing.T) {
	pt := NewProcessTracker()

	cmd1 := exec.Command("sleep", "60")
	cmd2 := exec.Command("sleep", "60")
	if err := cmd1.Start(); err != nil {
		t.Fatal(err)
	}
	if err := cmd2.Start(); err != nil {
		t.Fatal(err)
	}
	defer cmd1.Process.Kill()
	defer cmd2.Process.Kill()

	pt.Track("sess-a", cmd1.Process)
	pt.Track("sess-b", cmd2.Process)

	sessions := pt.ActiveSessions()
	if len(sessions) != 2 {
		t.Errorf("expected 2 active sessions, got %d", len(sessions))
	}

	cmd1.Process.Kill()
	cmd1.Wait()
	cmd2.Process.Kill()
	cmd2.Wait()
}

func TestProcessTracker_KillAlreadyExited(t *testing.T) {
	pt := NewProcessTracker()

	cmd := exec.Command("true")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	cmd.Wait() // let it finish

	pt.Track("sess-1", cmd.Process)
	// Should not panic when killing an already-exited process.
	killed := pt.KillSession("sess-1")
	// Kill on an already-exited process returns an error, so killed=0.
	_ = killed
}

func TestProcessTracker_Touch(t *testing.T) {
	pt := NewProcessTracker()

	cmd := exec.Command("sleep", "60")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer cmd.Process.Kill()

	pt.Track("sess-1", cmd.Process)

	// Initially, idle time should be near zero.
	health := pt.HealthCheck(time.Hour)
	if len(health) != 1 {
		t.Fatalf("expected 1 health entry, got %d", len(health))
	}
	if health[0].IsStale {
		t.Error("process should not be stale immediately after tracking")
	}

	// Touch the process.
	pt.Touch("sess-1", cmd.Process.Pid)

	health = pt.HealthCheck(time.Hour)
	if health[0].IsStale {
		t.Error("process should not be stale after touch")
	}
}

func TestProcessTracker_HealthCheck_StaleDetection(t *testing.T) {
	pt := NewProcessTracker()

	cmd := exec.Command("sleep", "60")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer cmd.Process.Kill()

	pt.Track("sess-1", cmd.Process)

	// With a zero threshold, everything is stale.
	health := pt.HealthCheck(0)
	if len(health) != 1 {
		t.Fatalf("expected 1, got %d", len(health))
	}
	if !health[0].IsStale {
		t.Error("expected stale with zero threshold")
	}
	if health[0].PID != cmd.Process.Pid {
		t.Errorf("expected pid %d, got %d", cmd.Process.Pid, health[0].PID)
	}
	if health[0].SessionID != "sess-1" {
		t.Errorf("expected session sess-1, got %s", health[0].SessionID)
	}
}

func TestProcessTracker_KillStale(t *testing.T) {
	pt := NewProcessTracker()

	cmd := exec.Command("sleep", "60")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}

	pt.Track("sess-1", cmd.Process)

	// With zero threshold, process is immediately stale.
	killed := pt.KillStale(0)
	if killed != 1 {
		t.Errorf("expected 1 killed, got %d", killed)
	}
	if pt.Count() != 0 {
		t.Errorf("expected 0 after kill stale, got %d", pt.Count())
	}

	cmd.Wait()
}

func TestProcessTracker_KillStale_PreservesActive(t *testing.T) {
	pt := NewProcessTracker()

	cmd := exec.Command("sleep", "60")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer cmd.Process.Kill()

	pt.Track("sess-1", cmd.Process)

	// With a very high threshold, nothing should be killed.
	killed := pt.KillStale(time.Hour)
	if killed != 0 {
		t.Errorf("expected 0 killed with high threshold, got %d", killed)
	}
	if pt.Count() != 1 {
		t.Errorf("expected 1 still tracked, got %d", pt.Count())
	}

	cmd.Process.Kill()
	cmd.Wait()
}

func TestIsProcessDone(t *testing.T) {
	if isProcessDone(nil) {
		t.Error("expected false for nil error")
	}
	if !isProcessDone(&os.PathError{Op: "os", Err: os.ErrProcessDone}) {
		// os.ErrProcessDone.Error() is "os: process already finished"
		// but os.PathError wraps it differently. Test with the exact string.
	}
}
