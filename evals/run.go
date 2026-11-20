package evals

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
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
// dollar cap stops a live run early rather than late. Costs reported with it
// are estimates, not bills.
var ConservativePrice = Price{Input: 15, Output: 75, CacheRead: 1.5, CacheWrite: 18.75}

func (p Price) usage(in, out, read, write int) float64 {
	return (float64(in)*p.Input + float64(out)*p.Output + float64(read)*p.CacheRead + float64(write)*p.CacheWrite) / 1e6
}

// Config says how to run the scenarios.
type Config struct {
	// Provider returns the model for one run. Nil runs offline, with each
	// scenario's script.
	Provider func(Scenario, Arm) (llmcontracts.Provider, error)
	// Model is written into each run's host.yaml so every run uses the same
	// model; empty uses ScriptedModel offline and is refused live.
	Model string
	// Gateway is passed to the agent for a live provider.
	Gateway agent.Gateway
	// Modes limits the arms run to these modes; empty runs every arm.
	Modes []Mode
	// Dir is where each run's agent directory is made; empty is a temporary
	// directory, removed afterwards.
	Dir string
	// TurnTokens caps the tokens one run's task turn may use, its subtasks
	// included (agent.Options.TurnTokenBudget). Zero is no cap.
	TurnTokens int
	// MaxUSD caps the whole run's estimated spend. Each model request first
	// reserves its most expensive outcome (its input plus max_tokens of
	// output); a request that would pass the cap is refused, which ends the
	// run in progress, and the remaining runs are skipped. Zero is no cap.
	MaxUSD float64
	// Price prices the usage; zero is ConservativePrice.
	Price Price
	// Timeout bounds one run. Zero is ten minutes.
	Timeout time.Duration
}

// Report is the outcome of Run.
type Report struct {
	Results []Result `json:"results"`
	// Model is the model the runs used: ScriptedModel offline.
	Model string `json:"model"`
	// Price is the price the costs were estimated with.
	Price Price `json:"price"`
	// SpentUSD is the whole run's estimated spend.
	SpentUSD float64 `json:"spent_usd"`
	// Stopped says why the run ended early (the dollar cap, an interrupt or an
	// error); empty when every run happened.
	Stopped string `json:"stopped,omitempty"`
}

// Result is one scenario run in one arm.
type Result struct {
	Scenario string `json:"scenario"`
	Arm      string `json:"arm"`
	Mode     Mode   `json:"mode"`
	// LastN and ResultCap are the arm's setting, when it varies one.
	LastN     int `json:"last_n,omitempty"`
	ResultCap int `json:"result_cap,omitempty"`
	// Success is the grader's verdict; Why says why a run failed. Invalid
	// marks a subtask run that never ran the variants the scenario requires:
	// it did not test what it was meant to.
	Success bool   `json:"success"`
	Invalid bool   `json:"invalid,omitempty"`
	Why     string `json:"why,omitempty"`
	// ParentContextTokens is what the root conversation's model requests
	// read during the task: input plus cache tokens across its turn.
	ParentContextTokens int `json:"parent_context_tokens"`
	// TotalTokens is the whole tree's use: the root turn's own tokens plus
	// every subtask's (trace usage: own plus subtasks[].usage).
	TotalTokens int `json:"total_tokens"`
	// CostUSD is TotalTokens priced with the report's price.
	CostUSD float64 `json:"cost_usd"`
	// WallMS is how long the task took.
	WallMS int64 `json:"wall_ms"`
	// Approvals is how many times the agent asked for approval, the
	// subtasks' relayed asks included.
	Approvals int `json:"approvals"`
	// ParentToolCalls counts the root turn's tool calls; Subtasks counts the
	// subtask conversations started anywhere in the tree, and Variants names
	// the variants they ran.
	ParentToolCalls int      `json:"parent_tool_calls"`
	Subtasks        int      `json:"subtasks"`
	Variants        []string `json:"variants,omitempty"`
	// Compactions counts the compaction passes in the tree.
	Compactions int `json:"compactions"`
	// Skipped says why the run did not happen.
	Skipped string `json:"skipped,omitempty"`
}

