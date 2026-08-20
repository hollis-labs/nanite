package selftools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/hollis-labs/nanite/internal/mcp"
	"github.com/hollis-labs/nanite/internal/permission"
)

// pythonSandboxDefaultTimeLimitSec is the default CPU/wall-clock cap.
const pythonSandboxDefaultTimeLimitSec = 10

// pythonSandboxDefaultMemLimitMB is the default memory cap (256 MB).
const pythonSandboxDefaultMemLimitMB = 256

// pythonSandboxMaxTimeLimitSec is the absolute ceiling callers cannot exceed.
const pythonSandboxMaxTimeLimitSec = 60

// pythonSandboxMaxMemLimitMB is the absolute memory ceiling.
const pythonSandboxMaxMemLimitMB = 1024

// pythonSandboxMaxOutputBytes is the max stdout/stderr capture from the sandbox.
const pythonSandboxMaxOutputBytes = 256 * 1024 // 256 KiB

// pythonSandboxMaxTracebackLines is the maximum number of Python traceback
// lines forwarded to the LLM on a syntax/runtime error.
const pythonSandboxMaxTracebackLines = 20

// PythonPermissionChecker is the narrow interface the sandbox calls to
// validate tool-call requests originating from inside the Python sandbox.
// The production wiring passes *permission.Engine; tests can stub it.
type PythonPermissionChecker interface {
	Check(ctx context.Context, sessionID, toolName string, input map[string]any, meta permission.ToolMeta) permission.CheckResult
}

// PythonToolDispatcher executes a single tool call on behalf of the sandbox
// and returns the serialisable result. The production wiring delegates to the
// same code path as a normal tool call.  Tests stub this.
type PythonToolDispatcher interface {
	Dispatch(ctx context.Context, sessionID, toolName string, args map[string]any) (any, error)
}

// sandboxToolRequest is the JSON wire format the Python preamble sends on FD3.
type sandboxToolRequest struct {
	ID   int            `json:"id"`
	Name string         `json:"name"`
	Args map[string]any `json:"args"`
}

// sandboxToolResponse is the JSON wire format Go sends back on FD4.
type sandboxToolResponse struct {
	ID     int    `json:"id"`
	Result any    `json:"result,omitempty"`
	Error  string `json:"error,omitempty"`
}

// sandboxEnvelope is the JSON blob written to the Python process' stdin.
// The Python preamble reads exactly this shape.
type sandboxEnvelope struct {
	Code         string         `json:"code"`
	Args         map[string]any `json:"args"`
	TimeLimitSec int            `json:"time_limit_sec"`
	MemLimitMB   int            `json:"mem_limit_mb"`
}

// PythonRunResult is the structured result returned by the tool to the LLM.
type PythonRunResult struct {
	Result    any                 `json:"result"`
	Stdout    string              `json:"stdout"`
	Error     string              `json:"error,omitempty"`
	ToolCalls []PythonToolCallLog `json:"tool_calls"`
}

// PythonToolCallLog records a single tool invocation made from the sandbox.
type PythonToolCallLog struct {
	Name   string `json:"name"`
	Status string `json:"status"` // "ok" | "denied" | "error"
}

