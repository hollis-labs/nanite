package service

// D-38 (CW-20260929-0010): a subagent spawned from an API-driven parent runs
// through the chat harness unless the user or app opted into CLI subagents.

import (
	"context"
	"strings"
	"testing"

	llmtypes "github.com/hollis-labs/go-llm-types"
	"github.com/hollis-labs/go-providers/provider"
	runtimeagent "github.com/hollis-labs/nanite/internal/runtime/agent"
	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/internal/subagent"
)

// runtimeStore adds the two reads subagentRuntime resolves through to the
// recording session store.
type runtimeStore struct {
	*recordingSessionStore
	sessionRuntime string
	appRuntime     string
}

func (s *runtimeStore) GetSessionSubagentRuntime(context.Context, string) (string, error) {
	return s.sessionRuntime, nil
}

func (s *runtimeStore) GetUserSettings(context.Context) (*store.UserSettings, error) {
	return &store.UserSettings{SubagentRuntime: s.appRuntime}, nil
}

// ctxLegacyRunner records the override the harness runner would see.
type ctxLegacyRunner struct {
	called   int
	override childRuntimeOverride
	hasOver  bool
}

func (f *ctxLegacyRunner) Run(ctx context.Context, _ *subagent.Run) (*subagent.Result, error) {
	f.called++
	f.override, f.hasOver = childRuntimeOverrideFrom(ctx)
	return &subagent.Result{Summary: "child summary", ResultJSON: "{}"}, nil
}

type runtimeHarness struct {
	runner *BootRunner
	legacy *ctxLegacyRunner
	boots  *int
}

// newRuntimeHarness builds a BootRunner whose only CLI adapter is "pty-claude".
// parentProvider is the spawning session's provider; roleProvider is the
// role profile's DefaultProvider.
func newRuntimeHarness(t *testing.T, parentProvider, roleProvider, sessionRuntime, appRuntime string) runtimeHarness {
	t.Helper()
	bridge := newFakeBridge()
	st := &runtimeStore{
		recordingSessionStore: &recordingSessionStore{parents: map[string]*store.Session{
			"sess-p": {ID: "sess-p", Provider: parentProvider, Model: "parent-model"},
		}},
		sessionRuntime: sessionRuntime,
		appRuntime:     appRuntime,
	}
	boots := 0
	booter := func(_ context.Context, _ *runtimeagent.Dependencies, opts runtimeagent.Options) (*runtimeagent.Session, error) {
		boots++
		go func() {
			if ch := bridge.chanFor(opts.SessionID); ch != nil {
				ch <- llmtypes.StreamEvent{Type: llmtypes.EventDone}
			}
		}()
		return &runtimeagent.Session{ID: opts.SessionID}, nil
	}
	legacy := &ctxLegacyRunner{}
	return runtimeHarness{
		runner: &BootRunner{
			deps: &runtimeagent.Dependencies{ProviderAdapter: func(name string) provider.CLIAdapter {
				if name == "pty-claude" {
					return &fakeCLIAdapter{name: name}
				}
				return nil
			}},
			bridge: bridge,
			agents: &stubAgentReaderForRunner{agents: map[string]*store.AgentProfile{
				"role": {ID: "ag", Slug: "role", DefaultProvider: roleProvider, DefaultModel: "role-model"},
			}},
			store:     st,
			booter:    booter,
			persistFn: func(context.Context, string, string) error { return nil },
			legacy:    legacy,
		},
		legacy: legacy,
		boots:  &boots,
	}
}

func spawnRun(providerArg string) *subagent.Run {
	return &subagent.Run{ID: "run-1", Role: "role", ParentSessionID: "sess-p", Prompt: "p", Provider: providerArg}
}

// The downgrade path: an API parent, a CLI provider named by the model's
// provider arg, no opt-in. The child runs on the harness with the parent's
// provider and model, and the result says so.
func TestBootRunner_APIParent_CLIProviderArg_DowngradesToHarness(t *testing.T) {
	h := newRuntimeHarness(t, "anthropic", "anthropic", "", "")
	res, err := h.runner.Run(context.Background(), spawnRun("pty-claude"))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if *h.boots != 0 {
		t.Errorf("booter called %d times, want 0 — a model-chosen provider arg must not bypass the harness", *h.boots)
	}
	if h.legacy.called != 1 {
		t.Fatalf("harness runner called %d times, want 1", h.legacy.called)
	}
	if !h.legacy.hasOver || h.legacy.override.provider != "anthropic" || h.legacy.override.model != "parent-model" {
		t.Errorf("override = %+v (present=%v), want the parent's anthropic/parent-model", h.legacy.override, h.legacy.hasOver)
	}
	for _, want := range []string{`"pty-claude"`, "subagent_runtime is api", "anthropic/parent-model", "child summary"} {
		if !strings.Contains(res.Summary, want) {
			t.Errorf("summary missing %q: %q", want, res.Summary)
		}
	}
}

