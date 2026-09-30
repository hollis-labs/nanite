package api

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/internal/shell"
	"github.com/hollis-labs/nanite/internal/store"
)

func newShellSession(t *testing.T, a *API, id, metadata string) {
	t.Helper()
	if err := a.Services.Store.CreateSession(context.Background(), &store.Session{ID: id, Title: id, Metadata: metadata}); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
}

// The mode fails safe: anything short of a readable, valid shell_mode is
// "ask" — including a canceled request context.
func TestShellMode_FailsSafeToAsk(t *testing.T) {
	a, _ := newTestAPI(t)
	newShellSession(t, a, "sh-yolo", `{"shell_mode":"yolo"}`)
	newShellSession(t, a, "sh-none", `{}`)
	newShellSession(t, a, "sh-bogus", `{"shell_mode":"bogus"}`)
	newShellSession(t, a, "sh-corrupt", `{"shell_mode":`)

	ctx := context.Background()
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	for _, c := range []struct {
		name string
		ctx  context.Context
		id   string
		want shell.Mode
	}{
		{"yolo", ctx, "sh-yolo", shell.ModeYOLO},
		{"canceled ctx reads ask", canceled, "sh-yolo", shell.ModeAsk},
		{"unset", ctx, "sh-none", shell.ModeAsk},
		{"unknown value", ctx, "sh-bogus", shell.ModeAsk},
		{"corrupt metadata", ctx, "sh-corrupt", shell.ModeAsk},
		{"unknown session", ctx, "nope", shell.ModeAsk},
	} {
		if got := a.Services.Shell.Mode(c.ctx, c.id); got != c.want {
			t.Errorf("%s: Mode = %q, want %q", c.name, got, c.want)
		}
	}
	home, _ := os.UserHomeDir()
	if got := a.Services.Shell.WorkDir(canceled, "sh-yolo"); got != home {
		t.Errorf("WorkDir with canceled ctx = %q, want home %q", got, home)
	}
}

func TestShellMode_HTTP(t *testing.T) {
	a, mux := newTestAPI(t)
	newShellSession(t, a, "sh-http", `{"keep":"me"}`)
	base := "/api/sessions/sh-http/shell-mode"

	if w := mcpDo(mux, "GET", base, ""); strings.TrimSpace(w.Body.String()) != `{"mode":"ask"}` {
		t.Fatalf("default: %s", w.Body.String())
	}
	if w := mcpDo(mux, "PUT", base, `not json`); w.Code != http.StatusBadRequest {
		t.Fatalf("bad body: %d %s", w.Code, w.Body.String())
	}
	if w := mcpDo(mux, "PUT", base, `{"mode":"bogus"}`); w.Code != http.StatusBadRequest || errorBody(t, w) != "mode must be ask, session, or yolo" {
		t.Fatalf("bad mode: %d %s", w.Code, w.Body.String())
	}
	if w := mcpDo(mux, "PUT", "/api/sessions/nope/shell-mode", `{"mode":"yolo"}`); w.Code != http.StatusInternalServerError || !strings.HasPrefix(errorBody(t, w), "get session: ") {
		t.Fatalf("unknown session: %d %s", w.Code, w.Body.String())
	}
	if w := mcpDo(mux, "PUT", base, `{"mode":"session"}`); w.Code != http.StatusOK || strings.TrimSpace(w.Body.String()) != `{"mode":"session"}` {
		t.Fatalf("set: %d %s", w.Code, w.Body.String())
	}
	if w := mcpDo(mux, "GET", base, ""); strings.TrimSpace(w.Body.String()) != `{"mode":"session"}` {
		t.Fatalf("after set: %s", w.Body.String())
	}
	sess, err := a.Services.Store.GetSession(context.Background(), "sh-http")
	if err != nil || !strings.Contains(sess.Metadata, `"keep":"me"`) {
		t.Fatalf("metadata merge lost a key: %q %v", sess.Metadata, err)
	}
}

func TestShellExec_ApprovalWorkDirAndRecord(t *testing.T) {
	a, mux := newTestAPI(t)
	dir := t.TempDir()
	meta, _ := json.Marshal(map[string]string{"shell_mode": "yolo", "project_dir": dir})
	newShellSession(t, a, "sh-yolo-dir", string(meta))
	newShellSession(t, a, "sh-ask", `{}`)

	if w := mcpDo(mux, "POST", "/api/sessions/sh-ask/shell-exec", `{}`); w.Code != http.StatusBadRequest || errorBody(t, w) != "command is required" {
		t.Fatalf("no command: %d %s", w.Code, w.Body.String())
	}
	w := mcpDo(mux, "POST", "/api/sessions/sh-ask/shell-exec", `{"command":"echo hi"}`)
	if w.Code != http.StatusOK || strings.TrimSpace(w.Body.String()) != `{"command":"echo hi","mode":"ask","requires_approval":true}` {
		t.Fatalf("ask mode: %d %s", w.Code, w.Body.String())
	}
	if msgs, _ := a.Services.Store.ListMessages(context.Background(), "sh-ask", 10); len(msgs) != 0 {
		t.Fatalf("unapproved command was recorded: %+v", msgs)
	}

	w = mcpDo(mux, "POST", "/api/sessions/sh-yolo-dir/shell-exec", `{"command":"pwd"}`)
	var res struct {
		MessageID string `json:"message_id"`
		Output    string `json:"output"`
		ExitCode  int    `json:"exit_code"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil || w.Code != http.StatusOK {
		t.Fatalf("exec: %d %s", w.Code, w.Body.String())
	}
	resolved, _ := filepath.EvalSymlinks(dir)
	if got := strings.TrimSpace(res.Output); got != dir && got != resolved {
		t.Fatalf("pwd = %q, want the session's project_dir %q", got, dir)
	}
	msg, err := a.Services.Store.GetMessage(context.Background(), res.MessageID)
	if err != nil || msg.Role != "user" || !strings.HasPrefix(msg.Content, "$ pwd\n") || !strings.Contains(msg.Metadata, `"type":"shell_exec"`) {
		t.Fatalf("recorded message = %+v, %v", msg, err)
	}

	w = mcpDo(mux, "GET", "/api/sessions/sh-yolo-dir/shell-info", "")
	if !strings.Contains(w.Body.String(), `"work_dir":"`+dir+`"`) {
		t.Fatalf("shell-info: %s", w.Body.String())
	}
	w = mcpDo(mux, "GET", "/api/sessions/sh-yolo-dir/shell-check?command=echo+ok", "")
	if !strings.Contains(w.Body.String(), `"mode":"yolo"`) {
		t.Fatalf("shell-check: %s", w.Body.String())
	}
}