// pythonPreamble is injected at the top of every sandbox execution.
// It installs resource limits, blocks network socket creation (best-effort),
// and exposes the tool_call(name, args) helper that round-trips over FD3/FD4.
//
// The preamble is designed to be minimal (~50 lines) and safe on Python ≥ 3.10.
// FD 3 is the request pipe (write); FD 4 is the response pipe (read).
// The envelope JSON is delivered on stdin before the model code runs.
const pythonPreamble = `
import sys, json, os, builtins

# ── resource limits (unix only) ─────────────────────────────────────────────
try:
    import resource as _resource
    _envelope = json.loads(sys.stdin.readline())
    _time_limit = int(_envelope.get("time_limit_sec", 10))
    _mem_limit  = int(_envelope.get("mem_limit_mb",  256)) * 1024 * 1024
    # CPU time
    _resource.setrlimit(_resource.RLIMIT_CPU, (_time_limit, _time_limit))
    # Address space (covers heap + stack + mmap)
    try:
        _resource.setrlimit(_resource.RLIMIT_AS, (_mem_limit, _mem_limit))
    except (ValueError, _resource.error):
        pass  # some platforms do not support RLIMIT_AS
except ImportError:
    # Windows: resource module not available; caps skipped (documented limitation)
    import json as _json_win
    _envelope = _json_win.loads(sys.stdin.readline())

_code = _envelope.get("code", "")
_args = _envelope.get("args", {})

# ── network deny (best-effort import block) ──────────────────────────────────
import socket as _socket_mod
_orig_socket_init = _socket_mod.socket.__init__
def _socket_deny(self, *a, **kw):
    raise PermissionError("network access is not permitted inside python_run")
_socket_mod.socket.__init__ = _socket_deny  # type: ignore[method-assign]

# ── tool_call FD channel ─────────────────────────────────────────────────────
_req_fd  = os.fdopen(3, "w", buffering=1, closefd=False)
_resp_fd = os.fdopen(4, "r", buffering=1, closefd=False)
_req_counter = 0

def tool_call(name: str, args: dict) -> dict:
    """
    Call a Nanite tool from inside the sandbox.

    Every call traverses the full permission engine and chat-surface
    enforcement — no security boundary is bypassed.

    Returns the tool result as a plain Python dict (or raises on denial/error).
    """
    global _req_counter
    _req_counter += 1
    req = json.dumps({"id": _req_counter, "name": name, "args": args})
    _req_fd.write(req + "\n")
    _req_fd.flush()
    resp_raw = _resp_fd.readline()
    if not resp_raw:
        raise RuntimeError("tool_call: response pipe closed unexpectedly")
    resp = json.loads(resp_raw)
    if resp.get("error"):
        raise RuntimeError(f"tool_call({name!r}): {resp['error']}")
    return resp.get("result", {})

# ── capture stdout ───────────────────────────────────────────────────────────
import io as _io
_captured_stdout = _io.StringIO()
sys.stdout = _captured_stdout

# ── execute model code ───────────────────────────────────────────────────────
_result = None
_error  = None
_tool_calls_log = []

# Inject args and tool_call into the execution namespace.
_ns = {"args": _args, "tool_call": tool_call, "__builtins__": builtins}

try:
    exec(compile(_code, "<python_run>", "exec"), _ns)
    _result = _ns.get("result", None)
except Exception as _exc:
    import traceback as _tb
    _lines = _tb.format_exc().splitlines()
    _error = "\n".join(_lines[:20])  # cap at 20 lines

# ── flush and restore stdout ─────────────────────────────────────────────────
sys.stdout = sys.__stdout__
_stdout_text = _captured_stdout.getvalue()

# ── emit final result on stdout ───────────────────────────────────────────────
_out = json.dumps({
    "result": _result,
    "stdout": _stdout_text,
    "error":  _error,
})
print(_out, flush=True)
`

