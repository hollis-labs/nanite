package service

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"strings"
	"testing"
	"time"

	llmtypes "github.com/hollis-labs/go-llm-types"
	permissionlib "github.com/hollis-labs/go-permission"
	ledger "github.com/hollis-labs/go-usage-ledger"
	"github.com/hollis-labs/nanite/internal/chat"
	ctxpkg "github.com/hollis-labs/nanite/internal/context"
	pluginpkg "github.com/hollis-labs/nanite/internal/plugin"
	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/internal/usagecost"
)

func terminationUsage(reason string) llmtypes.StreamEvent {
	report := usagecost.FromRaw("openai", `{"input_tokens":1000,"output_tokens":300,"input_tokens_details":{"cached_tokens":0,"cache_write_tokens":0},"output_tokens_details":{"reasoning_tokens":0}}`)
	return llmtypes.StreamEvent{Type: llmtypes.EventUsage, Content: usagecost.Content(report), Usage: &llmtypes.Usage{InputTokens: 1000, OutputTokens: 300, StopReason: reason}}
}

func terminationToolEvents(name string) []llmtypes.StreamEvent {
	return []llmtypes.StreamEvent{
		{Type: llmtypes.EventDelta, Content: "I will use a tool."},
		{Type: llmtypes.EventToolUse, ToolUse: &llmtypes.ToolUseBlock{ID: "tool-termination", Name: name, Input: map[string]any{"value": "fixture"}}},
		terminationUsage("tool_use"),
	}
}

