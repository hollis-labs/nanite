package evals

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/hollis-labs/libs/llm/core/llmcontracts"
	"github.com/hollis-labs/libs/llm/core/llmtypes"
	"github.com/hollis-labs/nanite/agent"
)

// Price is a model's price in US dollars per million tokens.
type Price struct {
	Input, Output, CacheRead, CacheWrite float64
}

// ConservativePrice is used when no price is given: high enough that the
// dollar cap stops a live run early rather than late. Reported costs made
// with it are estimates, not bills.
var ConservativePrice = Price{Input: 15, Output: 75, CacheRead: 1.5, CacheWrite: 18.75}

func (p Price) cost(u agent.TraceRecord) float64 {
	return p.usage(u.Usage.InputTokens, u.Usage.OutputTokens, u.Usage.CacheReadTokens, u.Usage.CacheWriteTokens)
}

func (p Price) usage(in, out, read, write int) float64 {
	return (float64(in)*p.Input + float64(out)*p.Output + float64(read)*p.CacheRead + float64(write)*p.CacheWrite) / 1e6
}

// Config says how to run the scenarios.
type Config struct {
	// Provider returns the model for one run. Nil runs offline, with each
	// scenario's script.
	Provider func(Scenario, Mode) (llmcontracts.Provider, error)
	// Gateway is passed to the agent for a live provider.
	Gateway agent.Gateway
	// Dir is where each run's agent directory is made; empty is a temporary
	// directory, removed afterwards.
	Dir string
	// TurnTokens caps the tokens one run may use, its subtasks included
	// (agent.Options.TurnTokenBudget). Zero is no cap.
	TurnTokens int
	// MaxUSD stops the whole run once its estimated spend reaches it: the run
	// in progress is cancelled and the rest are skipped. Zero is no cap.
	MaxUSD float64
	// Price prices the usage; zero is ConservativePrice.
	Price Price
	// Timeout bounds one run. Zero is ten minutes.
	Timeout time.Duration
}

// Result is one scenario run in one mode.
type Result struct {
	Scenario string `json:"scenario"`
	Mode     Mode   `json:"mode"`
	// Success is the grader's verdict; Why says why a run failed.
	Success bool   `json:"success"`
	Why     string `json:"why,omitempty"`
	// ParentContextTokens is what the root conversation's model requests
	// read: input plus cache tokens across its turn. It is the number Code
	// Mode is meant to shrink.
	ParentContextTokens int `json:"parent_context_tokens"`
	// TotalTokens is the whole tree's use: the root turn's own tokens plus
	// every subtask's (trace usage: own plus subtasks[].usage).
	TotalTokens int `json:"total_tokens"`
	// CostUSD is TotalTokens priced with Config.Price.
	CostUSD float64 `json:"cost_usd"`
	// WallMS is how long the run took.
	WallMS int64 `json:"wall_ms"`
	// Approvals is how many times the agent asked for approval, the
	// subtasks' relayed asks included.
	Approvals int `json:"approvals"`
	// ParentToolCalls counts the root turn's tool calls; Subtasks counts the
	// subtask conversations started anywhere in the tree.
	ParentToolCalls int `json:"parent_tool_calls"`
	Subtasks        int `json:"subtasks"`
	// Model is the model the root turn used.
	Model string `json:"model,omitempty"`
	// Skipped says why the run did not happen (the dollar cap).
	Skipped string `json:"skipped,omitempty"`
}

// Run runs each scenario in each mode, in order.
func Run(ctx context.Context, cfg Config, scenarios []Scenario, modes []Mode) ([]Result, error) {
	if cfg.Price == (Price{}) {
		cfg.Price = ConservativePrice
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = 10 * time.Minute
	}
	meter := &meter{price: cfg.Price, max: cfg.MaxUSD}
	var out []Result
	for _, s := range scenarios {
		for _, m := range modes {
			if meter.over() {
				out = append(out, Result{Scenario: s.Name, Mode: m, Skipped: fmt.Sprintf("dollar cap $%.2f reached", cfg.MaxUSD)})
				continue
			}
			r, err := runOne(ctx, cfg, meter, s, m)
			if err != nil {
				return out, fmt.Errorf("%s/%s: %w", s.Name, m, err)
			}
			out = append(out, r)
		}
	}
	return out, nil
}

