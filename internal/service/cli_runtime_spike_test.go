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

	runtimeagent "github.com/hollis-labs/nanite/internal/runtime/agent"
	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/internal/testhome"
	runtimeevents "github.com/hollis-labs/substrate/harness/adapters/runtimeevents"
)

// TestSpikeCLIRuntimeClaudeTurn is the B1 spike (CW-20261014-0155): one real
// Claude CLI turn driven through runtimeagent.Boot from a pinned definition
// plus runtime=cli host settings on a scratch DB. It is opt-in because it
// spawns the real claude binary and spends a turn:
//
//	NANITE_SPIKE_CLAUDE=1 go test ./internal/service -run TestSpikeCLIRuntimeClaudeTurn -v
func TestSpikeCLIRuntimeClaudeTurn(t *testing.T) {
	if testing.Short() || os.Getenv("NANITE_SPIKE_CLAUDE") == "" {
		t.Skip("set NANITE_SPIKE_CLAUDE=1 to run the real-Claude spike")
	}
	ctx := context.Background()
	st := newTestStore(t)

	src := "---\nschema_version: \"2\"\ndefinition_id: def:spike-cli\nrevision: \"1\"\nname: spike-cli\ndescription: CLI runtime spike.\nbehavior:\n  purpose: Answer in exactly one word.\nrequirements: {}\nharness_profile:\n  context: {}\n  permissions:\n    profile: default\ncontinuity:\n  mode: ephemeral\n---\nYou are a spike agent. Reply with the single word PONG and nothing else.\n"
	pin, err := st.InstallAgentDefinition(ctx, []byte(src), nil)
	if err != nil {
		t.Fatal(err)
	}
	host, err := st.CreateAgentHostSettings(ctx, store.AgentHostSettings{
		Slug: "spike-cli", Title: "Spike CLI", DefinitionRef: pin, Enabled: true,
		Source:   "b1-spike",
		Settings: store.NativeHostSettings{Version: "1", Runtime: "cli", Provider: "claude", Model: "haiku"},
	})
	if err != nil {
		t.Fatal(err)
	}

	bundle, err := BuildAgentDependencies(AgentDepsConfig{
		Store: st, Streams: NewStreamManager(st), WorkspacesRoot: t.TempDir(),
		DBPath: filepath.Join(t.TempDir(), "spike.db"),
	})
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var kinds []string
	var text strings.Builder
	done := make(chan struct{})
	var once sync.Once
	bundle.Deps.RuntimeEventSink = func(string, bool) runtimeevents.Sink {
		return runtimeevents.SinkFunc(func(_ context.Context, ev runtimeevents.Event) error {
			mu.Lock()
			defer mu.Unlock()
			kinds = append(kinds, string(ev.Kind))
			switch ev.Kind {
			case runtimeevents.KindStderrLine, runtimeevents.KindTurnFailed, runtimeevents.KindAgentDelta:
				t.Logf("%s %.400s", ev.Kind, ev.Payload)
			}
			switch ev.Kind {
			case runtimeevents.KindAgentDelta:
				var p struct {
					Content string `json:"content"`
				}
				_ = json.Unmarshal(ev.Payload, &p)
				text.WriteString(p.Content)
			case runtimeevents.KindTurnCompleted, runtimeevents.KindTurnFailed:
				once.Do(func() { close(done) })
			}
			return nil
		})
	}
	t.Cleanup(func() { _ = bundle.Manager.Shutdown(context.Background()) })

	// The host-settings slug resolves, through the existing store projection,
	// to the profile Boot consumes: definition text -> SystemPrompt, host
	// provider/model/runtime -> DefaultProvider/DefaultModel/RuntimeKind.
	sess, err := runtimeagent.Boot(ctx, bundle.Deps, runtimeagent.Options{
		Mode: runtimeagent.ModeLongLived, AgentProfile: host.Slug, Provider: host.Settings.Provider,
		Workdir: t.TempDir(), ExternalLifecycleObserver: false,
		// The test binary runs under an isolated HOME; Claude's login lives in the
		// real one, as it does for the daemon.
		Env: map[string]string{"HOME": testhome.RealHome()},
	})
	if err != nil {
		t.Fatalf("Boot: %v", err)
	}
	t.Cleanup(func() { _ = sess.Stop(context.Background()) })
	if err := sess.SendInput([]byte("ping")); err != nil {
		t.Fatalf("SendInput: %v", err)
	}
	select {
	case <-done:
	case <-time.After(120 * time.Second):
		t.Fatal("no terminal turn event within 120s")
	}
	mu.Lock()
	defer mu.Unlock()
	t.Logf("provider_session_id=%q event kinds=%v reply=%q", sess.ProviderSessionID(), kinds, text.String())
	if !strings.Contains(strings.ToUpper(text.String()), "PONG") {
		t.Errorf("reply %q does not contain PONG", text.String())
	}
}
