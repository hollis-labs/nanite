package mcp

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/hollis-labs/nanite/internal/sandbox"
	"github.com/hollis-labs/nanite/internal/truncate"
)

// CodeExecTransport provides the nanite_code_execute built-in tool for
// running code in the agent sandbox with full isolation.
type CodeExecTransport struct {
	// DefaultSessionID is used when the tool caller does not provide a session_id.
	// Falls back to "code-exec" if empty.
	DefaultSessionID string
}

// NewCodeExecTransport creates a CodeExecTransport with the given default session ID.
func NewCodeExecTransport(defaultSessionID string) *CodeExecTransport {
	if defaultSessionID == "" {
		defaultSessionID = "code-exec"
	}
	return &CodeExecTransport{DefaultSessionID: defaultSessionID}
}

// ListTools returns the code execution tool definition.
func (c *CodeExecTransport) ListTools(_ context.Context) ([]Tool, error) {
	return []Tool{
		{
			Name: "nanite_code_execute",
			Description: "Execute code in a sandboxed environment. Runs the provided code in an isolated sandbox directory " +
				"with restricted permissions. Use for running scripts, testing code snippets, or performing computations. " +
				"Output is truncated if too large. " +
				"Tip: for multi-step workflows, use dev_write to write a script to disk, nanite_code_execute to run it, " +
				"and dev_read to inspect output files.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"code": map[string]any{
						"type":        "string",
						"description": "The code to execute",
					},
					"language": map[string]any{
						"type":        "string",
						"enum":        []string{"shell", "python", "javascript"},
						"description": "The programming language. Determines the interpreter used.",
						"default":     "shell",
					},
					"timeout": map[string]any{
						"type":        "integer",
						"description": "Execution timeout in seconds. Default 30, max 300.",
						"default":     30,
					},
					"session_id": map[string]any{
						"type":        "string",
						"description": "Session ID for sandbox scoping. Uses default sandbox if omitted.",
					},
				},
				"required": []string{"code"},
			},
		},
	}, nil
}

// CallTool dispatches to the code execution handler.
func (c *CodeExecTransport) CallTool(_ context.Context, name string, args map[string]any) (*ToolResult, error) {
	if name != "nanite_code_execute" {
		return errorResult(fmt.Sprintf("unknown tool: %s", name)), nil
	}
	return c.callCodeExecute(args)
}

// interpreterInfo maps a language name to interpreter binary and file extension.
type interpreterInfo struct {
	binary    string
	extension string
}

var interpreters = map[string]interpreterInfo{
	"shell":      {binary: "sh", extension: ".sh"},
	"python":     {binary: "python3", extension: ".py"},
	"javascript": {binary: "node", extension: ".js"},
}

func (c *CodeExecTransport) callCodeExecute(args map[string]any) (*ToolResult, error) {
	code, _ := args["code"].(string)
	if code == "" {
		return errorResult("code is required"), nil
	}

	language, _ := args["language"].(string)
	if language == "" {
		language = "shell"
	}

	interp, ok := interpreters[language]
	if !ok {
		return errorResult(fmt.Sprintf("unsupported language %q (use shell, python, or javascript)", language)), nil
	}

	timeoutSec := intArg(args, "timeout", 30)
	if timeoutSec < 1 {
		timeoutSec = 1
	}
	if timeoutSec > 300 {
		timeoutSec = 300
	}

	sessionID, _ := args["session_id"].(string)
	if sessionID == "" {
		sessionID = c.DefaultSessionID
	}

	// Resolve sandbox directory to write the temp script file.
	sandboxDir, err := sandbox.Dir(sessionID)
	if err != nil {
		return errorResult(fmt.Sprintf("sandbox dir: %v", err)), nil
	}

	// Write code to a temp file in the sandbox directory.
	scriptName := fmt.Sprintf("exec_%s%s", uuid.New().String()[:8], interp.extension)
	scriptPath := filepath.Join(sandboxDir, scriptName)
	if err := os.WriteFile(scriptPath, []byte(code), 0644); err != nil {
		return errorResult(fmt.Sprintf("write script: %v", err)), nil
	}
	defer os.Remove(scriptPath)

	// Execute via sandbox.AgentExec.
	result, err := sandbox.AgentExec(sandbox.AgentExecOpts{
		SessionID: sessionID,
		Command:   interp.binary,
		Args:      []string{scriptPath},
		Timeout:   time.Duration(timeoutSec) * time.Second,
	})
	if err != nil {
		return errorResult(fmt.Sprintf("execution error: %v", err)), nil
	}

	// Format output.
	return formatExecResult(result), nil
}

// formatExecResult builds a tool result from sandbox execution output,
// truncating large output with a steering hint.
func formatExecResult(result *sandbox.ExecResult) *ToolResult {
	if result.TimedOut {
		var sb strings.Builder
		sb.WriteString("Execution timed out.\n")
		if result.Stdout != "" {
			sb.WriteString("\n--- stdout (partial) ---\n")
			sb.WriteString(result.Stdout)
		}
		if result.Stderr != "" {
			sb.WriteString("\n--- stderr (partial) ---\n")
			sb.WriteString(result.Stderr)
		}
		return errorResult(sb.String())
	}

	var sb strings.Builder

	if result.Stdout != "" {
		sb.WriteString(result.Stdout)
	}

	if result.Stderr != "" {
		if sb.Len() > 0 {
			sb.WriteString("\n")
		}
		sb.WriteString("--- stderr ---\n")
		sb.WriteString(result.Stderr)
	}

	if result.ExitCode != 0 {
		if sb.Len() > 0 {
			sb.WriteString("\n")
		}
		fmt.Fprintf(&sb, "Exit code: %d", result.ExitCode)
	}

	output := sb.String()
	if output == "" {
		output = "(no output)"
	}

	// Truncate large output using the truncation system.
	tr := truncate.Output(output, "nanite_code_execute")
	if tr.Truncated {
		return textResult(tr.Content +
			"\nOutput was truncated. To see specific parts, modify your code to print only the relevant output.")
	}

	return textResult(output)
}