// Run runs each scenario in each of its arms, in order, and returns what ran
// even when it stops early.
//
// Runs happen one at a time: each changes the process's working directory to
// its copy of the workspace (the file tools resolve relative paths against
// it), so nothing else in the process may depend on the working directory
// while Run runs, and Run must not be called concurrently.
func Run(ctx context.Context, cfg Config, scenarios []Scenario) (Report, error) {
	if cfg.Price == (Price{}) {
		cfg.Price = ConservativePrice
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = 10 * time.Minute
	}
	model := cfg.Model
	if cfg.Provider == nil {
		model = ScriptedModel
	} else if model == "" {
		return Report{}, errors.New("evals: a live run needs a model")
	}
	rep := Report{Model: model, Price: cfg.Price}
	meter := &meter{price: cfg.Price, max: cfg.MaxUSD}
	defer func() { rep.SpentUSD = meter.spentUSD() }()
	for _, s := range scenarios {
		for _, arm := range s.AllArms() {
			if len(cfg.Modes) > 0 && !slices.Contains(cfg.Modes, arm.Mode) {
				continue
			}
			skip := ""
			switch {
			case ctx.Err() != nil:
				skip = "interrupted"
			case meter.over():
				skip = fmt.Sprintf("dollar cap $%.2f reached", cfg.MaxUSD)
			}
			if skip != "" {
				rep.Stopped = skip
				rep.Results = append(rep.Results, Result{Scenario: s.Name, Arm: arm.Name, Mode: arm.Mode, Skipped: skip})
				continue
			}
			r, err := runOne(ctx, cfg, model, meter, s, arm)
			rep.Results = append(rep.Results, r)
			if err != nil {
				rep.Stopped = "error: " + Shorten(err.Error())
				rep.SpentUSD = meter.spentUSD()
				return rep, fmt.Errorf("%s/%s: %w", s.Name, arm.Name, err)
			}
		}
	}
	rep.SpentUSD = meter.spentUSD()
	return rep, nil
}

func runOne(ctx context.Context, cfg Config, modelName string, meter *meter, s Scenario, arm Arm) (res Result, retErr error) {
	res = Result{Scenario: s.Name, Arm: arm.Name, Mode: arm.Mode, LastN: arm.LastN, ResultCap: arm.ResultCap}
	base := cfg.Dir
	if base == "" {
		tmp, err := os.MkdirTemp("", "nanite-eval-")
		if err != nil {
			return res, err
		}
		defer func() { retErr = errors.Join(retErr, os.RemoveAll(tmp)) }()
		base = tmp
	}
	dir, err := os.MkdirTemp(base, s.Name+"-"+string(arm.Mode)+"-")
	if err != nil {
		return res, err
	}
	// Resolve symlinks (macOS's temporary directory is one), so the working
	// directory and the agent directory are the same single workspace root.
	if dir, err = filepath.EvalSymlinks(dir); err != nil {
		return res, err
	}
	if err := s.install(dir); err != nil {
		return res, err
	}
	if err := setModel(dir, modelName); err != nil {
		return res, err
	}
	if arm.Prepare != nil {
		if err := arm.Prepare(dir); err != nil {
			return res, err
		}
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
		script = newScripted(s.Script(arm))
		script.dirs = []string{dir}
		model = script
	} else if model, err = cfg.Provider(s, arm); err != nil {
		return res, err
	}
	model = &metered{Provider: model, meter: meter, cancel: cancel}

	builtins := []agent.BuiltinGroup{agent.BuiltinFilesRead}
	if s.Writes {
		builtins = append(builtins, agent.BuiltinFilesWrite)
	}
	if arm.Mode == Subtask {
		builtins = append(builtins, agent.BuiltinSubtasks)
	}
	gate := &countingGate{}
	var mu sync.Mutex
	var records []agent.TraceRecord
	opts := agent.Options{
		Dir: dir, GlobalSkillsDir: "-", Builtins: builtins, Provider: model, Gateway: cfg.Gateway, Gate: gate,
		TurnTokenBudget: cfg.TurnTokens,
		TraceSink: func(_ context.Context, r agent.TraceRecord) {
			mu.Lock()
			records = append(records, r)
			mu.Unlock()
		},
	}
	if arm.Options != nil {
		arm.Options(&opts)
	}
	a, err := agent.Open(runCtx, opts)
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
	send := func(text string) (agent.Result, error) {
		run, err := conv.Send(runCtx, agent.Input{Text: text})
		if err != nil {
			return agent.Result{}, err
		}
		for ev := range run.Events() {
			if q, ok := ev.(agent.Question); ok {
				_ = run.Answer(q.ID, agent.Answer{Value: json.RawMessage("null")})
			}
		}
		return run.Wait()
	}
	var runErr error
	for _, msg := range s.Prelude() {
		if _, runErr = send(msg); runErr != nil {
			break
		}
	}
	mu.Lock()
	first := len(records)
	mu.Unlock()
	gate.n.Store(0)
	start := time.Now()
	var result agent.Result
	if runErr == nil {
		result, runErr = send(task)
	}
	res.WallMS = time.Since(start).Milliseconds()
	res.Approvals = int(gate.n.Load())

	mu.Lock()
	recs := append([]agent.TraceRecord(nil), records[first:]...)
	mu.Unlock()
	res.fill(recs, conv.ID(), meter.price)

	switch {
	case meter.capped():
		res.Why = fmt.Sprintf("stopped: dollar cap $%.2f reached", cfg.MaxUSD)
	case runErr != nil:
		res.Why = "run failed: " + Shorten(runErr.Error())
	default:
		res.Success, res.Why = s.Grade(result.Text, dir)
	}
	if script != nil {
		if f := script.failures(); len(f) > 0 {
			res.Success, res.Why = false, f[0]
		}
	}
	if arm.Mode == Subtask {
		for _, v := range s.Requires {
			if !slices.Contains(res.Variants, v) {
				res.Success, res.Invalid = false, true
				res.Why = fmt.Sprintf("invalid: the %s variant did not run", v)
				break
			}
		}
	}
	return res, nil
}

