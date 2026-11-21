// Package deskpilot contains an opt-in live front desk acceptance test.
package deskpilot

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	chat "github.com/hollis-labs/libs/agent/chat"
	"github.com/hollis-labs/libs/agent/desk"
	"github.com/hollis-labs/libs/llm/core/llmcontracts"
	"github.com/hollis-labs/libs/llm/core/llmtypes"
	"github.com/hollis-labs/libs/security/redact"
	chatstream "github.com/hollis-labs/libs/ui/ui-go/chatstream"
	"github.com/hollis-labs/nanite/agent"
	"github.com/hollis-labs/nanite/agent/authored"
	"github.com/hollis-labs/nanite/agent/providers"
	"github.com/hollis-labs/nanite/chatrunner"
)

//go:embed agentdir
var files embed.FS

const pilotChat = "desk-pilot"

// The live provider still performs every model call. The restart barrier stops
// only a request following a real tool result, before it reaches the gateway.
// This avoids racing the model's speed and never substitutes a scripted answer.
type observed struct {
	llmcontracts.Provider
	mu             sync.Mutex
	usage          llmtypes.Usage
	calls, missing int
	tools          []string
	barrier        chan struct{}
	stopped        bool
}

func (p *observed) StreamChat(ctx context.Context, req llmtypes.ChatRequest) (<-chan llmtypes.StreamEvent, error) {
	hasResult := slices.ContainsFunc(req.Messages, func(m llmtypes.ChatMessage) bool {
		return slices.ContainsFunc(m.ContentBlocks, func(b llmtypes.ContentBlock) bool { return b.Type == "tool_result" && !b.IsError })
	})
	p.mu.Lock()
	subtask := slices.ContainsFunc(req.Tools, func(tool llmtypes.ToolDefinition) bool { return tool.Name == "return_result" })
	pause := !p.stopped && subtask && hasResult
	if pause {
		p.stopped = true
		close(p.barrier)
	}
	p.mu.Unlock()
	if pause {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	in, err := p.Provider.StreamChat(ctx, req)
	if err != nil {
		return nil, err
	}
	p.mu.Lock()
	p.calls++
	p.mu.Unlock()
	out := make(chan llmtypes.StreamEvent)
	go func() {
		defer close(out)
		sawUsage := false
		defer func() {
			if !sawUsage {
				p.mu.Lock()
				p.missing++
				p.mu.Unlock()
			}
		}()
		for ev := range in {
			p.mu.Lock()
			if ev.Usage != nil {
				sawUsage = true
				p.usage.InputTokens += ev.Usage.InputTokens
				p.usage.OutputTokens += ev.Usage.OutputTokens
				p.usage.CacheReadTokens += ev.Usage.CacheReadTokens
				p.usage.CacheCreationTokens += ev.Usage.CacheCreationTokens
				p.usage.CostUSD += ev.Usage.CostUSD
			}
			if ev.ToolUse != nil {
				p.tools = append(p.tools, ev.ToolUse.Name)
			}
			p.mu.Unlock()
			select {
			case out <- ev:
			case <-ctx.Done():
			}
		}
	}()
	return out, nil
}

// Complete uses the same streaming wire so desk planning usage is measured too.
func (p *observed) Complete(ctx context.Context, req llmtypes.ChatRequest) (string, error) {
	events, err := p.StreamChat(ctx, req)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	for ev := range events {
		if ev.Type == llmtypes.EventError {
			return "", errors.New(ev.Error)
		}
		if ev.Type == llmtypes.EventDelta {
			b.WriteString(ev.Content)
		}
	}
	return b.String(), ctx.Err()
}

type recorder struct {
	mu              sync.Mutex
	plans           []desk.Result
	notices         []string
	active, overlap bool
}

func (h *recorder) observe(_ context.Context, _ string, _ chat.Turn, run chat.Run) error {
	h.mu.Lock()
	if h.active {
		h.overlap = true
	}
	h.active = true
	h.mu.Unlock()
	defer func() { h.mu.Lock(); h.active = false; h.mu.Unlock() }()
	_, err := h.consume(run)
	return err
}

func (h *recorder) consume(run chat.Run) (string, error) {
	var b strings.Builder
	for ev, err := range run.Events() {
		if err != nil {
			return b.String(), err
		}
		if ev.Verb == chatstream.VerbRunError {
			return b.String(), fmt.Errorf("run failed: %s", ev.Message)
		}
		if ev.Verb == chatstream.VerbPartDelta {
			b.WriteString(ev.Text)
		}
		if ev.Verb == chatstream.VerbActivity && ev.Kind == chatrunner.ActivityNotice {
			raw, err := json.Marshal(ev.Value)
			if err != nil {
				return "", err
			}
			var v struct{ Kind, Body string }
			if err := json.Unmarshal(raw, &v); err != nil {
				return "", err
			}
			if v.Kind == chatrunner.NoticeKindDesk {
				h.mu.Lock()
				h.notices = append(h.notices, v.Body)
				h.mu.Unlock()
			}
		}
	}
	return b.String(), nil
}

func TestLiveDesk(t *testing.T) {
	if os.Getenv("NANITE_DESK_LIVE") != "1" {
		t.Skip("set NANITE_DESK_LIVE=1 to spend real model tokens")
	}
	required := func(key string) string {
		t.Helper()
		v := os.Getenv(key)
		if v == "" {
			t.Fatalf("%s is required", key)
		}
		return v
	}
	identityPath := required("NANITE_DESK_IDENTITY")
	gatewayURL := required("NANITE_DESK_GATEWAY_URL")
	model := required("NANITE_DESK_MODEL")
	wire := required("NANITE_DESK_WIRE")
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Minute)
	defer cancel()
	dir := t.TempDir()
	err := fs.WalkDir(files, "agentdir", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel("agentdir", path)
		dst := filepath.Join(dir, rel)
		if d.IsDir() {
			return os.MkdirAll(dst, 0700)
		}
		data, err := files.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(dst, data, 0600)
	})
	if err != nil {
		t.Fatal(err)
	}
	identity, err := os.ReadFile(identityPath) // #nosec G304 -- operator-selected identity configuration, never a model-supplied path.
	if err != nil {
		t.Fatal(err)
	}
	id, err := authored.ParseIdentity(identity, dir)
	if err != nil {
		t.Fatal(err)
	}
	// Validate the identity here, but keep it outside the agent's readable files.
	scope := redact.NewScope()
	warden, err := providers.NewWarden(dir, id, providers.WardenOptions{Redactor: scope, OnAuthWaiting: func(w agent.AuthWaiting) { t.Logf("Warden grant required: %+v", w) }})
	if err != nil {
		t.Fatal(err)
	}
	live, err := providers.NewNamed(authored.ProviderSpec{Name: "pilot", Wire: wire, BaseURL: gatewayURL, Auth: authored.AuthWarden, Audience: "llm-gateway", Scopes: []string{"llm-gateway.chat"}}, providers.Options{Warden: warden, Redactor: scope})
	if err != nil {
		t.Fatal(err)
	}
	profile, err := files.ReadFile("agentdir/host.yaml")
	if err != nil {
		t.Fatal(err)
	}
	quoted, _ := json.Marshal(model)
	profile = []byte(strings.ReplaceAll(string(profile), "configured-by-live-test", string(quoted)))
	if err := os.WriteFile(filepath.Join(dir, "host.yaml"), profile, 0600); err != nil { // #nosec G703 -- fixed filename in t.TempDir, model is JSON-quoted YAML data.
		t.Fatal(err)
	}
	p := &observed{Provider: live, barrier: make(chan struct{})}
	h := &recorder{}
	defer func() {
		h.mu.Lock()
		for i, plan := range h.plans {
			raw, _ := json.Marshal(plan)
			t.Logf("Plan %d: %s", i, raw)
		}
		for i, n := range h.notices {
			t.Logf("Delivery %d: %s", i+1, n)
		}
		h.mu.Unlock()
		p.mu.Lock()
		defer p.mu.Unlock()
		estimate := (float64(p.usage.InputTokens)*15 + float64(p.usage.OutputTokens)*75 + float64(p.usage.CacheReadTokens)*1.5 + float64(p.usage.CacheCreationTokens)*18.75) / 1e6
		t.Logf("Usage: %+v; calls=%d missing_usage=%d; conservative estimated USD=%.6f (15/75/1.5/18.75 per million input/output/cache-read/cache-write); tools=%v", p.usage, p.calls, p.missing, estimate, p.tools)
	}()
	var current *chatrunner.Runner
	defer func() {
		if current != nil {
			_ = current.Close()
		}
	}()
	policy := desk.DefaultPolicy()
	policy.MergeWindow = 100 * time.Millisecond
	open := func() *chatrunner.Runner {
		t.Helper()
		r, err := chatrunner.Open(ctx, chatrunner.Options{
			Agent:   agent.Options{Dir: dir, WorkDir: dir, Provider: p, GlobalSkillsDir: "-", Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Builtins: []agent.BuiltinGroup{agent.BuiltinFilesRead, agent.BuiltinSubtasks, agent.BuiltinBackground}},
			Observe: h.observe, ResumeInterrupted: true,
			Desk: &chatrunner.DeskOptions{Intake: desk.Intake{Mode: desk.IntakeAuto}, PlanWithModel: true, IntakeTimeout: 60 * time.Second, Policy: &policy,
				PlannerInstructions: "Each numbered request is one independent research item. Keep exact result markers and file paths in each goal. Do not add a synthesis or answer item. These local read-only tasks need no owner clarification.",
				OnPlan:              func(_ string, r desk.Result) { h.mu.Lock(); h.plans = append(h.plans, r); h.mu.Unlock() },
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	send := func(text string) string {
		t.Helper()
		run, err := current.Start(ctx, chat.Turn{ConversationID: pilotChat, Text: text})
		if err != nil {
			t.Fatal(err)
		}
		answer, err := h.consume(run)
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("User: %s\nDesk: %s", text, answer)
		return answer
	}
	current = open()
	answer := send("What is 2 + 2? Reply with just 4.")
	if strings.TrimSpace(answer) != "4" {
		t.Fatalf("inline answer: %q", answer)
	}
	send("Research these two independent tasks in the background:\n1. Read fixtures/numbers.txt and sum Batch A. Return PILOT_SUM=<sum> with the source.\n2. Read fixtures/observations.txt and count red observations for Cedar. Return PILOT_RED=<count> with the source.")
	h.mu.Lock()
	firstPlan := h.plans[len(h.plans)-1]
	h.mu.Unlock()
	if firstPlan.Err != "" || firstPlan.Plan.Source != "model" || firstPlan.Disposition != desk.Dispatch || len(firstPlan.Plan.Items) != 2 {
		t.Fatal("first live background plan did not dispatch two items")
	}
	send("Research these two further independent tasks in the background:\n1. Read fixtures/numbers.txt and multiply Batch B. Return PILOT_PRODUCT=<product> with the source.\n2. Read fixtures/observations.txt and count distinct Maple directions. Return PILOT_DIRECTIONS=<count> with the source.")
	h.mu.Lock()
	secondPlan := h.plans[len(h.plans)-1]
	h.mu.Unlock()
	if secondPlan.Err != "" || secondPlan.Plan.Source != "model" || secondPlan.Disposition != desk.Dispatch || len(secondPlan.Plan.Items) != 2 {
		t.Fatal("second live background plan did not dispatch two items")
	}
	select {
	case <-p.barrier:
	case <-ctx.Done():
		t.Fatal("no real tool-result restart checkpoint", ctx.Err())
	}
	running := 0
	dispatched := map[string]bool{}
	for _, topic := range current.Desk().Ledger().List(nil) {
		for _, ref := range topic.Refs {
			if ref.Kind == "job" {
				dispatched[ref.ID] = true
			}
		}
		for _, work := range topic.Work {
			if work.State == desk.WorkRunning {
				running++
			}
		}
	}
	if len(dispatched) != 4 {
		t.Fatalf("expected four background job refs before restart, got %v", dispatched)
	}
	if running == 0 {
		t.Fatal("restart checkpoint had no running desk job")
	}
	t.Logf("Restarting with %d running jobs after a live tool call; dispatched=%v", running, dispatched)
	if err := current.Close(); err != nil {
		t.Fatal(err)
	}
	current = nil
	current = open()
	recovered, err := current.RecoverTurns(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := current.Desk().Recover(ctx); err != nil {
		t.Fatal(err)
	}
	t.Logf("RecoverTurns handled %v; desk recovery replayed", recovered)
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		total, done := 0, 0
		for _, topic := range current.Desk().Ledger().List(nil) {
			for _, work := range topic.Work {
				total++
				if work.State == desk.WorkDone {
					done++
				}
			}
		}
		h.mu.Lock()
		all := strings.Join(h.notices, "\n")
		h.mu.Unlock()
		if total == 4 && done == 4 && strings.Contains(all, "PILOT_SUM=80") && strings.Contains(all, "PILOT_RED=3") && strings.Contains(all, "PILOT_PRODUCT=105") && strings.Contains(all, "PILOT_DIRECTIONS=3") {
			break
		}
		select {
		case <-ticker.C:
		case <-ctx.Done():
			t.Fatalf("work did not settle (%d/%d): %s", done, total, all)
		}
	}
	// Recovery is idempotent after completion, including on a third Open.
	if err := current.Close(); err != nil {
		t.Fatal(err)
	}
	current = nil
	current = open()
	if _, err := current.RecoverTurns(ctx); err != nil {
		t.Fatal(err)
	}
	if err := current.Desk().Recover(ctx); err != nil {
		t.Fatal(err)
	}
	if err := current.Close(); err != nil {
		t.Fatal(err)
	}
	current = nil
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.overlap {
		t.Error("per-chat delivery observers overlapped")
	}
	if len(h.plans) != 3 {
		t.Errorf("got %d intake plans, want 3", len(h.plans))
	}
	for i, r := range h.plans {
		if i == 0 {
			if r.Disposition != desk.Chat {
				t.Error("quick ask was not inline")
			}
			continue
		}
		if r.Err != "" || r.Plan.Source != "model" || r.Disposition != desk.Dispatch || len(r.Plan.Items) != 2 {
			t.Errorf("background plan %d failed acceptance", i)
		}
	}
	all := strings.Join(h.notices, "\n")
	for _, marker := range []string{"PILOT_SUM=80", "PILOT_RED=3", "PILOT_PRODUCT=105", "PILOT_DIRECTIONS=3"} {
		if n := strings.Count(all, marker); n != 1 {
			t.Errorf("%s delivered %d times", marker, n)
		}
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if !slices.Contains(p.tools, "read_file") || !slices.Contains(p.tools, "return_result") {
		t.Error("workers did not use the required builtins")
	}
	if p.missing > 0 {
		t.Error("gateway omitted usage; token/cost report is incomplete")
	}
}
