package service

// CW-20261001-0232: only a turn whose caller is CallerChat mints path grants
// from its text, and even then only the ones the mention policy allows. A
// durable agent's wake (CallerBackground) or a subagent's turn is not a person
// typing, and a "chat" turn is not proof of one either, so a path named there
// never becomes a grant for ~/.ssh, the systemd user directory or anything
// else outside $HOME.

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/hollis-labs/nanite/internal/dispatcher"
	"github.com/hollis-labs/nanite/internal/permission"
)

func newMentionTestService(t *testing.T) (*chatServiceImpl, *callerRecordingRunner, *permission.PathGrants, string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	for _, env := range []string{"XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_STATE_HOME"} {
		t.Setenv(env, "")
	}
	for _, d := range []string{".ssh", filepath.Join(".config", "systemd", "user"), filepath.Join("dev", "proj")} {
		if err := os.MkdirAll(filepath.Join(home, d), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	runner := newCallerRecordingRunner()
	svc, _ := newHandleMessageCallerTypeTestService(t, runner)
	grants := permission.NewPathGrants()
	// As NewContainer configures it.
	grants.SetMentionPolicy(permission.MentionPolicy{Confine: true})
	svc.pathGrants = grants
	return svc, runner, grants, home
}

func waitDispatched(t *testing.T, runner *callerRecordingRunner) {
	t.Helper()
	select {
	case <-runner.done:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for runner.Invoke")
	}
}

const mentionTurn = "write ~/.ssh/authorized_keys and ~/.config/systemd/user/x.service and /etc/hosts and ~/dev/proj/main.go"

func TestHandleMessage_ChatTurnGrantsOnlyWhatThePolicyAllows(t *testing.T) {
	svc, runner, grants, home := newMentionTestService(t)

	if _, err := svc.HandleMessage(context.Background(), "sess-chat", mentionTurn); err != nil {
		t.Fatalf("HandleMessage: %v", err)
	}
	waitDispatched(t, runner)

	if !grants.IsPathAllowed("sess-chat", filepath.Join(home, "dev", "proj", "main.go")) {
		t.Fatal("a chat turn's mention of ~/dev/proj/main.go was not granted")
	}
	for _, refused := range []string{
		filepath.Join(home, ".ssh", "authorized_keys"),
		filepath.Join(home, ".config", "systemd", "user", "x.service"),
		"/etc/hosts",
		"/etc/shadow",
		filepath.Join(home, ".ssh", "id_ed25519"),
	} {
		if grants.IsPathAllowed("sess-chat", refused) {
			t.Fatalf("a chat turn's mention granted %s", refused)
		}
	}
}

func TestHandleMessage_NonChatTurnsRegisterNoGrants(t *testing.T) {
	for _, caller := range []dispatcher.CallerType{dispatcher.CallerBackground, dispatcher.CallerSubagent} {
		t.Run(string(caller), func(t *testing.T) {
			svc, runner, grants, home := newMentionTestService(t)
			ctx := dispatcher.WithCallerType(context.Background(), caller)

			if _, err := svc.HandleMessage(ctx, "sess-other", mentionTurn); err != nil {
				t.Fatalf("HandleMessage: %v", err)
			}
			waitDispatched(t, runner)

			if n := grants.BucketSize("sess-other"); n != 0 {
				t.Fatalf("a %s turn registered %d grants: %v", caller, n, grants.ListGrants("sess-other"))
			}
			if grants.IsPathAllowed("sess-other", filepath.Join(home, "dev", "proj", "main.go")) {
				t.Fatalf("a %s turn granted a path its text named", caller)
			}
		})
	}
}

// The durable-agent wake path end to end: SendMessage stamps CallerBackground.
func TestHandleMessage_WakeThroughDurableControllerRegistersNoGrants(t *testing.T) {
	svc, runner, grants, _ := newMentionTestService(t)

	runtime := NewChatDurableAgentRuntimeController(svc)
	if err := runtime.SendMessage(context.Background(), "sess-wake", mentionTurn); err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	waitDispatched(t, runner)

	if n := grants.BucketSize("sess-wake"); n != 0 {
		t.Fatalf("a wake turn registered %d grants: %v", n, grants.ListGrants("sess-wake"))
	}
}

// A subagent's lineage carries its parent's grants and nothing it could add
// itself: its own turn mints none, and what the parent was refused is not there.
func TestHandleMessage_SubagentLineageCannotReAddGrants(t *testing.T) {
	svc, runner, grants, home := newMentionTestService(t)

	if _, err := svc.HandleMessage(context.Background(), "sess-parent", mentionTurn); err != nil {
		t.Fatalf("parent HandleMessage: %v", err)
	}
	waitDispatched(t, runner)
	grants.RegisterLineage("sess-child", "sess-parent")

	ctx := dispatcher.WithCallerType(context.Background(), dispatcher.CallerSubagent)
	if _, err := svc.HandleMessage(ctx, "sess-child", "also ~/.ssh/config and /etc/passwd and /var/lib/other"); err != nil {
		t.Fatalf("child HandleMessage: %v", err)
	}
	waitDispatched(t, runner)

	if n := grants.BucketSize("sess-child"); n != 0 {
		// BucketSize counts the child's own bucket.
		t.Fatalf("the subagent's own bucket has %d grants: %v", n, grants.ListGrants("sess-child"))
	}
	for _, refused := range []string{
		filepath.Join(home, ".ssh", "config"), "/etc/passwd", "/var/lib/other", filepath.Join(home, ".ssh", "authorized_keys"),
	} {
		if grants.IsPathAllowed("sess-child", refused) {
			t.Fatalf("the subagent reached %s", refused)
		}
	}
	if !grants.IsPathAllowed("sess-child", filepath.Join(home, "dev", "proj", "other.go")) {
		t.Fatal("the subagent lost the parent's permitted grant")
	}
}