// fill takes the metrics from the task turn's trace records.
func (r *Result) fill(recs []agent.TraceRecord, root string, price Price) {
	children := map[string]bool{}
	variants := map[string]bool{}
	for _, rec := range recs {
		r.Compactions += len(rec.Compactions)
		if rec.ParentConversationID != "" {
			children[rec.ConversationID] = true
			if rec.Role != "" {
				variants[rec.Role] = true
			}
			continue
		}
		if rec.ConversationID != root {
			continue
		}
		u := rec.Usage
		r.ParentContextTokens += u.InputTokens + u.CacheReadTokens + u.CacheWriteTokens
		r.TotalTokens += u.InputTokens + u.OutputTokens + u.CacheReadTokens + u.CacheWriteTokens
		r.CostUSD += price.usage(u.InputTokens, u.OutputTokens, u.CacheReadTokens, u.CacheWriteTokens)
		r.ParentToolCalls += len(rec.ToolCalls)
		for _, st := range rec.Subtasks {
			variants[st.Variant] = true
			// A background subtask still running when the turn ended is
			// listed with no usage (its tokens are in its own records, which
			// may come later). The scenarios start none, and a report that
			// added them would depend on timing, so the harness leaves
			// background subtasks out of its totals.
			if st.Background {
				continue
			}
			su := st.Usage
			r.TotalTokens += su.InputTokens + su.OutputTokens + su.CacheReadTokens + su.CacheWriteTokens
			r.CostUSD += price.usage(su.InputTokens, su.OutputTokens, su.CacheReadTokens, su.CacheWriteTokens)
		}
	}
	r.Subtasks = len(children)
	for v := range variants {
		r.Variants = append(r.Variants, v)
	}
	slices.Sort(r.Variants)
}

// setModel writes the model into the run's host.yaml, so each run uses the
// model asked for whatever the environment's default.
func setModel(dir, model string) error {
	p := filepath.Join(dir, "host.yaml")
	data, err := os.ReadFile(p) //nolint:gosec // the run's own copy of the agent directory
	if err != nil {
		return err
	}
	text := string(data)
	if !strings.Contains(text, "\nruntime:\n") {
		return fmt.Errorf("evals: %s has no runtime section", p)
	}
	text = regexp.MustCompile(`(?m)^  model: .*\n`).ReplaceAllString(text, "")
	text = strings.Replace(text, "\nruntime:\n", "\nruntime:\n  model: "+model+"\n", 1)
	return os.WriteFile(p, []byte(text), 0o600) //nolint:gosec // the run's own copy of the agent directory
}