func runOne(ctx context.Context, cfg Config, meter *meter, s Scenario, m Mode) (res Result, retErr error) {
	res = Result{Scenario: s.Name, Mode: m}
	base := cfg.Dir
	if base == "" {
		tmp, err := os.MkdirTemp("", "nanite-eval-")
		if err != nil {
			return res, err
		}
		defer func() { retErr = errors.Join(retErr, os.RemoveAll(tmp)) }()
		base = tmp
	}
	dir, err := os.MkdirTemp(base, s.Name+"-"+string(m)+"-")
	if err != nil {
		return res, err
	}
	if err := s.install(dir); err != nil {
		return res, err
	}
	// File tools resolve relative paths against the working directory first,
	// so the run works inside its own copy of the workspace.
	prev, err := os.Getwd()
	if err != nil {
		return res, err
	}
	if err := os.Chdir(dir); err != nil {
		return res, err
	}
	defer func() { retErr = errors.Join(retErr, os.Chdir(prev)) }()

	runCtx, cancel := context.WithTimeout(ctx, cfg.Timeout)
	defer cancel()
	var model llmcontracts.Provider
	var script *scripted
	if cfg.Provider == nil {
		script = newScripted(s.Script(m))
		script.dirs = []string{dir}
		if resolved, err := filepath.EvalSymlinks(dir); err == nil && resolved != dir {
			script.dirs = append([]string{resolved}, script.dirs...)
		}
		model = script
	} else if model, err = cfg.Provider(s, m); err != nil {
		return res, err
	}
	model = &metered{Provider: model, meter: meter, cancel: cancel}

	builtins := []agent.BuiltinGroup{agent.BuiltinFilesRead}
	if s.Writes {
		builtins = append(builtins, agent.BuiltinFilesWrite)
	}
	if m == Subtask {
		builtins = append(builtins, agent.BuiltinSubtasks)
	}
	gate := &countingGate{}
	var mu sync.Mutex
	var records []agent.TraceRecord
	a, err := agent.Open(runCtx, agent.Options{
		Dir: dir, GlobalSkillsDir: "-", Builtins: builtins, Provider: model, Gateway: cfg.Gateway, Gate: gate,
		TurnTokenBudget: cfg.TurnTokens,
		TraceSink: func(_ context.Context, r agent.TraceRecord) {
			mu.Lock()
			records = append(records, r)
			mu.Unlock()
		},
	})
	if err != nil {
		return res, err
	}
	defer func() { retErr = errors.Join(retErr, a.Close()) }()
	conv, err := a.NewConversation(runCtx)
	if err != nil {
		return res, err
	}
	task, err := s.Task()
	if err != nil {
		return res, err
	}
	start := time.Now()
	run, err := conv.Send(runCtx, agent.Input{Text: task})
	if err != nil {
		return res, err
	}
	for ev := range run.Events() {
		if q, ok := ev.(agent.Question); ok {
			_ = run.Answer(q.ID, agent.Answer{Value: json.RawMessage("null")})
		}
	}
	result, runErr := run.Wait()
	res.WallMS = time.Since(start).Milliseconds()
	res.Approvals = int(gate.n.Load())

	mu.Lock()
	recs := append([]agent.TraceRecord(nil), records...)
	mu.Unlock()
	res.fill(recs, conv.ID(), cfg.Price)

	switch {
	case meter.over():
		res.Why = fmt.Sprintf("stopped: dollar cap $%.2f reached", cfg.MaxUSD)
	case runErr != nil:
		res.Why = "run failed: " + runErr.Error()
	default:
		res.Success, res.Why = s.Grade(result.Text, dir)
	}
	if script != nil {
		if f := script.failures(); len(f) > 0 {
			res.Success, res.Why = false, f[0]
		}
	}
	return res, nil
}

// fill takes the metrics from the run's trace records.
func (r *Result) fill(recs []agent.TraceRecord, root string, price Price) {
	children := map[string]bool{}
	for _, rec := range recs {
		if rec.ParentConversationID != "" {
			children[rec.ConversationID] = true
			continue
		}
		if rec.ConversationID != root {
			continue
		}
		u := rec.Usage
		r.ParentContextTokens += u.InputTokens + u.CacheReadTokens + u.CacheWriteTokens
		r.TotalTokens += u.InputTokens + u.OutputTokens + u.CacheReadTokens + u.CacheWriteTokens
		r.CostUSD += price.cost(rec)
		r.ParentToolCalls += len(rec.ToolCalls)
		r.Model = rec.Model
		for _, st := range rec.Subtasks {
			su := st.Usage
			r.TotalTokens += su.InputTokens + su.OutputTokens + su.CacheReadTokens + su.CacheWriteTokens
			r.CostUSD += price.usage(su.InputTokens, su.OutputTokens, su.CacheReadTokens, su.CacheWriteTokens)
		}
	}
	r.Subtasks = len(children)
}

// countingGate allows every approval request and counts them.
type countingGate struct{ n atomic.Int64 }

func (g *countingGate) Decide(ctx context.Context, req agent.PermissionRequest) (agent.PermissionDecision, error) {
	g.n.Add(1)
	return agent.AllowGate.Decide(ctx, req)
}

// meter adds up the estimated spend from the model's usage reports.
type meter struct {
	price Price
	max   float64
	mu    sync.Mutex
	spent float64
}

func (m *meter) add(u *llmtypes.Usage) float64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.spent += m.price.usage(u.InputTokens, u.OutputTokens, u.CacheReadTokens, u.CacheCreationTokens)
	return m.spent
}

func (m *meter) over() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.max > 0 && m.spent >= m.max
}

// Spent is the estimated spend so far.
func (m *meter) Spent() float64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.spent
}

// metered counts every response's usage and cancels the run once the dollar
// cap is reached, so a live run never runs past it by more than one response.
type metered struct {
	llmcontracts.Provider
	meter  *meter
	cancel context.CancelFunc
}

func (p *metered) StreamChat(ctx context.Context, req llmtypes.ChatRequest) (<-chan llmtypes.StreamEvent, error) {
	if p.meter.over() {
		p.cancel()
		return nil, errors.New("evals: dollar cap reached")
	}
	in, err := p.Provider.StreamChat(ctx, req)
	if err != nil {
		return nil, err
	}
	out := make(chan llmtypes.StreamEvent)
	go func() {
		defer close(out)
		for ev := range in {
			if ev.Type == llmtypes.EventUsage && ev.Usage != nil {
				p.meter.add(ev.Usage)
				if p.meter.over() {
					p.cancel()
				}
			}
			select {
			case out <- ev:
			case <-ctx.Done():
				for range in {
				}
				return
			}
		}
	}()
	return out, nil
}