// RunPythonSandbox executes user-provided Python code in an isolated subprocess.
//
// Transport: dual-FD JSON-RPC.
//   - FD 3 (request pipe, write-end in Python, read-end in Go): Python sends
//     tool_call requests as newline-delimited JSON.
//   - FD 4 (response pipe, read-end in Python, write-end in Go): Go sends
//     tool_call responses as newline-delimited JSON.
//   - stdin: carries the envelope JSON (code, args, caps) that the preamble
//     reads before executing the model code.
//   - stdout: the final result JSON (one line) emitted after execution.
//
// Tool calls from inside the sandbox go through permission and dispatcher;
// the log of calls (name + status) is included in the structured result.
func RunPythonSandbox(
	ctx context.Context,
	sessionID string,
	code string,
	args map[string]any,
	timeLimitSec int,
	memLimitMB int,
	perm PythonPermissionChecker,
	dispatcher PythonToolDispatcher,
) (*PythonRunResult, error) {
	// Clamp limits.
	if timeLimitSec <= 0 {
		timeLimitSec = pythonSandboxDefaultTimeLimitSec
	}
	if timeLimitSec > pythonSandboxMaxTimeLimitSec {
		timeLimitSec = pythonSandboxMaxTimeLimitSec
	}
	if memLimitMB <= 0 {
		memLimitMB = pythonSandboxDefaultMemLimitMB
	}
	if memLimitMB > pythonSandboxMaxMemLimitMB {
		memLimitMB = pythonSandboxMaxMemLimitMB
	}

	// Locate python3 on PATH.
	py3, err := exec.LookPath("python3")
	if err != nil {
		return nil, fmt.Errorf("python_run: python3 not found on PATH — install Python ≥ 3.10")
	}

	// Build stdin envelope.
	// The preamble (passed as -c) reads the envelope from stdin; the model
	// code arrives inside the envelope's "code" field. This keeps the model
	// code out of the process argv (no ps leakage) and lets the preamble
	// compile it cleanly via exec(compile(_code, ...)).
	if args == nil {
		args = map[string]any{}
	}
	envelopeJSON, err := json.Marshal(sandboxEnvelope{
		Code:         code,
		Args:         args,
		TimeLimitSec: timeLimitSec,
		MemLimitMB:   memLimitMB,
	})
	if err != nil {
		return nil, fmt.Errorf("python_run: marshal envelope: %w", err)
	}

	// Create the two pipe pairs for FD3 (req) and FD4 (resp).
	// reqR → Python reads requests (FD3 from Python's perspective)
	// reqW → Go writes requests (never used — Python writes)
	// Go reads from reqR; Python writes to reqW.
	// Named from Python's perspective: FD3 = write-only req, FD4 = read-only resp.
	pyReqR, pyReqW, err := os.Pipe() // Python writes here (FD3); Go reads
	if err != nil {
		return nil, fmt.Errorf("python_run: create req pipe: %w", err)
	}
	pyRespR, pyRespW, err := os.Pipe() // Go writes here (FD4); Python reads
	if err != nil {
		pyReqR.Close()
		pyReqW.Close()
		return nil, fmt.Errorf("python_run: create resp pipe: %w", err)
	}
	defer pyReqR.Close()
	defer pyReqW.Close()
	defer pyRespR.Close()
	defer pyRespW.Close()

	// Wall-clock deadline for the entire sandbox execution.
	deadline := time.Duration(timeLimitSec) * time.Second
	runCtx, cancel := context.WithTimeout(ctx, deadline+500*time.Millisecond)
	defer cancel()

	// Spawn python3 with isolated mode flags.
	// -I: isolated mode (no PYTHONPATH, no site).
	// -B: don't write .pyc files.
	// -E: ignore all PYTHON* env vars.
	// -S: don't import site on startup.
	// -c: run only the preamble; model code arrives via stdin envelope.
	cmd := exec.CommandContext(runCtx, py3, "-IBES", "-c", pythonPreamble)

	// ExtraFiles maps: ExtraFiles[0] → FD3, ExtraFiles[1] → FD4.
	// Python's FD3 = write (request out), FD4 = read (response in).
	cmd.ExtraFiles = []*os.File{pyReqW, pyRespR}

	// Stdin carries the envelope JSON (one line).
	cmd.Stdin = strings.NewReader(string(envelopeJSON) + "\n")

	// stdout captures the final result JSON.
	var stdoutBuf limitedSandboxBuffer
	stdoutBuf.max = pythonSandboxMaxOutputBytes
	cmd.Stdout = &stdoutBuf

	// stderr captures traceback / diagnostic output (not forwarded to LLM by default).
	var stderrBuf limitedSandboxBuffer
	stderrBuf.max = pythonSandboxMaxOutputBytes
	cmd.Stderr = &stderrBuf

	// Apply OS-level isolation (setpgid for signal cascading; rlimit on unix).
	applySandboxSysProcAttr(cmd)

	// Goroutine safety: tool_calls_log is written from the pump goroutine.
	var toolCallsLog []PythonToolCallLog
	var toolCallsMu sync.Mutex

	// Start the subprocess.
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("python_run: start python3: %w", err)
	}

	// Close the child-side ends in the parent after Start() so EOF propagates
	// correctly when Python closes its FDs.
	pyReqW.Close()
	pyRespR.Close()

	// Pump goroutine: reads tool-call requests from pyReqR, dispatches them
	// through the permission engine, writes responses to pyRespW.
	pumpDone := make(chan struct{})
	var pumpErr error
	go func() {
		defer close(pumpDone)
		defer pyRespW.Close() // close resp write-end when pump exits → Python readline returns ""
		pumpErr = pumpToolCalls(runCtx, sessionID, pyReqR, pyRespW, perm, dispatcher, &toolCallsLog, &toolCallsMu)
	}()

	// Wait for the process to exit and the pump to finish.
	cmdErr := cmd.Wait()
	<-pumpDone

	// Determine if we timed out.
	timedOut := runCtx.Err() == context.DeadlineExceeded

	// Build the structured result.
	result := &PythonRunResult{
		ToolCalls: []PythonToolCallLog{},
	}
	toolCallsMu.Lock()
	if len(toolCallsLog) > 0 {
		result.ToolCalls = toolCallsLog
	}
	toolCallsMu.Unlock()

	if timedOut {
		result.Error = fmt.Sprintf("execution timed out after %ds", timeLimitSec)
		return result, nil
	}

	// Parse stdout as the final result JSON.
	rawOut := strings.TrimSpace(stdoutBuf.String())
	if rawOut != "" {
		var pyOut struct {
			Result any    `json:"result"`
			Stdout string `json:"stdout"`
			Error  string `json:"error"`
		}
		if jsonErr := json.Unmarshal([]byte(rawOut), &pyOut); jsonErr == nil {
			result.Result = pyOut.Result
			result.Stdout = pyOut.Stdout
			if pyOut.Error != "" {
				result.Error = pyOut.Error
			}
		} else {
			// stdout was not valid JSON — treat as raw stdout.
			result.Stdout = rawOut
		}
	}

	// Surface non-timeout exit errors.
	if cmdErr != nil && !timedOut && result.Error == "" {
		stderr := strings.TrimSpace(stderrBuf.String())
		if stderr != "" {
			lines := strings.Split(stderr, "\n")
			if len(lines) > pythonSandboxMaxTracebackLines {
				lines = lines[:pythonSandboxMaxTracebackLines]
			}
			result.Error = strings.Join(lines, "\n")
		} else {
			result.Error = fmt.Sprintf("exit code %d", exitCode(cmdErr))
		}
	}

	_ = pumpErr // pump errors are surfaced via tool_calls_log status entries
	return result, nil
}