var secretish = regexp.MustCompile(`(?i)(sk-[a-z0-9_-]{6,}|bearer\s+\S+|key[=:]\s*\S+|token[=:]\s*\S+)`)

// Shorten makes error text fit for a report: anything that looks like a
// credential is removed and the text is cut to 300 characters.
func Shorten(s string) string {
	s = secretish.ReplaceAllString(s, "[redacted]")
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > 300 {
		s = s[:297] + "..."
	}
	return s
}

// countingGate allows every approval request and counts them.
type countingGate struct{ n atomic.Int64 }

func (g *countingGate) Decide(ctx context.Context, req agent.PermissionRequest) (agent.PermissionDecision, error) {
	g.n.Add(1)
	return agent.AllowGate.Decide(ctx, req)
}

// meter keeps the estimated spend. A request reserves its most expensive
// outcome before it is sent and settles to its reported usage when that
// arrives, so concurrent requests cannot together pass the cap.
type meter struct {
	price    Price
	max      float64
	mu       sync.Mutex
	spent    float64
	reserved float64
	refused  bool
}

// reserve holds cost against the cap; false means the request would pass it.
func (m *meter) reserve(cost float64) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.max > 0 && m.spent+m.reserved+cost > m.max {
		m.refused = true
		return false
	}
	m.reserved += cost
	return true
}

// settle releases a reservation and records what the request cost.
func (m *meter) settle(reserved, cost float64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.reserved -= reserved
	m.spent += cost
}

// over reports whether no further request can start.
func (m *meter) over() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.refused || (m.max > 0 && m.spent >= m.max)
}

// capped reports whether the cap refused a request.
func (m *meter) capped() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.refused
}

func (m *meter) spentUSD() float64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.spent
}

// worst is a request's most expensive outcome: its input (estimated from its
// size) and its whole output allowance.
func (m *meter) worst(inputTokens, maxTokens int) float64 {
	if maxTokens <= 0 {
		maxTokens = 8192
	}
	return m.price.usage(inputTokens, maxTokens, 0, 0)
}

var errCapped = errors.New("evals: dollar cap reached")

// metered prices every model request against the meter and ends the run when
// the cap would be passed: the request is refused and the run cancelled.
type metered struct {
	llmcontracts.Provider
	meter  *meter
	cancel context.CancelFunc
}

func (p *metered) StreamChat(ctx context.Context, req llmtypes.ChatRequest) (<-chan llmtypes.StreamEvent, error) {
	hold := p.meter.worst(requestTokens(req, nil), req.MaxTokens)
	if !p.meter.reserve(hold) {
		p.cancel()
		return nil, errCapped
	}
	in, err := p.Provider.StreamChat(ctx, req)
	if err != nil {
		p.meter.settle(hold, 0)
		return nil, err
	}
	out := make(chan llmtypes.StreamEvent)
	go func() {
		defer close(out)
		// The reservation holds until the stream ends, then settles to the
		// usage reported, or stays as the cost when none was.
		var cost float64
		reported := false
		defer func() {
			if !reported {
				cost = hold
			}
			p.meter.settle(hold, cost)
		}()
		for ev := range in {
			if ev.Type == llmtypes.EventUsage && ev.Usage != nil {
				u := ev.Usage
				reported = true
				cost += p.meter.price.usage(u.InputTokens, u.OutputTokens, u.CacheReadTokens, u.CacheCreationTokens)
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

// Complete serves compaction summaries and argument repair. It reports no
// usage, so it is charged an estimate of four characters a token.
func (p *metered) Complete(ctx context.Context, req llmtypes.ChatRequest) (string, error) {
	in := requestTokens(req, nil)
	hold := p.meter.worst(in, req.MaxTokens)
	if !p.meter.reserve(hold) {
		p.cancel()
		return "", errCapped
	}
	text, err := p.Provider.Complete(ctx, req)
	p.meter.settle(hold, p.meter.price.usage(in, len(text)/4+1, 0, 0))
	return text, err
}