// A role whose own DefaultProvider is a CLI provider is downgraded the same way.
func TestBootRunner_APIParent_CLIRoleDefault_DowngradesToHarness(t *testing.T) {
	h := newRuntimeHarness(t, "anthropic", "pty-claude", "", "")
	if _, err := h.runner.Run(context.Background(), spawnRun("")); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if *h.boots != 0 || h.legacy.called != 1 {
		t.Errorf("boots=%d harness=%d, want 0 and 1", *h.boots, h.legacy.called)
	}
}

// An API role on an API parent is untouched: no override, no note.
func TestBootRunner_APIParent_APIRole_NoOverrideNoNote(t *testing.T) {
	h := newRuntimeHarness(t, "anthropic", "anthropic", "", "")
	res, err := h.runner.Run(context.Background(), spawnRun(""))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if h.legacy.hasOver {
		t.Errorf("unexpected override %+v on a plain API spawn", h.legacy.override)
	}
	if res.Summary != "child summary" {
		t.Errorf("summary = %q, want it untouched", res.Summary)
	}
}

// The opt-in lives with the user or app: the app default and the session
// override, the session winning.
func TestBootRunner_APIParent_RuntimeResolution(t *testing.T) {
	cases := []struct {
		name         string
		session, app string
		wantBoot     bool
	}{
		{"app cli", "", "cli", true},
		{"session cli over app unset", "cli", "", true},
		{"session cli over app api", "cli", "api", true},
		{"session api over app cli", "api", "cli", false},
		{"both unset", "", "", false},
		{"app api", "", "api", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newRuntimeHarness(t, "anthropic", "anthropic", tc.session, tc.app)
			if _, err := h.runner.Run(context.Background(), spawnRun("pty-claude")); err != nil {
				t.Fatalf("Run: %v", err)
			}
			if booted := *h.boots == 1; booted != tc.wantBoot {
				t.Errorf("booted=%v, want %v (boots=%d harness=%d)", booted, tc.wantBoot, *h.boots, h.legacy.called)
			}
		})
	}
}

// A CLI parent keeps today's behavior whatever the runtime setting.
func TestBootRunner_CLIParent_KeepsCLI(t *testing.T) {
	h := newRuntimeHarness(t, "pty-claude", "anthropic", "api", "api")
	if _, err := h.runner.Run(context.Background(), spawnRun("pty-claude")); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if *h.boots != 1 || h.legacy.called != 0 {
		t.Errorf("boots=%d harness=%d, want 1 and 0", *h.boots, h.legacy.called)
	}
}

// The override changes the child session row, not just the runner's intent:
// ChatRunner creates the child on the parent's provider and model.
func TestChatRunner_CreateChildSession_HonorsRuntimeOverride(t *testing.T) {
	st := &recordingSessionStore{parents: map[string]*store.Session{"sess-p": {ID: "sess-p", Provider: "anthropic", Model: "parent-model"}}}
	r := &ChatRunner{store: st}
	agent := &store.AgentProfile{ID: "ag", Slug: "role", DefaultProvider: "pty-claude", DefaultModel: "role-model"}
	ctx := withChildRuntimeOverride(context.Background(), "anthropic", "parent-model")
	if _, err := r.createChildSession(ctx, &subagent.Run{ParentSessionID: "sess-p", Prompt: "p", Role: "role"}, agent); err != nil {
		t.Fatalf("createChildSession: %v", err)
	}
	if len(st.created) != 1 || st.created[0].Provider != "anthropic" || st.created[0].Model != "parent-model" {
		t.Errorf("child session = %+v, want anthropic/parent-model", st.created)
	}
	// Without an override the role's own values stand.
	st.created = nil
	if _, err := r.createChildSession(context.Background(), &subagent.Run{ParentSessionID: "sess-p", Prompt: "p", Role: "role"}, agent); err != nil {
		t.Fatalf("createChildSession: %v", err)
	}
	if st.created[0].Provider != "pty-claude" || st.created[0].Model != "role-model" {
		t.Errorf("child session = %+v, want the role's pty-claude/role-model", st.created[0])
	}
}