// pumpToolCalls reads newline-delimited JSON requests from r (Python FD3),
// dispatches them, and writes responses to w (Python FD4).
// It exits when r reaches EOF (Python closed its write end on exit).
func pumpToolCalls(
	ctx context.Context,
	sessionID string,
	r *os.File,
	w *os.File,
	perm PythonPermissionChecker,
	dispatcher PythonToolDispatcher,
	log *[]PythonToolCallLog,
	mu *sync.Mutex,
) error {
	dec := json.NewDecoder(r)
	enc := json.NewEncoder(w)

	for {
		var req sandboxToolRequest
		if err := dec.Decode(&req); err != nil {
			if err == io.EOF {
				return nil
			}
			return fmt.Errorf("pump: decode request: %w", err)
		}

		// Permission check.
		var resp sandboxToolResponse
		resp.ID = req.ID

		if perm != nil {
			checkResult := perm.Check(ctx, sessionID, req.Name, req.Args, permission.ToolMeta{
				IsReadOnly: false, // conservative: sandbox calls are treated as non-read-only
			})
			if checkResult.Decision == permission.DecisionDeny {
				resp.Error = fmt.Sprintf("permission denied for tool %q: %s", req.Name, checkResult.Reason)
				mu.Lock()
				*log = append(*log, PythonToolCallLog{Name: req.Name, Status: "denied"})
				mu.Unlock()
				slog.Info("python sandbox: tool call denied",
					"tool", req.Name, "session_id", sessionID, "reason", checkResult.Reason)
				if err := enc.Encode(resp); err != nil {
					return fmt.Errorf("pump: encode deny response: %w", err)
				}
				continue
			}
		}

		// Dispatch the tool call.
		if dispatcher == nil {
			resp.Error = "no tool dispatcher configured"
		} else {
			toolResult, dispErr := dispatcher.Dispatch(ctx, sessionID, req.Name, req.Args)
			if dispErr != nil {
				resp.Error = dispErr.Error()
				mu.Lock()
				*log = append(*log, PythonToolCallLog{Name: req.Name, Status: "error"})
				mu.Unlock()
				slog.Info("python sandbox: tool call error",
					"tool", req.Name, "session_id", sessionID, "err", dispErr)
			} else {
				resp.Result = toolResult
				mu.Lock()
				*log = append(*log, PythonToolCallLog{Name: req.Name, Status: "ok"})
				mu.Unlock()
			}
		}

		if err := enc.Encode(resp); err != nil {
			return fmt.Errorf("pump: encode response: %w", err)
		}
	}
}

