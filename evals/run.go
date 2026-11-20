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
	"github.com/hollis-labs/nanite/internal/slots"
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
	// Dir is the base directory each run's agent directory and workspace
	// copy are made in; empty is a temporary directory per run, removed
	// afterwards. A run works only inside its own copy (agent.Options.WorkDir)
	// and never changes the process's working directory.
	Dir string
	// Parallel is how many runs may happen at once; zero is one. Results
	// keep the scenario and arm order whatever finishes first.
	Parallel int
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
	// SubtaskTools names the tools the subtasks called; ParentFetches counts
	// the root's fetch_tool_result calls. They show whether a run exercised
	// what its arm varies.
	SubtaskTools  []string `json:"subtask_tools,omitempty"`
	ParentFetches int      `json:"parent_fetches"`
	// StubCalls counts the calls to each of the scenario's stub tools,
	// subtasks' calls included.
	StubCalls map[string]int `json:"stub_calls,omitempty"`
	// Skipped says why the run did not happen.
	Skipped string `json:"skipped,omitempty"`
	// cancelled is set when the run ended with Run's context already
	// cancelled (by another run's failure or an interrupt).
	cancelled bool
}

// Run runs each scenario in each of its arms and returns what ran, in
// scenario and arm order, even when it stops early. Up to cfg.Parallel runs
// happen at once. Run is safe to call concurrently: no run depends on or
// changes the process's working directory.
func Run(ctx context.Context, cfg Config, scenarios []Scenario) (Report, error) {
	if cfg.Price == (Price{}) {
		cfg.Price = ConservativePrice
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = 10 * time.Minute
	}
	if cfg.Parallel < 0 {
		return Report{}, errors.New("evals: Parallel must not be negative")
	}
	if cfg.Parallel == 0 {
		cfg.Parallel = 1
	}
	model := cfg.Model
	if cfg.Provider == nil {
		model = ScriptedModel
	} else if model == "" {
		return Report{}, errors.New("evals: a live run needs a model")
	}
	rep := Report{Model: model, Price: cfg.Price}
	meter := newMeter(cfg.Price, cfg.MaxUSD)

	type job struct {
		s   Scenario
		arm Arm
	}
	var jobs []job
	for _, s := range scenarios {
		if err := checkStubs(s.Stubs); err != nil {
			return Report{}, fmt.Errorf("%s: %w", s.Name, err)
		}
	}
	for _, s := range scenarios {
		for _, arm := range s.AllArms() {
			if len(cfg.Modes) > 0 && !slices.Contains(cfg.Modes, arm.Mode) {
				continue
			}
			jobs = append(jobs, job{s, arm})
		}
	}
	results := make([]Result, len(jobs))
	errs := make([]error, len(jobs))
	var (
		mu sync.Mutex
		wg sync.WaitGroup
		// failed is the index of the run that failed, or -1.
		failed = -1
	)
	// A failed run stops the others: none starts after it, and those in
	// flight are cancelled.
	runCtx, stopAll := context.WithCancel(ctx)
	defer stopAll()
	sem := make(chan struct{}, cfg.Parallel)
	for i, j := range jobs {
		sem <- struct{}{}
		mu.Lock()
		stop := failed >= 0
		mu.Unlock()
		skip := ""
		switch {
		case stop:
			skip = "an earlier run failed"
		case ctx.Err() != nil:
			skip = "interrupted"
		case meter.over():
			skip = fmt.Sprintf("dollar cap $%.2f reached", cfg.MaxUSD)
		}
		if skip != "" {
			<-sem
			results[i] = Result{Scenario: j.s.Name, Arm: j.arm.Name, Mode: j.arm.Mode, Skipped: skip}
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			results[i], errs[i] = runOne(runCtx, cfg, model, meter, j.s, j.arm)
			mu.Lock()
			defer mu.Unlock()
			switch {
			case errs[i] != nil && failed < 0:
				failed = i
				stopAll()
			case failed >= 0 && ctx.Err() == nil && (errs[i] != nil || (results[i].cancelled && !results[i].Success)):
				// Cancelled because another run failed.
				errs[i] = nil
				results[i].Why, results[i].Invalid = "", false
				results[i].Skipped = "stopped: another run failed"
			}
		}()
	}
	wg.Wait()
	rep.SpentUSD = meter.spentUSD()

	var firstErr error
	if failed >= 0 {
		firstErr = fmt.Errorf("%s/%s: %w", jobs[failed].s.Name, jobs[failed].arm.Name, errs[failed])
		rep.Stopped = "error: " + Shorten(errs[failed].Error())
	}
	for _, r := range results {
		if r.Skipped == "an earlier run failed" {
			// Runs that never started after a failure are not reported.
			continue
		}
		if r.Skipped != "" && rep.Stopped == "" {
			rep.Stopped = r.Skipped
		}
		rep.Results = append(rep.Results, r)
	}
	return rep, firstErr
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
	// Resolve symlinks (macOS's temporary directory is one), so the work
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
		// The run works inside its own copy of the workspace: relative paths
		// resolve there, and the process's working directory is untouched.
		Dir: dir, WorkDir: dir, GlobalSkillsDir: "-", Builtins: builtins, Provider: model, Gateway: cfg.Gateway, Gate: gate,
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
	// Every model request goes through the meter, whatever the arm set.
	if len(opts.Providers) > 0 {
		return res, errors.New("evals: an arm may not add named providers; they would bypass the dollar cap")
	}
	probe := &probe{seedLacks: arm.SeedLacks}
	meteredProvider := &metered{Provider: opts.Provider, meter: meter, cancel: cancel, probe: probe}
	opts.Provider = meteredProvider
	a, err := agent.Open(runCtx, opts)
	if err != nil {
		return res, err
	}
	defer func() { retErr = errors.Join(retErr, a.Close()) }()
	stubs := &stubCalls{}
	if len(s.Stubs) > 0 {
		if err := a.Tools().SetGroup(StubGroup, stubs.tools(s.Stubs)...); err != nil {
			return res, err
		}
	}
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
	res.cancelled = ctx.Err() != nil
	res.WallMS = time.Since(start).Milliseconds()
	res.Approvals = int(gate.n.Load())
	res.StubCalls = stubs.counts()

	mu.Lock()
	recs := append([]agent.TraceRecord(nil), records[first:]...)
	mu.Unlock()
	res.fill(recs, conv.ID(), meter.price)

	switch {
	case meteredProvider.capped.Load():
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
	if why := invalid(s, arm, res, probe); why != "" {
		res.Success, res.Invalid, res.Why = false, true, "invalid: "+why
	}
	return res, nil
}

// invalid says why a subtask run did not test what its arm is for, or "".
func invalid(s Scenario, arm Arm, r Result, p *probe) string {
	if arm.Mode != Subtask || r.Skipped != "" {
		return ""
	}
	for _, v := range s.Requires {
		if !slices.Contains(r.Variants, v) {
			return fmt.Sprintf("the %s variant did not run", v)
		}
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.override && (arm.VariesLastN || len(arm.SeedLacks) > 0) {
		return "a subtask call set context.last_n or context.full, so the arm's last_n did not apply"
	}
	if p.seeded != "" {
		return fmt.Sprintf("a subtask's first message already held %q, so it never had to look for it", p.seeded)
	}
	if arm.NeedsStored && !p.stored {
		return "no subtask result was stored, so the result cap was not exercised"
	}
	return ""
}

// probe watches the requests of one run for what decides whether it tested
// its arm.
type probe struct {
	seedLacks []string
	mu        sync.Mutex
	// seeded is a fact found in a subtask's first message; override is set
	// when a subtask call chose its own context; stored when a subtask's
	// result came back as a stored pointer.
	seeded   string
	override bool
	stored   bool
}

func (p *probe) see(req llmtypes.ChatRequest) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if goalOf(req.Messages) != "" && p.seeded == "" {
		first := firstUser(req.Messages)
		for _, f := range p.seedLacks {
			if strings.Contains(first, f) {
				p.seeded = f
				break
			}
		}
	}
	for _, m := range req.Messages {
		for _, b := range m.ContentBlocks {
			if b.Type == "tool_use" && b.Name == "subtask" && b.Input != nil {
				if c, ok := (*b.Input)["context"].(map[string]any); ok {
					if _, set := c["last_n"]; set {
						p.override = true
					}
					if full, _ := c["full"].(bool); full {
						p.override = true
					}
				}
			}
			if b.Type == "tool_result" && strings.Contains(b.Content, `"result_ref"`) {
				p.stored = true
			}
		}
	}
}

// firstUser is the text of the first user message.
func firstUser(msgs []llmtypes.ChatMessage) string {
	for _, m := range msgs {
		if m.Role != "user" {
			continue
		}
		text := m.Content
		for _, b := range m.ContentBlocks {
			if b.Type == "text" && b.Name != slots.TurnContextBlock {
				text += b.Text
			}
		}
		return text
	}
	return ""
}

// fill takes the metrics from the task turn's trace records.
func (r *Result) fill(recs []agent.TraceRecord, root string, price Price) {
	children := map[string]bool{}
	variants := map[string]bool{}
	tools := map[string]bool{}
	for _, rec := range recs {
		r.Compactions += len(rec.Compactions)
		if rec.ParentConversationID != "" {
			children[rec.ConversationID] = true
			if rec.Role != "" {
				variants[rec.Role] = true
			}
			for _, c := range rec.ToolCalls {
				tools[c.Tool] = true
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
		for _, c := range rec.ToolCalls {
			if c.Tool == "fetch_tool_result" {
				r.ParentFetches++
			}
		}
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
	for t := range tools {
		r.SubtaskTools = append(r.SubtaskTools, t)
	}
	slices.Sort(r.SubtaskTools)
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
// arrives, so concurrent requests cannot together pass the cap. A request
// that would pass the cap waits while other requests hold reservations (they
// usually settle far below their worst case) and is refused only when it
// would pass the cap on its own; after that every request is refused.
type meter struct {
	price    Price
	max      float64
	mu       sync.Mutex
	spent    float64
	reserved float64
	refused  bool
	// holds counts the reservations not yet settled. It decides whether a
	// request waits, since reserved, a float, may not return to exactly 0.
	holds int
	// changed is closed, and replaced, whenever a reservation settles.
	changed chan struct{}
}

func newMeter(price Price, maxUSD float64) *meter {
	return &meter{price: price, max: maxUSD, changed: make(chan struct{})}
}

// reserve holds cost against the cap. It returns errCapped when the request
// would pass the cap, or ctx's error if ctx ends while it waits.
func (m *meter) reserve(ctx context.Context, cost float64) error {
	for {
		m.mu.Lock()
		switch {
		case m.refused:
			m.mu.Unlock()
			return errCapped
		case m.max <= 0 || m.spent+m.reserved+cost <= m.max:
			m.reserved += cost
			m.holds++
			m.mu.Unlock()
			return nil
		case m.holds == 0:
			m.refused = true
			m.mu.Unlock()
			return errCapped
		}
		wait := m.changed
		m.mu.Unlock()
		select {
		case <-wait:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// settle releases a reservation and records what the request cost.
func (m *meter) settle(reserved, cost float64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.reserved -= reserved
	m.holds--
	if m.holds == 0 {
		m.reserved = 0
	}
	m.spent += cost
	close(m.changed)
	m.changed = make(chan struct{})
}

// over reports whether no further request can start.
func (m *meter) over() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.refused || (m.max > 0 && m.spent >= m.max)
}

func (m *meter) spentUSD() float64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.spent
}

// defaultMaxOutput is the output allowance assumed for a request that names
// none: the largest the providers give by default.
const defaultMaxOutput = 16384

// worst is a request's most expensive outcome: its whole input, estimated
// generously at three characters a token, and its whole output allowance.
func (m *meter) worst(req llmtypes.ChatRequest) float64 {
	out := req.MaxTokens
	if out <= 0 {
		out = defaultMaxOutput
	}
	return m.price.usage(inputTokens(req), out, 0, 0)
}

// inputTokens estimates a request's input generously, at three characters a
// token, for pricing requests that report no usage.
func inputTokens(req llmtypes.ChatRequest) int {
	n := len(req.SystemPrompt)
	for _, s := range req.SlotBlocks {
		n += len(s.Content)
	}
	msgs, _ := json.Marshal(req.Messages)
	tools, _ := json.Marshal(req.Tools)
	return (n+len(msgs)+len(tools))/3 + 1
}

var errCapped = errors.New("evals: dollar cap reached")

// metered prices every model request against the meter and ends the run when
// the cap would be passed: the request is refused and the run cancelled.
type metered struct {
	llmcontracts.Provider
	meter  *meter
	cancel context.CancelFunc
	probe  *probe
	// capped is set when the cap refused one of this run's requests.
	capped atomic.Bool
}

// refuse ends the run when the meter refused a request for the cap.
func (p *metered) refuse(err error) error {
	if errors.Is(err, errCapped) {
		p.capped.Store(true)
		p.cancel()
	}
	return err
}

func (p *metered) StreamChat(ctx context.Context, req llmtypes.ChatRequest) (<-chan llmtypes.StreamEvent, error) {
	if p.probe != nil {
		p.probe.see(req)
	}
	hold := p.meter.worst(req)
	if err := p.meter.reserve(ctx, hold); err != nil {
		return nil, p.refuse(err)
	}
	in, err := p.Provider.StreamChat(ctx, req)
	if err != nil {
		// A request that failed to start may still have been billed.
		p.meter.settle(hold, hold)
		return nil, err
	}
	out := make(chan llmtypes.StreamEvent)
	go func() {
		defer close(out)
		// The reservation holds until the stream ends. A stream that finished
		// settles to its reported usage; one that did not (cancelled, cut
		// off, or reporting no usage) is charged at least the reservation,
		// since the provider may have billed more than it reported.
		var reported float64
		finished := false
		account := func(ev llmtypes.StreamEvent) {
			switch {
			case ev.Type == llmtypes.EventDone:
				finished = true
			case ev.Type == llmtypes.EventUsage && ev.Usage != nil:
				u := ev.Usage
				reported += p.meter.price.usage(u.InputTokens, u.OutputTokens, u.CacheReadTokens, u.CacheCreationTokens)
				if u.StopReason != "" {
					finished = true
				}
			}
		}
		defer func() {
			cost := max(hold, reported)
			if finished && reported > 0 {
				cost = reported
			}
			p.meter.settle(hold, cost)
		}()
		for ev := range in {
			account(ev)
			select {
			case out <- ev:
			case <-ctx.Done():
				// Nobody reads any more; keep accounting what still arrives.
				for ev := range in {
					account(ev)
				}
				return
			}
		}
	}()
	return out, nil
}

// Complete serves compaction summaries and argument repair. It reports no
// usage, so a call that returns is charged an estimate (its input at three
// characters a token and its answer at four), and one that fails is charged
// its reservation.
func (p *metered) Complete(ctx context.Context, req llmtypes.ChatRequest) (string, error) {
	hold := p.meter.worst(req)
	if err := p.meter.reserve(ctx, hold); err != nil {
		return "", p.refuse(err)
	}
	text, err := p.Provider.Complete(ctx, req)
	if err != nil {
		p.meter.settle(hold, hold)
		return text, err
	}
	p.meter.settle(hold, p.meter.price.usage(inputTokens(req), len(text)/4+1, 0, 0))
	return text, nil
}
