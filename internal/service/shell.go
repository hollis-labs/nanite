package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/google/uuid"
	"github.com/hollis-labs/nanite/internal/sandbox"
	"github.com/hollis-labs/nanite/internal/shell"
	"github.com/hollis-labs/nanite/internal/store"
)

// ShellStore is the store surface ShellService uses: the session whose
// metadata holds the shell mode and working directory, and the message a
// command's output is recorded as.
type ShellStore interface {
	GetSession(ctx context.Context, id string) (*store.Session, error)
	UpdateSessionMetadata(ctx context.Context, id, metadataJSON string) error
	CreateMessage(ctx context.Context, msg *store.Message) error
}

// ShellService owns the policy for running a user's shell command in a
// session: the approval mode ("ask", "session" or "yolo", kept in session
// metadata as shell_mode), the working directory (metadata project_dir, else
// the home directory), whether the OS sandbox applies, and recording the
// command and its output as a message.
//
// Reads run under the caller's ctx and fail safe: when the session cannot
// be read — including once the caller's request is canceled — the mode is
// "ask", which requires approval, and the working directory is home.
type ShellService struct {
	store ShellStore
}

func NewShellService(st ShellStore) *ShellService {
	return &ShellService{store: st}
}

// ShellValidationError reports a request the shell rules reject. Its
// message is meant for the caller.
type ShellValidationError struct {
	Msg string
}

func (e *ShellValidationError) Error() string { return e.Msg }

// ShellDeniedError reports a command the sandbox refused to run.
type ShellDeniedError struct {
	Err error
}

func (e *ShellDeniedError) Error() string { return e.Err.Error() }
func (e *ShellDeniedError) Unwrap() error { return e.Err }

// Mode returns the session's shell approval mode. A session that cannot be
// read, metadata that does not parse, and a missing or unknown shell_mode
// all mean "ask".
func (s *ShellService) Mode(ctx context.Context, sessionID string) shell.Mode {
	sess, err := s.store.GetSession(ctx, sessionID)
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

// SetMode stores the session's shell approval mode, merging it into the
// session metadata. An unknown mode is a *ShellValidationError. Metadata
// that does not parse is an error and is left as it was. Read, merge and
// write are separate steps, not one transaction.
func (s *ShellService) SetMode(ctx context.Context, sessionID, mode string) error {
	if !shell.ValidMode(mode) {
		return &ShellValidationError{Msg: "mode must be ask, session, or yolo"}
	}
	sess, err := s.store.GetSession(ctx, sessionID)
	if err != nil {
		return fmt.Errorf("get session: %w", err)
	}

	meta := make(map[string]interface{})
	if sess.Metadata != "" {
		if decodeErr := json.Unmarshal([]byte(sess.Metadata), &meta); decodeErr != nil {
			return fmt.Errorf("parse session metadata: %w", decodeErr)
		}
	}
	meta["shell_mode"] = mode

	out, err := json.Marshal(meta)
	if err != nil {
		return fmt.Errorf("marshal metadata: %w", err)
	}
	return s.store.UpdateSessionMetadata(ctx, sessionID, string(out))
}

// WorkDir returns the directory the session's shell commands run in: the
// session metadata's project_dir when set, else the home directory ("/" if
// even that is unknown).
func (s *ShellService) WorkDir(ctx context.Context, sessionID string) string {
	sess, err := s.store.GetSession(ctx, sessionID)
	if err != nil {
		return fallbackHomeDir()
	}
	var meta map[string]interface{}
	if err := json.Unmarshal([]byte(sess.Metadata), &meta); err == nil {
		if dir, ok := meta["project_dir"].(string); ok && dir != "" {
			return dir
		}
	}
	return fallbackHomeDir()
}

// ShellExecResult is the outcome of ShellService.Exec.
type ShellExecResult struct {
	// RequiresApproval is set when the session is in "ask" mode and the
	// command was not approved; nothing ran and nothing was recorded.
	RequiresApproval bool
	Mode             shell.Mode
	MessageID        string
	Command          string
	Output           string
	ExitCode         int
	TimedOut         bool
	SandboxIsolated  bool
}

// Exec runs a user's shell command in the session, subject to its mode:
// "ask" needs approved, "session" runs in the OS sandbox, "yolo" runs
// without it. When the sandbox was asked for but could not be applied, the
// output is prefixed with a degraded-mode warning. The command and its
// output are recorded as a user message in the session.
//
// A command the sandbox refuses is a *ShellDeniedError; other run failures
// are returned as they are. A failure to record the message is an error
// prefixed "persist shell output: ".
func (s *ShellService) Exec(ctx context.Context, sessionID, command string, approved bool) (*ShellExecResult, error) {
	mode := s.Mode(ctx, sessionID)
	if mode == shell.ModeAsk && !approved {
		return &ShellExecResult{RequiresApproval: true, Mode: mode, Command: command}, nil
	}

	result, err := sandbox.UserExec(sandbox.UserExecOpts{
		Command:   resolveShell(),
		Args:      []string{"-c", command},
		Dir:       s.WorkDir(ctx, sessionID),
		Sandboxed: mode != shell.ModeYOLO, // OS sandbox for ask+session, not yolo
	})
	if err != nil {
		if strings.Contains(err.Error(), "denied") {
			return nil, &ShellDeniedError{Err: err}
		}
		return nil, err
	}
	output := result.Stdout
	if result.Stderr != "" {
		output += result.Stderr
	}
	if mode != shell.ModeYOLO && !result.SandboxIsolated {
		output = "[sandbox: OS-level isolation NOT applied — running in degraded mode]\n" + output
	}

	meta := map[string]interface{}{
		"type": "shell_exec",
		"shell_exec": map[string]interface{}{
			"command":          command,
			"exit_code":        result.ExitCode,
			"timed_out":        result.TimedOut,
			"sandbox_isolated": result.SandboxIsolated,
		},
	}
	metaJSON, _ := json.Marshal(meta)

	msg := &store.Message{
		ID:        uuid.New().String(),
		SessionID: sessionID,
		Role:      "user",
		Content:   fmt.Sprintf("$ %s\n%s", command, output),
		Metadata:  string(metaJSON),
	}
	if err := s.store.CreateMessage(ctx, msg); err != nil {
		return nil, errors.New("persist shell output: " + err.Error())
	}

	return &ShellExecResult{
		Mode:            mode,
		MessageID:       msg.ID,
		Command:         command,
		Output:          output,
		ExitCode:        result.ExitCode,
		TimedOut:        result.TimedOut,
		SandboxIsolated: result.SandboxIsolated,
	}, nil
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