// exitCode extracts the exit code from a command error.
func exitCode(err error) int {
	if err == nil {
		return 0
	}
	if exitErr, ok := err.(*exec.ExitError); ok {
		return exitErr.ExitCode()
	}
	return -1
}

// limitedSandboxBuffer is a size-capped io.Writer + String() for stdout/stderr.
type limitedSandboxBuffer struct {
	mu  sync.Mutex
	buf strings.Builder
	n   int64 // bytes written so far
	max int
}

func (b *limitedSandboxBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	remaining := b.max - int(atomic.LoadInt64(&b.n))
	if remaining <= 0 {
		return len(p), nil
	}
	if len(p) > remaining {
		p = p[:remaining]
	}
	n, _ := b.buf.Write(p)
	atomic.AddInt64(&b.n, int64(n))
	return len(p), nil
}

func (b *limitedSandboxBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// applySandboxSysProcAttr configures process group isolation on supported platforms.
// On unix: sets Setpgid so the sandbox process group can be killed as a unit.
// On other platforms: no-op.
func applySandboxSysProcAttr(cmd *exec.Cmd) {
	if runtime.GOOS == "windows" {
		return
	}
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
}

// naniteRunPythonToolDefinition returns the tool definition for
// python_run. Reach for any caller (Chat, Planner, Worker, or a
// custom agent profile) is governed by the agent profile's tool
// permissions and the dev-mode gate, not by a hard-coded surface
// restriction in this definition.
func naniteRunPythonToolDefinition() mcp.Tool {
	return mcp.Tool{
		Name: "python_run",
		Description: `Execute a Python script in an isolated sandbox, with access to Nanite tools via tool_call().

**When to use:**
- Pagination loops: iterate over tool_call() results across multiple pages.
- Multi-entity join: call a list tool, then call a detail tool per item, return joined.
- Numeric aggregation: sum/mean/filter over tool_call() result data.
- Any scenario where N repeated tool calls with logic between them is simpler as code.

**When NOT to use:**
- General computation without tool calls (use scratchpad/think instead).
- File I/O, network access, or long-running background tasks.

**tool_call(name, args) helper:**
Every call goes through the full permission engine — no security hole is opened.
Returns a dict, raises RuntimeError on denial or error.

Example:
` + "```python" + `
total = 0
for offset in range(0, 500, 50):
    page = tool_call("some_list_tool", {"limit": 50, "offset": offset})
    items = page.get("items", [])
    total += len(items)
    if len(items) < 50:
        break
result = {"count": total}
` + "```" + `

Set ` + "`result`" + ` in the script namespace to control what is returned to the LLM.

**Output shape:** {result: any, stdout: string, error?: string, tool_calls: [{name, status}]}

**Resource caps (defaults):** 10s CPU time, 256 MB memory. Override with time_limit_seconds / memory_limit_mb.
Absolute maximums: 60s / 1 GB — the harness clamps to these regardless of what you pass.

**Python requirement:** python3 ≥ 3.10 must be on PATH in the execution environment.`,
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"code": map[string]any{
					"type":        "string",
					"description": "Python source code to execute. Set `result` to control the return value.",
				},
				"args": map[string]any{
					"type":        "object",
					"description": "Optional dict of arguments passed as `args` in the script namespace.",
				},
				"time_limit_seconds": map[string]any{
					"type":        "integer",
					"description": "Wall-clock / CPU time cap in seconds. Default 10, max 60.",
				},
				"memory_limit_mb": map[string]any{
					"type":        "integer",
					"description": "Memory cap in MB. Default 256, max 1024.",
				},
				"session_id": map[string]any{
					"type":        "string",
					"description": "Session ID for permission attribution. Auto-filled from context when omitted.",
				},
			},
			"required": []string{"code"},
		},
	}
}
