package service

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	runtimeevents "github.com/hollis-labs/libs/harness/core/adapters/runtimeevents"
	runtimeagent "github.com/hollis-labs/nanite/internal/runtime/agent"
	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/internal/testhome"
)

// TestLiveCLIRuntimeModelAndHostDirectory boots a real Claude from a pinned
// definition and runtime=cli host settings, and checks two things the launch
// argv tests cannot: the host's model is the one the CLI reports running, and a
// host directory outside the work root is writable even though Claude does not
// trust the ephemeral boot dir (its settings.json additionalDirectories are
// ignored, so only the command-line --add-dir grants it).
//
// Opt-in, since it spawns claude and spends a turn:
//
//	NANITE_LIVE_CLAUDE=1 go test ./internal/service -run TestLiveCLIRuntimeModelAndHostDirectory -v -count=1
func TestLiveCLIRuntimeModelAndHostDirectory(t *testing.T) {
	if testing.Short() || os.Getenv("NANITE_LIVE_CLAUDE") == "" {
		t.Skip("set NANITE_LIVE_CLAUDE=1 to run against the real claude CLI")
	}
	ctx := context.Background()
	st := newTestStore(t)
	work, shared := t.TempDir(), t.TempDir()
	src := "---\nschema_version: \"2\"\ndefinition_id: def:live-cli\nrevision: \"1\"\nname: live-cli\ndescription: Live CLI check.\nbehavior:\n  purpose: Do exactly what is asked.\nrequirements: {}\nharness_profile:\n  context: {}\n  permissions:\n    profile: default\ncontinuity:\n  mode: ephemeral\n---\nDo exactly what is asked, briefly.\n"
	pin, err := st.InstallAgentDefinition(ctx, []byte(src), nil)
	if err != nil {
		t.Fatal(err)
	}
	host, err := st.CreateAgentHostSettings(ctx, store.AgentHostSettings{
		Slug: "live-cli", Title: "Live CLI", DefinitionRef: pin, Enabled: true, Source: "b2-live",
		Settings: store.NativeHostSettings{Version: "1", Runtime: "cli", Provider: "claude", Model: "haiku", Directories: []string{work, shared}},
	})
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := BuildAgentDependencies(AgentDepsConfig{Store: st, Streams: NewStreamManager(st), WorkspacesRoot: t.TempDir(), DBPath: filepath.Join(t.TempDir(), "live.db")})
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var terminal runtimeevents.Event
	var initModel string
	done := make(chan struct{})
	var once sync.Once
	bundle.Deps.RuntimeEventSink = func(string, bool) runtimeevents.Sink {
		return runtimeevents.SinkFunc(func(_ context.Context, ev runtimeevents.Event) error {
			mu.Lock()
			defer mu.Unlock()
			switch ev.Kind { //nolint:exhaustive // only the CLI's init line and terminal events matter here
			case runtimeevents.KindStdoutLine:
				var p struct{ Line string }
				_ = json.Unmarshal(ev.Payload, &p)
				var init struct {
					Subtype string `json:"subtype"`
					Model   string `json:"model"`
				}
				if json.Unmarshal([]byte(p.Line), &init) == nil && init.Subtype == "init" {
					initModel = init.Model
				}
			case runtimeevents.KindTurnCompleted, runtimeevents.KindTurnFailed:
				terminal = ev
				once.Do(func() { close(done) })
			}
			return nil
		})
	}
	t.Cleanup(func() { _ = bundle.Manager.Shutdown(context.Background()) })

	sess, err := runtimeagent.Boot(ctx, bundle.Deps, runtimeagent.Options{
		Mode: runtimeagent.ModeLongLived, AgentProfile: host.ID, Provider: "claude", Workdir: work,
		Env: map[string]string{"HOME": testhome.RealHome()},
	})
	if err != nil {
		t.Fatalf("Boot: %v", err)
	}
	t.Cleanup(func() { _ = sess.Stop(context.Background()) })
	target := filepath.Join(shared, "out.txt")
	if err = sess.SendInput([]byte("Use the Write tool to create the file " + target + " containing exactly HELLO. Then reply DONE.")); err != nil {
		t.Fatalf("SendInput: %v", err)
	}
	select {
	case <-done:
	case <-time.After(150 * time.Second):
		t.Fatal("no terminal turn event within 150s")
	}
	mu.Lock()
	defer mu.Unlock()
	if terminal.Kind != runtimeevents.KindTurnCompleted {
		t.Fatalf("turn ended with %q: %s", terminal.Kind, terminal.Payload)
	}
	if !strings.Contains(strings.ToLower(initModel), "haiku") {
		t.Errorf("CLI reported model %q, want the host's haiku", initModel)
	}
	got, err := os.ReadFile(target) //nolint:gosec // G304: target is under the test's own t.TempDir()
	if err != nil || strings.TrimSpace(string(got)) != "HELLO" {
		t.Errorf("host directory write: %q, %v", got, err)
	}
	t.Logf("cli model=%q wrote=%q", initModel, strings.TrimSpace(string(got)))
}
