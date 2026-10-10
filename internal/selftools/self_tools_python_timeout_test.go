package selftools

import (
	"context"
	"strings"
	"testing"
	"time"
)

// CW-20261001-0026: python_run's CPU-time limit was set with soft == hard.
// Linux checks the hard limit first, so the child died of SIGKILL and never
// saw SIGXCPU, and the Go side, which counted only SIGXCPU or its own wall
// deadline as a timeout, reported "exit code -1" whenever the CPU limit
// fired before the deadline (both land near 1.5s on a loaded box, so the
// timeout test flaked).

func TestRunPythonSandbox_CPULimitLeavesRoomForSIGXCPU(t *testing.T) {
	skipIfNoPython3(t)
	result, err := RunPythonSandbox(context.Background(), "test-session",
		"import resource\nresult = list(resource.getrlimit(resource.RLIMIT_CPU))",
		nil, 3, 0, allowAllPermChecker{}, nilDispatcher{})
	if err != nil {
		t.Fatalf("RunPythonSandbox: %v", err)
	}
	limits, ok := result.Result.([]any)
	if !ok || len(limits) != 2 {
		t.Fatalf("RLIMIT_CPU = %#v (error %q), want [soft hard]", result.Result, result.Error)
	}
	if soft, hard := limits[0], limits[1]; soft != float64(3) || hard != float64(4) {
		t.Fatalf("RLIMIT_CPU = (%v, %v), want (3, 4): a hard limit equal to the soft one is a SIGKILL with no SIGXCPU", soft, hard)
	}
}

// TestRunPythonSandbox_CPULimitSignalIsATimeout drives the SIGXCPU path
// directly, without depending on which of the CPU limit and the wall
// deadline fires first.
func TestRunPythonSandbox_CPULimitSignalIsATimeout(t *testing.T) {
	skipIfNoPython3(t)
	start := time.Now()
	result, err := RunPythonSandbox(context.Background(), "test-session",
		"import os, signal\nos.kill(os.getpid(), signal.SIGXCPU)\nwhile True: pass",
		nil, 5, 0, allowAllPermChecker{}, nilDispatcher{})
	if err != nil {
		t.Fatalf("RunPythonSandbox: %v", err)
	}
	if !strings.Contains(result.Error, "timed out") {
		t.Fatalf("SIGXCPU reported as %q, want a timeout", result.Error)
	}
	if elapsed := time.Since(start); elapsed > 4*time.Second {
		t.Fatalf("took %s: the SIGXCPU was not acted on, the wall deadline ended the run", elapsed)
	}
}

// TestRunPythonSandbox_SignalDeathNamesTheSignal keeps a kill that is not a
// timeout from reading as "exit code -1".
func TestRunPythonSandbox_SignalDeathNamesTheSignal(t *testing.T) {
	skipIfNoPython3(t)
	result, err := RunPythonSandbox(context.Background(), "test-session",
		"import os, signal\nos.kill(os.getpid(), signal.SIGKILL)",
		nil, 5, 0, allowAllPermChecker{}, nilDispatcher{})
	if err != nil {
		t.Fatalf("RunPythonSandbox: %v", err)
	}
	if (!strings.Contains(result.Error, "killed by signal") && !strings.Contains(result.Error, "exit code 137 (possible child signal")) || strings.Contains(result.Error, "timed out") {
		t.Fatalf("SIGKILL reported as %q, want it named as a signal and not as a timeout", result.Error)
	}
}
