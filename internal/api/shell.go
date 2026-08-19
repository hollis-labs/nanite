package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/hollis-labs/nanite/internal/sandbox"
	"github.com/hollis-labs/nanite/internal/shell"
	"github.com/hollis-labs/nanite/internal/store"
)

// handleGetShellMode returns the current shell approval mode for a session.
func (a *API) handleGetShellMode(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("id")
	mode := a.sessionShellMode(sessionID)
	a.jsonResp(w, http.StatusOK, map[string]string{"mode": string(mode)})
}

// handleSetShellMode sets the shell approval mode for a session.
func (a *API) handleSetShellMode(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("id")

	var req SetShellModeRequest
	if err := a.decode(r, &req); err != nil {
		a.errorResp(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if !shell.ValidMode(req.Mode) {
		a.errorResp(w, http.StatusBadRequest, "mode must be ask, session, or yolo")
		return
	}

	if err := a.setSessionMetadataField(sessionID, "shell_mode", req.Mode); err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}

	a.jsonResp(w, http.StatusOK, map[string]string{"mode": req.Mode})
}

// handleShellExec executes a shell command in the context of a session and
// persists the output as a user message visible to the LLM.
func (a *API) handleShellExec(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("id")

	var req ShellExecRequest
	if err := a.decode(r, &req); err != nil {
		a.errorResp(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if req.Command == "" {
		a.errorResp(w, http.StatusBadRequest, "command is required")
		return
	}

	// Enforce approval modes.
	mode := a.sessionShellMode(sessionID)
	if mode == shell.ModeAsk && !req.Approved {
		a.jsonResp(w, http.StatusOK, map[string]interface{}{
			"requires_approval": true,
			"command":           req.Command,
			"mode":              string(mode),
		})
		return
	}

	// Resolve working directory from session/project.
	workDir := a.resolveShellWorkDir(sessionID)

	// Execute via sandbox.UserExec (handles denylist + env filtering).
	// YOLO mode skips the OS sandbox but keeps denylist + env filter.
	var output string
	var exitCode int
	var timedOut bool

	shPath := resolveShell()

	result, err := sandbox.UserExec(sandbox.UserExecOpts{
		Command:   shPath,
		Args:      []string{"-c", req.Command},
		Dir:       workDir,
		Sandboxed: mode != shell.ModeYOLO, // OS sandbox for ask+session, not yolo
	})
	if err != nil {
		if strings.Contains(err.Error(), "denied") {
			a.errorResp(w, http.StatusForbidden, err.Error())
		} else {
			a.errorResp(w, http.StatusInternalServerError, err.Error())
		}
		return
	}
	output = result.Stdout
	if result.Stderr != "" {
		output += result.Stderr
	}
	exitCode = result.ExitCode
	timedOut = result.TimedOut

	// Build the message content the LLM will see.
	content := fmt.Sprintf("$ %s\n%s", req.Command, output)

	// Build message metadata.
	meta := map[string]interface{}{
		"type": "shell_exec",
		"shell_exec": map[string]interface{}{
			"command":   req.Command,
			"exit_code": exitCode,
			"timed_out": timedOut,
		},
	}
	metaJSON, _ := json.Marshal(meta)

	// Persist as a user message so the LLM sees the command + output.
	msg := &store.Message{
		ID:        uuid.New().String(),
		SessionID: sessionID,
		Role:      "user",
		Content:   content,
		Metadata:  string(metaJSON),
	}
	if err := a.Services.Store.CreateMessage(msg); err != nil {
		a.errorResp(w, http.StatusInternalServerError, "persist shell output: "+err.Error())
		return
	}

	a.jsonResp(w, http.StatusOK, map[string]interface{}{
		"message_id": msg.ID,
		"command":    req.Command,
		"output":     output,
		"exit_code":  exitCode,
		"timed_out":  timedOut,
	})
}

// handleShellCheck validates a command against the denylist without executing it.
// Used by the frontend for real-time feedback while typing.
func (a *API) handleShellCheck(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("id")
	command := r.URL.Query().Get("command")
	if command == "" {
		a.errorResp(w, http.StatusBadRequest, "command query param is required")
		return
	}

	mode := a.sessionShellMode(sessionID)
	allowed := true
	reason := ""

	if blocked, r := sandbox.CheckDenylist(command); blocked {
		allowed = false
		reason = r
	}

	a.jsonResp(w, http.StatusOK, map[string]interface{}{
		"allowed": allowed,
		"reason":  reason,
		"mode":    string(mode),
	})
}

// handleShellInfo returns working directory and git info for the shell info drawer.
func (a *API) handleShellInfo(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("id")
	workDir := a.resolveShellWorkDir(sessionID)

	info := map[string]interface{}{
		"work_dir": workDir,
	}

	// Try to get git branch and status.
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()

	branchResult := shell.Exec(ctx, "git rev-parse --abbrev-ref HEAD", shell.ExecOpts{WorkDir: workDir})
	if branchResult.ExitCode == 0 {
		branch := branchResult.Output
		if len(branch) > 0 && branch[len(branch)-1] == '\n' {
			branch = branch[:len(branch)-1]
		}
		info["git_branch"] = branch

		statusResult := shell.Exec(ctx, "git status --porcelain", shell.ExecOpts{WorkDir: workDir})
		if statusResult.ExitCode == 0 {
			if statusResult.Output == "" {
				info["git_status"] = "clean"
			} else {
				info["git_status"] = "dirty"
			}
		}
	}

	a.jsonResp(w, http.StatusOK, info)
}

// --- helpers ---

// sessionShellMode reads the shell_mode from session metadata, defaulting to "ask".
func (a *API) sessionShellMode(sessionID string) shell.Mode {
	sess, err := a.Services.Store.GetSession(sessionID)
	if err != nil {
		return shell.ModeAsk
	}
	var meta map[string]interface{}
	if err := json.Unmarshal([]byte(sess.Metadata), &meta); err != nil {
		return shell.ModeAsk
	}
	if m, ok := meta["shell_mode"].(string); ok && shell.ValidMode(m) {
		return shell.Mode(m)
	}
	return shell.ModeAsk
}

// setSessionMetadataField merges a single key into the session's metadata JSON.
func (a *API) setSessionMetadataField(sessionID, key string, value interface{}) error {
	sess, err := a.Services.Store.GetSession(sessionID)
	if err != nil {
		return fmt.Errorf("get session: %w", err)
	}

	meta := make(map[string]interface{})
	if sess.Metadata != "" {
		json.Unmarshal([]byte(sess.Metadata), &meta)
	}
	meta[key] = value

	out, err := json.Marshal(meta)
	if err != nil {
		return fmt.Errorf("marshal metadata: %w", err)
	}
	return a.Services.Store.UpdateSessionMetadata(sessionID, string(out))
}

// resolveShellWorkDir determines the working directory for shell commands.
// Priority: project directory > $HOME. Phase 0 item 20 (retire workspaces):
// this used to also fall back to the session's workspace settings
// (workspaces.settings' project_dir) — the in-app `workspaces` table is
// retired in full.
func (a *API) resolveShellWorkDir(sessionID string) string {
	sess, err := a.Services.Store.GetSession(sessionID)
	if err != nil {
		return fallbackHomeDir()
	}

	// Try project directory from session metadata.
	var meta map[string]interface{}
	if err := json.Unmarshal([]byte(sess.Metadata), &meta); err == nil {
		if dir, ok := meta["project_dir"].(string); ok && dir != "" {
			return dir
		}
	}

	return fallbackHomeDir()
}

func fallbackHomeDir() string {
	if home, err := os.UserHomeDir(); err == nil {
		return home
	}
	return "/"
}

// resolveShell returns the path to a POSIX shell for command execution.
// Priority: $SHELL env var > exec.LookPath("sh") > /bin/sh.
func resolveShell() string {
	if s := os.Getenv("SHELL"); s != "" {
		return s
	}
	if p, err := exec.LookPath("sh"); err == nil {
		return p
	}
	return "/bin/sh"
}
