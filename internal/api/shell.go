package api

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/hollis-labs/nanite/internal/sandbox"
	"github.com/hollis-labs/nanite/internal/service"
	"github.com/hollis-labs/nanite/internal/shell"
)

// handleGetShellMode returns the current shell approval mode for a session.
func (a *API) handleGetShellMode(w http.ResponseWriter, r *http.Request) {
	mode := a.Services.Shell.Mode(r.Context(), r.PathValue("id"))
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
	if err := a.Services.Shell.SetMode(r.Context(), sessionID, req.Mode); err != nil {
		var ve *service.ShellValidationError
		if errors.As(err, &ve) {
			a.errorResp(w, http.StatusBadRequest, ve.Msg)
			return
		}
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}

	a.jsonResp(w, http.StatusOK, map[string]string{"mode": req.Mode})
}

// handleShellExec executes a shell command in the context of a session and
// records it, subject to the session's shell mode (ShellService.Exec).
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

	res, err := a.Services.Shell.Exec(r.Context(), sessionID, req.Command, req.Approved)
	if err != nil {
		var denied *service.ShellDeniedError
		if errors.As(err, &denied) {
			a.errorResp(w, http.StatusForbidden, err.Error())
			return
		}
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	if res.RequiresApproval {
		a.jsonResp(w, http.StatusOK, map[string]interface{}{
			"requires_approval": true,
			"command":           res.Command,
			"mode":              string(res.Mode),
		})
		return
	}

	a.jsonResp(w, http.StatusOK, map[string]interface{}{
		"message_id":       res.MessageID,
		"command":          res.Command,
		"output":           res.Output,
		"exit_code":        res.ExitCode,
		"timed_out":        res.TimedOut,
		"sandbox_isolated": res.SandboxIsolated,
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

	mode := a.Services.Shell.Mode(r.Context(), sessionID)
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
	workDir := a.Services.Shell.WorkDir(r.Context(), sessionID)

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
