package sandbox

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestPythonCommandRejectsUnsafeSetup(t *testing.T) {
	cleanup, err := PreparePythonCommand(nil, t.TempDir())
	if cleanup != nil || !errors.Is(err, ErrPythonIsolationUnavailable) {
		t.Fatalf("invalid setup: %v", err)
	}
	if runtime.GOOS != "linux" {
		return
	} // Only Linux selects bwrap through PATH.
	t.Setenv("PATH", t.TempDir())
	t.Setenv(allowUnsandboxedExecEnvVar, "1")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cmd := exec.CommandContext(ctx, "/usr/bin/true")
	cleanup, err = PreparePythonCommand(cmd, t.TempDir())
	if cleanup != nil || !errors.Is(err, ErrPythonIsolationUnavailable) {
		t.Fatalf("cleanup=%v err=%v", cleanup != nil, err)
	}
}

func TestPythonCommandIsolation(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = listener.Close() }()
	hostConn, dialErr := net.DialTimeout("tcp", listener.Addr().String(), time.Second)
	if dialErr != nil {
		t.Fatalf("host endpoint is not reachable for isolation control: %v", dialErr)
	}
	_ = hostConn.Close()
	t.Setenv("PYTHON_RUN_TEST_SECRET", "must-not-inherit")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	dir := t.TempDir()
	// _socket bypasses the old monkeypatch; the subprocess independently
	// proves that the OS boundary also applies to descendants.
	const code = `import os, json, _socket, subprocess, sys
port = sys.stdin.read().strip()
def reachable():
    s = _socket.socket()
    s.settimeout(.1)
    try:
        s.connect(("127.0.0.1", int(port)))
        return True
    except OSError:
        return False
    finally:
        s.close()
child = subprocess.run([sys.executable, "-c", "import socket,sys; s=socket.socket(); s.settimeout(.1); s.connect(('127.0.0.1',int(sys.argv[1])))", port], capture_output=True)
print(json.dumps({"secret": os.getenv("PYTHON_RUN_TEST_SECRET"), "cwd": os.getcwd(), "home": os.getenv("HOME"), "network": reachable(), "child_network": child.returncode == 0}))`
	cmd := exec.CommandContext(ctx, "python3", "-IBES", "-c", code)
	cmd.Stdin = strings.NewReader(strconv.Itoa(listener.Addr().(*net.TCPAddr).Port))
	cleanup, err := PreparePythonCommand(cmd, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	body, err := cmd.Output()
	if err != nil {
		t.Fatalf("strict runtime: %v", err)
	}
	var result struct {
		Secret       *string
		Cwd          string
		Home         string
		Network      bool
		ChildNetwork bool `json:"child_network"`
	}
	if decodeErr := json.Unmarshal(body, &result); decodeErr != nil {
		t.Fatal(decodeErr)
	}
	if result.Secret != nil || result.Network || result.ChildNetwork {
		t.Fatalf("isolation failed: %+v", result)
	}
	canonicalDir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Clean(result.Cwd) != filepath.Clean(canonicalDir) || result.Home != dir {
		t.Fatalf("scratch isolation failed: %+v", result)
	}
}
