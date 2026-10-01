package agent

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	llmtypes "github.com/hollis-labs/go-llm-types"
	"github.com/hollis-labs/go-providers/provider"
	"github.com/hollis-labs/nanite/internal/permission"
)

// fakeOpencodeSessionLostScript stands in for `opencode run`: it appends its
// argv to $NANITE_TEST_PROBE_FILE, fails a `--session <id>` resume the way
// opencode does for an id it no longer has ("Session not found" on stderr,
// exit 1, no JSON), and otherwise answers one JSON turn in a new session.
const fakeOpencodeSessionLostScript = `#!/bin/sh
printf '%s\n' "$*" >> "$NANITE_TEST_PROBE_FILE"
for a in "$@"; do
  if [ "$a" = "--session" ]; then
    echo "Error: Session not found" >&2
    exit 1
  fi
done
printf '%s\n' \
  '{"type":"step_start","sessionID":"ses_new","part":{"type":"step-start"}}' \
  '{"type":"text","sessionID":"ses_new","part":{"type":"text","text":"fresh"}}' \
  '{"type":"step_finish","sessionID":"ses_new","part":{"type":"step-finish","reason":"stop","tokens":{"input":1,"output":1,"reasoning":0,"cache":{"read":0,"write":0}}}}'
`

// CW-20260930-0113 review: a resume the provider no longer has must not fail
// twice. The lost turn clears the persisted provider session id inside
// Session.SendInput, before the chat path's cleanupFailedRuntimeSend stops
// the session, so the next message, whether on this session or after a cold
// re-boot that resumes whatever agent_runtime holds (chat_boot_drive.go),
// runs without --session.
func TestSessionLost_NextTurnStartsFresh(t *testing.T) {
	dir := t.TempDir()
	probe := filepath.Join(dir, "argv.log")
	script := filepath.Join(dir, "fake-opencode.sh")
	if err := os.WriteFile(script, []byte(fakeOpencodeSessionLostScript), 0o755); err != nil { //nolint:gosec // an executable test fixture in t.TempDir()
		t.Fatal(err)
	}
	t.Setenv("OPENCODE_CLI_PATH", script)

	store := newFakeRuntimeStore()
	profile := storeProfile("opencode")
	deps := &Dependencies{
		Agents:         &fakeAgentProfiles{profile: &profile},
		Manager:        NewSessionManager(),
		Store:          store,
		PathGrants:     permission.NewPathGrants(),
		EventFanout:    func(string) chan<- llmtypes.StreamEvent { return make(chan llmtypes.StreamEvent, 64) },
		WorkspacesRoot: t.TempDir(),
	}
	t.Cleanup(func() { _ = deps.Manager.Shutdown(context.Background()) })

	boot := func(sessionID string) *Session {
		t.Helper()
		sess, err := Boot(context.Background(), deps, Options{
			Mode:                    ModeLongLived,
			SessionID:               sessionID,
			Role:                    "executor",
			ResumeProviderSessionID: store.provIDs[sessionID], // what chat_boot_drive reads before Boot
			Env:                     map[string]string{"NANITE_TEST_PROBE_FILE": probe},
		})
		if err != nil {
			t.Fatalf("Boot: %v", err)
		}
		return sess
	}
	stop := func(sess *Session) {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = sess.Stop(ctx)
	}
	lastArgv := func() string {
		t.Helper()
		raw, err := os.ReadFile(probe) //nolint:gosec // this test's own probe file
		if err != nil {
			t.Fatal(err)
		}
		lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
		return lines[len(lines)-1]
	}

	const sessionID = "rt-session-lost"
	lostTurn := func() *Session {
		t.Helper()
		store.provIDs[sessionID] = "ses_dead" // a resume id the provider has since lost
		sess := boot(sessionID)
		err := sess.SendInput([]byte("hello"))
		if !errors.Is(err, provider.ErrProviderSessionLost) {
			t.Fatalf("SendInput on a lost resume = %v, want ErrProviderSessionLost", err)
		}
		if !strings.Contains(lastArgv(), "--session ses_dead") {
			t.Fatalf("lost turn argv = %q, want it to resume ses_dead", lastArgv())
		}
		if got := store.provIDs[sessionID]; got != "" {
			t.Fatalf("provider session id after the lost turn = %q, want it cleared", got)
		}
		return sess
	}

	// The chat path: cleanupFailedRuntimeSend stops the session, and the next
	// message cold-boots, resuming whatever agent_runtime holds.
	stop(lostTurn())
	sess := boot(sessionID)
	if err := sess.SendInput([]byte("after reboot")); err != nil {
		t.Fatalf("turn after the re-boot: %v", err)
	}
	if strings.Contains(lastArgv(), "--session") {
		t.Fatalf("argv after the re-boot = %q, want no --session (the dead id was resumed again)", lastArgv())
	}
	stop(sess)

	// The same live session: agentkit dropped the dead id too.
	sess = lostTurn()
	if err := sess.SendInput([]byte("again")); err != nil {
		t.Fatalf("next turn on the same session: %v", err)
	}
	if strings.Contains(lastArgv(), "--session") {
		t.Fatalf("next turn argv = %q, want no --session", lastArgv())
	}
	stop(sess)
}