// Verify the real persisted format, not just the in-memory accounting: one row
// per turn, all calls included, and prices captured before provider dispatch.
func assertTerminationSnapshot(t *testing.T, f *characterizationFixture, calls int, status string, cost float64) {
	t.Helper()
	var rows int
	if err := f.st.DB.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM token_usage WHERE message_id='assistant-termination'`).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 1 {
		t.Fatalf("usage rows=%d, want exactly one", rows)
	}
	var raw string
	var actualCost float64
	if err := f.st.DB.QueryRowContext(t.Context(), `SELECT cost_snapshot,estimated_cost_usd FROM token_usage WHERE message_id='assistant-termination'`).Scan(&raw, &actualCost); err != nil {
		t.Fatal(err)
	}
	var snapshot usagecost.Snapshot
	if err := json.Unmarshal([]byte(raw), &snapshot); err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Calls) != calls || snapshot.Status != status || math.Abs(actualCost-cost) > 1e-12 {
		t.Fatalf("snapshot=%+v cost=%v; want %d calls, %s, %v", snapshot, actualCost, calls, status, cost)
	}
	for _, call := range snapshot.Calls {
		if call.Price.InputPerMillion != 2 || call.Price.OutputPerMillion != 8 {
			t.Fatalf("frozen price lost: %+v", call.Price)
		}
	}
}

func TestTerminatedTurnPersistsIncurredUsage(t *testing.T) {
	for _, path := range []string{"stream error", "stream error without usage", "later dispatch error", "later stream error without usage", "budget refusal", "recovery refused", "canceled between calls", "tool panic", "provider panic", "stream stall", "assistant save error", "approval timeout then dispatch error"} {
		t.Run(path, func(t *testing.T) {
			f := newCharacterizationFixture(t, nil, "fixture_tool", "request_tools")
			f.svc.modelCatalog, _ = usageCatalogFixture(t, "characterization", "characterization-model")
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			steps := []characterizationProviderStep{{events: []llmtypes.StreamEvent{{Type: llmtypes.EventDelta, Content: "Answer started."}, terminationUsage("end_turn")}}}
			wantStatus, wantCost := "COMPLETE", .0044
			wantCalls := 1
			wantPanic := ""
			switch path {
			case "stream error":
				steps[0].events = append(steps[0].events, llmtypes.StreamEvent{Type: llmtypes.EventError, Error: "original provider failure"})
			case "stream error without usage":
				steps[0].events = []llmtypes.StreamEvent{{Type: llmtypes.EventError, Error: "original provider failure"}}
				wantStatus, wantCost = "PARTIAL", 0
			case "later dispatch error", "recovery refused", "canceled between calls", "provider panic", "approval timeout then dispatch error":
				steps[0].events = terminationToolEvents("fixture_tool")
				steps = append(steps, characterizationProviderStep{err: errors.New("original dispatch failure")})
				if path == "recovery refused" {
					steps[1].err = ctxpkg.ErrContextOverflow
				}
				if path == "canceled between calls" {
					steps[0].beforeReturn = cancel
				}
				if path == "provider panic" {
					steps[1].beforeReturn = func() { panic("original provider panic") }
					wantPanic = "original provider panic"
				}
				if path == "approval timeout then dispatch error" {
					f.svc.permissions = permissionlib.NewEngine(permissionlib.ModeDefault, &permissionlib.RuleSet{Rules: []permissionlib.Rule{{Tool: "fixture_tool", Behavior: permissionlib.DecisionAsk}}}, permissionlib.WithApprovalTimeout(time.Millisecond))
				}
			case "later stream error without usage":
				steps[0].events = terminationToolEvents("fixture_tool")
				steps = append(steps, characterizationProviderStep{events: []llmtypes.StreamEvent{{Type: llmtypes.EventError, Error: "original provider failure"}}})
				wantCalls, wantStatus = 2, "PARTIAL"
			case "budget refusal":
				wrapped := &terminationStore{Store: f.svc.store}
				f.svc.store = wrapped
				steps[0].events = terminationToolEvents("fixture_tool")
				steps[0].beforeReturn = func() { wrapped.smallBudget = true }
			case "tool panic":
				steps[0].events = terminationToolEvents("request_tools")
				wantPanic = "characterizationTools.HandleRequestTools: unexpected call"
			case "stream stall":
				hold := make(chan struct{})
				t.Cleanup(func() { close(hold) })
				steps[0].hold = hold
				f.svc.agents.(*characterizationAgents).agent.Constraints = `{"idle_timeout_seconds":1}`
			case "assistant save error":
				f.svc.store = &terminationStore{Store: f.svc.store, saveError: errors.New("original save failure")}
			}
			f.provider.steps = steps
			var events []chat.StreamEvent
			var recovered any
			func() { defer func() { recovered = recover() }(); events = f.runCtx(ctx, t, "assistant-termination") }()
			if wantPanic != "" {
				if recovered != wantPanic {
					t.Fatalf("panic=%v, want %s", recovered, wantPanic)
				}
			} else if recovered != nil {
				t.Fatalf("unexpected panic: %v", recovered)
			}
			if strings.HasPrefix(path, "stream error") {
				assertTerminationError(t, events, "original provider failure")
			}
			if path == "assistant save error" {
				assertTerminationError(t, events, "Failed to save response")
			}
			if path == "approval timeout then dispatch error" {
				assertApprovalTimedOut(t, events)
			}
			assertTerminationSnapshot(t, f, wantCalls, wantStatus, wantCost)
		})
	}
}

func assertTerminationError(t *testing.T, events []chat.StreamEvent, detail string) {
	t.Helper()
	for _, event := range events {
		encoded, _ := json.Marshal(event.StructuredError)
		if event.Type == "error" && strings.Contains(event.Error+string(encoded), detail) {
			return
		}
	}
	t.Fatalf("original error %q missing: %+v", detail, events)
}

func assertApprovalTimedOut(t *testing.T, events []chat.StreamEvent) {
	t.Helper()
	for _, event := range events {
		if event.Type == "tool_result" && strings.Contains(event.Summary, "approval timed out") {
			return
		}
	}
	t.Fatalf("did not exercise approval timeout: %+v", events)
}

// These paths already finalized before the fix. They must retain that behavior
// and never INSERT again when the turn-level fallback subsequently runs.
func TestFinalizedTurnPersistsUsageOnce(t *testing.T) {
	for _, path := range []string{"normal", "interrupted", "canceled closed stream", "approval timeout", "hard ceiling", "plugin blocks later call"} {
		t.Run(path, func(t *testing.T) {
			f := newCharacterizationFixture(t, nil, "fixture_tool")
			f.svc.modelCatalog, _ = usageCatalogFixture(t, "characterization", "characterization-model")
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			reason := "end_turn"
			if path == "interrupted" {
				reason = "interrupted"
			}
			steps := []characterizationProviderStep{{events: []llmtypes.StreamEvent{{Type: llmtypes.EventDelta, Content: "A finished answer."}, terminationUsage(reason)}}}
			calls := 1
			if path == "plugin blocks later call" {
				host := pluginpkg.NewHost(http.NewServeMux(), pluginpkg.NewLogger("termination"))
				t.Cleanup(func() { _ = host.Shutdown() })
				if err := host.RegisterEventHook([]string{pluginpkg.EventMessageSending}, &characterizationCancelHook{}); err != nil {
					t.Fatal(err)
				}
				steps[0].events = terminationToolEvents("fixture_tool")
				steps[0].beforeReturn = func() { f.svc.pluginHost = host }
			}
			if path == "hard ceiling" {
				f.svc.agents.(*characterizationAgents).agent.Constraints = `{"hard_ceiling":1}`
				steps[0].events = terminationToolEvents("fixture_tool")
			}
			if path == "canceled closed stream" {
				steps[0].beforeReturn = cancel
			}
			if path == "normal" || path == "approval timeout" {
				steps = append([]characterizationProviderStep{{events: terminationToolEvents("fixture_tool")}}, steps...)
				calls = 2
			}
			if path == "approval timeout" {
				f.svc.permissions = permissionlib.NewEngine(permissionlib.ModeDefault, &permissionlib.RuleSet{Rules: []permissionlib.Rule{{Tool: "fixture_tool", Behavior: permissionlib.DecisionAsk}}}, permissionlib.WithApprovalTimeout(time.Millisecond))
			}
			f.provider.steps = steps
			events := f.runCtx(ctx, t, "assistant-termination")
			if path == "approval timeout" {
				assertApprovalTimedOut(t, events)
			}
			assertTerminationSnapshot(t, f, calls, "COMPLETE", float64(calls)*.0044)
		})
	}
}

type terminationStore struct {
	Store
	saveError   error
	usageError  error
	attempts    int
	smallBudget bool
}

func (s *terminationStore) CreateMessage(ctx context.Context, msg *store.Message) error {
	if s.saveError != nil {
		return s.saveError
	}
	return s.Store.CreateMessage(ctx, msg)
}
func (s *terminationStore) RecordUsageSnapshot(ctx context.Context, sessionID, messageID, model string, input, output, tools, creation, read int, calls []ledger.Row) error {
	s.attempts++
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if s.usageError != nil {
		return s.usageError
	}
	return s.Store.RecordUsageSnapshot(ctx, sessionID, messageID, model, input, output, tools, creation, read, calls)
}

func TestTurnUsageWriteFailureIsBestEffort(t *testing.T) {
	for _, terminated := range []bool{false, true} {
		t.Run(map[bool]string{false: "finalized", true: "terminated"}[terminated], func(t *testing.T) {
			events := []llmtypes.StreamEvent{{Type: llmtypes.EventDelta, Content: "Started."}, terminationUsage("end_turn")}
			if terminated {
				events = append(events, llmtypes.StreamEvent{Type: llmtypes.EventError, Error: "original provider failure"})
			}
			f := newCharacterizationFixture(t, []characterizationProviderStep{{events: events}})
			wrapped := &terminationStore{Store: f.svc.store, usageError: errors.New("usage database unavailable")}
			f.svc.store = wrapped
			emitted := f.run(t, "assistant-termination")
			if wrapped.attempts != 1 {
				t.Fatalf("write attempts=%d, want one best-effort attempt", wrapped.attempts)
			}
			if terminated {
				assertTerminationError(t, emitted, "original provider failure")
			}
		})
	}
}

func (s *terminationStore) GetUserSettings(ctx context.Context) (*store.UserSettings, error) {
	settings, err := s.Store.GetUserSettings(ctx)
	if err == nil && settings != nil && s.smallBudget {
		settings.ContextWindowTokens = 10
	}
	return settings, err
}

func TestTurnWithoutProviderCallDoesNotPersistUsage(t *testing.T) {
	for _, path := range []string{"preparation failure", "dispatch failure", "plugin blocks first call"} {
		t.Run(path, func(t *testing.T) {
			f := newCharacterizationFixture(t, []characterizationProviderStep{{err: errors.New("dispatch failed")}})
			switch path {
			case "preparation failure":
				f.session = "missing-session"
			case "plugin blocks first call":
				host := pluginpkg.NewHost(http.NewServeMux(), pluginpkg.NewLogger("termination"))
				t.Cleanup(func() { _ = host.Shutdown() })
				if err := host.RegisterEventHook([]string{pluginpkg.EventMessageSending}, &characterizationCancelHook{}); err != nil {
					t.Fatal(err)
				}
				f.svc.pluginHost = host
			}
			f.run(t, "assistant-termination")
			var rows int
			if err := f.st.DB.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM token_usage WHERE message_id='assistant-termination'`).Scan(&rows); err != nil {
				t.Fatal(err)
			}
			if rows != 0 {
				t.Fatalf("fabricated usage row without an established provider call: %d", rows)
			}
		})
	}
}
