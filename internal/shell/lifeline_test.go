package shell

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestMain lets the test binary act as a parent that starts a command and
// then waits to be killed.
func TestMain(m *testing.M) {
	if os.Getenv("NANITE_SHELL_LIFELINE_PARENT") == "1" {
		process, err := Start(context.Background(), Command{Script: "sleep 300", Timeout: time.Hour})
		if err != nil {
			fmt.Println("error", err)
			os.Exit(1)
		}
		fmt.Println(process.PID())
		select {}
	}
	os.Exit(m.Run())
}

func TestCommandDiesWhenParentIsKilled(t *testing.T) {
	parent := exec.Command(os.Args[0]) // #nosec G204 G702 -- Re-runs this test binary.
	parent.Env = append(os.Environ(), "NANITE_SHELL_LIFELINE_PARENT=1")
	stdout, err := parent.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := parent.Start(); err != nil {
		t.Fatal(err)
	}
	line, err := bufio.NewReader(stdout).ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	pgid, err := strconv.Atoi(strings.TrimSpace(line))
	if err != nil {
		t.Fatalf("parent printed %q", line)
	}
	t.Cleanup(func() { _ = syscall.Kill(-pgid, syscall.SIGKILL) })
	if err := syscall.Kill(-pgid, 0); err != nil {
		t.Fatalf("command group not running before the kill: %v", err)
	}
	if err := parent.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = parent.Wait()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if errors.Is(syscall.Kill(-pgid, 0), syscall.ESRCH) {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("command group survived its killed parent")
}

func TestLifelineLeavesNoWatcherAfterNormalExit(t *testing.T) {
	process, err := Start(t.Context(), Command{Script: "true", Timeout: 5 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	pid := process.PID()
	if _, err := process.Wait(); err != nil {
		t.Fatal(err)
	}
	out, _ := exec.Command("/usr/bin/pgrep", "-f", "nanite-lifeline "+strconv.Itoa(pid)+"$").Output() // #nosec G204 -- Numeric PID.
	if len(strings.Fields(string(out))) != 0 {
		t.Fatalf("watcher still running: %s", out)
	}
}
