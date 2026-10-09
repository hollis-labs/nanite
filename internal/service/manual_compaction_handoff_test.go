package service

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/nanite/internal/chat"
	"github.com/hollis-labs/nanite/internal/store"
	ctxpkg "github.com/hollis-labs/substrate/agent/context"
	llmtypes "github.com/hollis-labs/substrate/llm-core/llmtypes"
)

func manualHandoffFixture(t *testing.T) (*characterizationFixture, *Container, *fakeEventEmitter) {
	t.Helper()
	f := newCharacterizationFixture(t, nil)
	ctx := context.Background()
	for i := 0; i < 12; i++ {
		role := "user"
		if i%2 == 1 {
			role = "assistant"
		}
		if err := f.st.CreateMessage(ctx, &store.Message{
			ID: fmt.Sprintf("handoff-message-%d", i), SessionID: f.session, Role: role,
			Content: fmt.Sprintf("turn %d: %s", i, strings.Repeat("continuity ", 150)),
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.st.UpdateUserSettings(ctx, &store.UserSettings{
		ContextOverflowRecovery: true, SummarizerProvider: "characterization", SummarizerModel: "characterization-model",
	}); err != nil {
		t.Fatal(err)
	}
	f.context.mutate = func(result *SlotAssemblyResult) {
		forceCompactableWindow(result)
		// Fixed host context must survive independently of the compactable
		// conversation and enrichment. These values grant no model authority.
		result.Window.SetContent(ctxpkg.SlotSystem, "host system instruction")
		result.Window.SetContent(ctxpkg.SlotPermissions, "host-owned permission boundary")
		result.Window.SetContent(ctxpkg.SlotUserContext, "pinned host context")
	}
	events := &fakeEventEmitter{}
	f.svc.events = events
	c := &Container{
		Sessions: f.svc.sessions, Agents: f.svc.agents, Settings: NewUserSettingsService(f.st),
		Context: f.context, Providers: f.svc.providers, ProviderDefaults: f.st,
		CompactionEvents: NewCompactionEventWriter(f.st), CompactionHandoffs: f.st,
		Events: events, Streams: f.svc.streams,
	}
	return f, c, events
}

func TestManualCompactionHandoff_MatchesAutomaticPaths(t *testing.T) {
	for _, proactive := range []bool{false, true} {
		t.Run(fmt.Sprintf("proactive=%t", proactive), func(t *testing.T) {
			var expected *ctxpkg.HandoffPayload
			var expectedSlot string
			for _, path := range []string{"manual", "overflow", "budget"} {
				t.Run(path, func(t *testing.T) {
					f, c, events := manualHandoffFixture(t)
					ctx := context.Background()
					stashID := ""
					if proactive {
						var writeErr error
						stashID, writeErr = WriteGlass4Handoff(f.st, f.session, ctxpkg.HandoffPayload{
							SessionIntent: "curated intent", NextStepAnchor: "continue the operator's plan",
						})
						if writeErr != nil {
							t.Fatal(writeErr)
						}
					}
					ch := make(chan chat.StreamEvent, 8)
					if path == "manual" {
						settings, settingsErr := f.st.GetUserSettings(ctx)
						if settingsErr != nil {
							t.Fatal(settingsErr)
						}
						settings.ContextOverflowRecovery = false
						if updateErr := f.st.UpdateUserSettings(ctx, settings); updateErr != nil {
							t.Fatal(updateErr)
						}
						c.Streams.CreateStream("manual-handoff-stream", f.session)
						stream, _, ok := c.Streams.Subscribe("manual-handoff-stream", 0)
						if !ok {
							t.Fatal("subscribe")
						}
						outcome, compactErr := c.CompactSession(ctx, f.session)
						if compactErr != nil || outcome == nil || len(outcome.StagesApplied) == 0 {
							t.Fatalf("manual outcome = %+v, %v", outcome, compactErr)
						}
						// The conversation notification retains its established
						// first position; the new handoff event follows it.
						for _, kind := range []string{"slot_changed", "handoff_loaded"} {
							select {
							case ev := <-stream:
								if ev.Type != kind {
									t.Fatalf("event = %q, want %q", ev.Type, kind)
								}
							case <-time.After(time.Second):
								t.Fatalf("missing %s", kind)
							}
						}
					} else {
						sess, getErr := f.st.GetSession(ctx, f.session)
						if getErr != nil {
							t.Fatal(getErr)
						}
						agent, resolveErr := c.Agents.ResolveForSession(ctx, f.session)
						if resolveErr != nil {
							t.Fatal(resolveErr)
						}
						assembled, assemblyErr := c.Context.AssembleSlots(ctx, sess, agent, nil, "", 0, "")
						if assemblyErr != nil {
							t.Fatal(assemblyErr)
						}
						if path == "overflow" {
							_, _, ok := f.svc.recoverFromContextOverflow(ctx, f.session, assembled, agent, assembled.Messages, nil, ch, "rate budget", compactTriggerRateBudget)
							if !ok {
								t.Fatal("automatic recovery refused")
							}
						} else {
							f.svc.enforceBudgetOrCompact(ctx, f.session, assembled, agent, assembled.Messages, nil, ch)
						}
						if findEvent(drainManualHandoffEvents(ch), "handoff_loaded") == nil {
							t.Fatal("automatic handoff event missing")
						}
					}
					payload, latestID, readErr := ReadLatestGlass4Handoff(f.st, f.session)
					if readErr != nil || payload == nil {
						t.Fatalf("handoff = %+v, %v", payload, readErr)
					}
					if proactive && latestID != stashID {
						t.Fatal("compaction replaced the curated handoff")
					}
					if expected == nil {
						expected = payload
					} else if !reflect.DeepEqual(expected, payload) {
						t.Fatalf("handoff differs from manual: %+v vs %+v", expected, payload)
					}
					f.context.mutateLast(func(result *SlotAssemblyResult) {
						slot := result.Window.Slot(ctxpkg.SlotHandoff)
						if slot == nil || !slot.Flags.AutoInject || slot.Content != ctxpkg.RenderHandoffForSlot(*payload) {
							t.Fatalf("post-compaction handoff slot = %+v", slot)
						}
						if expectedSlot == "" {
							expectedSlot = slot.Content
						} else if expectedSlot != slot.Content {
							t.Fatal("manual and automatic rendered handoff differ")
						}
						for name, want := range map[string]string{
							ctxpkg.SlotSystem: "host system instruction", ctxpkg.SlotPermissions: "host-owned permission boundary", ctxpkg.SlotUserContext: "pinned host context",
						} {
							if got := result.Window.Slot(name).Content; got != want {
								t.Fatalf("%s changed: %q", name, got)
							}
						}
					})
					if len(events.pre) != 1 || len(events.post) != 1 || len(f.tools.calls()) != 0 {
						t.Fatalf("unexpected hooks/tool execution: %+v %+v %v", events.pre, events.post, f.tools.calls())
					}
				})
			}
		})
	}
}

func drainManualHandoffEvents(ch <-chan chat.StreamEvent) []chat.StreamEvent {
	var events []chat.StreamEvent
	for {
		select {
		case ev := <-ch:
			events = append(events, ev)
		default:
			return events
		}
	}
}

type manualFailingHandoffStore struct {
	HandoffStashStore
	err error
}

func (s manualFailingHandoffStore) GetLatestStashForSession(context.Context, string) (store.HandoffStash, error) {
	return store.HandoffStash{}, s.err
}

func (s manualFailingHandoffStore) UpsertHandoffStash(context.Context, store.HandoffStash) error {
	return s.err
}

func TestManualCompactionHandoff_StoreFailureIsNonfatal(t *testing.T) {
	f, c, events := manualHandoffFixture(t)
	c.CompactionHandoffs = manualFailingHandoffStore{HandoffStashStore: f.st, err: errors.New("handoff unavailable")}
	outcome, err := c.CompactSession(context.Background(), f.session)
	if err != nil || outcome == nil || len(events.post) != 1 {
		t.Fatalf("nonfatal handoff failure = %+v, %v, %+v", outcome, err, events.post)
	}
	payload, _, readErr := ReadLatestGlass4Handoff(f.st, f.session)
	if readErr != nil || payload != nil {
		t.Fatalf("failed handoff unexpectedly persisted: %+v, %v", payload, readErr)
	}
}

func TestManualCompactionHandoff_CanceledAssemblyHasNoEffects(t *testing.T) {
	f, c, events := manualHandoffFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	mutate := f.context.mutate
	f.context.mutate = func(result *SlotAssemblyResult) {
		mutate(result)
		cancel()
	}
	outcome, err := c.CompactSession(ctx, f.session)
	if outcome != nil || !errors.Is(err, context.Canceled) || len(events.pre) != 0 || len(events.post) != 0 {
		t.Fatalf("canceled assembly = %+v, %v, %+v, %+v", outcome, err, events.pre, events.post)
	}
	payload, _, readErr := ReadLatestGlass4Handoff(f.st, f.session)
	if readErr != nil || payload != nil {
		t.Fatalf("canceled request wrote handoff: %+v, %v", payload, readErr)
	}
}

type manualFailingSummarizerProvider struct {
	*characterizationProvider
	complete func(context.Context) error
}

func (p manualFailingSummarizerProvider) Complete(ctx context.Context, _ llmtypes.ChatRequest) (string, error) {
	return "", p.complete(ctx)
}

func TestManualCompactionHandoff_PipelineFailureKeepsPreStashWithoutPost(t *testing.T) {
	for _, canceled := range []bool{false, true} {
		t.Run(fmt.Sprintf("canceled=%t", canceled), func(t *testing.T) {
			f, c, events := manualHandoffFixture(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			cause := errors.New("summarizer failed")
			if canceled {
				cause = context.Canceled
			}
			c.Providers.Register("characterization", manualFailingSummarizerProvider{
				characterizationProvider: f.provider,
				complete: func(callCtx context.Context) error {
					if callCtx != ctx {
						t.Fatal("summarizer did not receive caller context")
					}
					if canceled {
						cancel()
						return callCtx.Err()
					}
					return cause
				},
			})
			outcome, err := c.CompactSession(ctx, f.session)
			if outcome != nil || !errors.Is(err, cause) || len(events.pre) != 1 || len(events.post) != 0 {
				t.Fatalf("pipeline failure = %+v, %v, %+v, %+v", outcome, err, events.pre, events.post)
			}
			payload, _, readErr := ReadLatestGlass4Handoff(f.st, f.session)
			if readErr != nil || payload == nil {
				t.Fatalf("pre-compaction continuity not retained: %+v, %v", payload, readErr)
			}
			f.context.mutateLast(func(result *SlotAssemblyResult) {
				if result.Window.Slot(ctxpkg.SlotHandoff).Content != "" {
					t.Fatal("failed pipeline ran the post hook")
				}
			})
		})
	}
}
