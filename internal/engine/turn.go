package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	ctxpkg "github.com/hollis-labs/libs/agent/core/context"
	"github.com/hollis-labs/libs/agent/core/loopdetect"
	"github.com/hollis-labs/libs/agent/core/runloop"
	"github.com/hollis-labs/libs/agent/core/turn"
	"github.com/hollis-labs/libs/agent/trace"
	"github.com/hollis-labs/libs/core/panics"
	"github.com/hollis-labs/libs/llm/core/llmcontracts"
	"github.com/hollis-labs/libs/llm/core/llmtypes"
	"github.com/hollis-labs/libs/security/policy"
	"github.com/hollis-labs/nanite/internal/definition"
	"github.com/hollis-labs/nanite/internal/events"
	"github.com/hollis-labs/nanite/internal/permission"
	"github.com/hollis-labs/nanite/internal/provider/failure"
	"github.com/hollis-labs/nanite/internal/provider/station"
	"github.com/hollis-labs/nanite/internal/recover"
	"github.com/hollis-labs/nanite/internal/recovery"
	"github.com/hollis-labs/nanite/internal/skills"
	"github.com/hollis-labs/nanite/internal/slots"
	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/internal/subagent"
	"github.com/hollis-labs/nanite/internal/tools"
)

type execution struct {
	engine     *Engine
	run        *Run
	info       store.Conversation
	definition definition.Definition
	provider   llmcontracts.Provider
	// summaryProvider and summaryModel serve compaction summaries.
	summaryProvider llmcontracts.Provider
	summaryModel    string
	assembled       slots.Assembled
	policy          policy.PDP
	journal         *store.Journal
	submitted       bool
	messageIDs      []string
	messages        []llmtypes.ChatMessage
	parent          *string
	validators      map[string]recover.Validator
	catalog         map[string]tools.Tool
	specs           []llmtypes.ToolDefinition
	loop            *loopdetect.Detector
	result          events.Result
	// lastRequestTokens is the input the provider reported for the latest
	// request and lastRequestEstimate what Nanite estimated for it; their
	// ratio calibrates the compaction check.
	lastRequestTokens, lastRequestEstimate int
	// schemaTokens is the estimated size of this turn's tool schemas.
	schemaTokens int
	// warnedStatic is set once the turn has warned that fixed content alone
	// fills the compaction budget.
	warnedStatic bool
	// toolCalls and passes record the turn's tool calls and compaction
	// passes for its trace record.
	toolCalls []trace.ToolCall
	passes    []trace.Compaction
	// subtasks lists the subtasks this turn started, for its trace record;
	// usageMu guards it, since subtasks may run in parallel.
	subtasks []trace.Subtask
	// ownUsage is the turn's own model requests, without what subtasks
	// rolled into result.Usage; it is the trace record's usage.
	ownUsage events.Usage
	// compactions counts compaction passes in this turn; stalled is set once
	// a pass frees too little, so later steps stop retrying it.
	compactions int
	stalled     bool
	// variants are the subtask variants this turn offers; variant is this
	// conversation's own when it is a child, and depth is 0 for a root chat.
	variants map[string]subagent.Variant
	variant  subagent.Variant
	chain    []subagent.Variant
	depth    int
	// root is the conversation at the top of this one's subtask chain (this
	// conversation itself at depth 0). A subtask is evaluated under the
	// root's permissions and grants, and relays the asks they do not cover
	// to the root, attributed by origin (nil at depth 0).
	root   store.Conversation
	origin *permission.SubtaskOrigin
	// lineage is this conversation and those above it, nearest first: the
	// conversations whose grants cover it.
	lineage []string
	// deadline is the child's wall-clock budget, checked between steps.
	deadline time.Time
	// shared is the root turn's token budget, shared with its foreground
	// subtasks at every depth; nil when the agent sets none.
	shared *turnBudget
	// source is what started this turn (Input.Source).
	source string
	// usageMu guards result.Usage, which subtask handlers add child usage to.
	usageMu sync.Mutex
	// returned is set once this child has called return_result; the turn
	// ends after that step.
	returned atomic.Bool
}

// canSpawn reports whether this conversation may run subtask: a root chat
// always may, a depth-1 child only when its variant has can_spawn.
func (x *execution) canSpawn() bool {
	return x.depth == 0 || (x.depth == 1 && x.variant.CanSpawn)
}

// Compaction limits for one turn. After a pass that frees less than
// minCompactionGain of the estimate, the step path stops compacting.
const (
	maxCompactionsPerTurn = 3
	minCompactionGain     = 0.10
)

// errCompactionNoProgress reports a compaction pass that freed too little
// context to be worth repeating.
var errCompactionNoProgress = errors.New("compaction freed too little context")

// errEmptySummary reports a summarizer that returned no text.
var errEmptySummary = errors.New("compaction summary is empty")

func (e *Engine) execute(ctx context.Context, r *Run, info store.Conversation, input Input, state slots.Snapshot, prev map[string]string, hostTools []tools.Tool, hostSkills []skills.Skill, interrupted *store.InterruptedTurn) {
	ctx, endSpan := events.TurnSpan(ctx)
	var result events.Result
	// Every turn writes one trace record, however it ends: it is written here,
	// after the guard, so a panic anywhere in the turn is recorded as failed
	// with its ref.
	tr := &turnTrace{rec: newTraceRecord(time.Now().UTC(), r.id, info, interrupted != nil, e.subtaskLinkOf(info.ID))}
	if input.Source == SourceNotice && interrupted == nil {
		// A notice turn is an automatic activation: its source is reflex.
		tr.rec.Source = trace.SourceReflex
	}
	// A panic while preparing the turn fails it with a ref; the host and the
	// conversation stay usable.
	err := e.panics.Call("turn", func() (err error) {
		result, err = e.turn(station.WithSessionID(ctx, info.ID), r, info, input, state, prev, hostTools, hostSkills, interrupted, tr)
		return err
	}, panics.With("conversation", info.ID), panics.With("run", r.id))
	if err != nil {
		if clean := e.scrub.Text(err.Error()); clean != err.Error() {
			err = errors.New(clean)
		}
	}
	// Writing the record must never keep the run from finishing.
	_ = e.panics.Call("trace", func() error { e.writeTrace(ctx, tr.rec, tr.x, result, err); return nil }, panics.With("conversation", info.ID), panics.With("run", r.id))
	endSpan()
	terminal := r.terminal(result, err)
	r.report(context.WithoutCancel(ctx), terminal)
	r.cancel()
	e.release(info.ID, r, func() { r.finish(result, err, terminal) })
}

// release removes r from the active runs and runs finish while holding the
// engine lock, unlocking even if finish panics.
func (e *Engine) release(id string, r *Run, finish func()) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.active[id] == r {
		delete(e.active, id)
		e.signalLocked()
	}
	finish()
}

// turnTrace is what a turn contributes to its trace record: the record so far
// and, once built, the execution with its tool calls and compactions.
type turnTrace struct {
	rec trace.Turn
	x   *execution
}

func (e *Engine) turn(ctx context.Context, r *Run, info store.Conversation, input Input, state slots.Snapshot, prev map[string]string, hostTools []tools.Tool, hostSkills []skills.Skill, interrupted *store.InterruptedTurn, tr *turnTrace) (_ events.Result, retErr error) {
	r.emit(ctx, func(meta events.EventMeta) events.Event { return events.RunStarted{EventMeta: meta} })
	if e.testTurnHook != nil {
		e.testTurnHook()
	}
	var resume recovery.Plan
	if interrupted != nil {
		rawHistory, err := e.cfg.Store.History(ctx, info.ID)
		if err != nil {
			return events.Result{}, err
		}
		resume, err = recovery.ResumePlan(rawHistory, *interrupted)
		if err != nil || !resume.Generate {
			return e.finalizeInterrupted(ctx, info.ID, *interrupted, resume, err)
		}
		input.Text = resume.UserText
	}

	def, err := definition.Load(e.cfg.Dir, e.cfg.GlobalSkillsDir)
	if err != nil {
		return events.Result{}, err
	}
	pdp, err := permission.Rules(def.Snapshot.Profile.Permissions.Mode, def.Rules)
	if err != nil {
		return events.Result{}, err
	}
	variants, variantErr := subagent.Load(e.cfg.Dir)
	variantWarning := ""
	if variantErr != nil {
		// A bad authored variant is skipped, not fatal; nanite validate reports it.
		variantWarning = "Some subtask variants did not load: " + variantErr.Error()
	}
	var variant subagent.Variant
	var chain []subagent.Variant
	depth := 0
	root := info
	lineage := []string{info.ID}
	if info.ParentID != "" {
		depth = 1
		parentInfo, err := e.cfg.Store.Conversation(ctx, info.ParentID)
		if err != nil {
			return events.Result{}, err
		}
		root = parentInfo
		lineage = append(lineage, parentInfo.ID)
		if parentInfo.ParentID != "" {
			if root, err = e.cfg.Store.Conversation(ctx, parentInfo.ParentID); err != nil {
				return events.Result{}, err
			}
			lineage = append(lineage, root.ID)
			// A depth-2 child is also bound by the lead variant that started it.
			depth = 2
			lead, ok := variants[parentInfo.Role]
			if !ok {
				return events.Result{}, fmt.Errorf("subtask variant %q is not defined", parentInfo.Role)
			}
			chain = append(chain, lead)
		}
		var ok bool
		if variant, ok = variants[info.Role]; !ok {
			return events.Result{}, fmt.Errorf("subtask variant %q is not defined", info.Role)
		}
		chain = append(chain, variant)
		// A budget only narrows the agent's own limit.
		if steps := variant.Budgets.Steps; steps > 0 && steps < def.Runtime.MaxSteps {
			def.Runtime.MaxSteps = steps
		}
	}
	use := definition.UseChat
	if info.ParentID != "" {
		use = definition.UseSubtask
	}
	var model llmcontracts.Provider
	var sel definition.Selection
	if variant.Provider != "" {
		// A variant's own selection, resolved like a use in providers.yaml.
		model, sel, err = e.resolve(def, "variant "+variant.Name, definition.Selection{Provider: variant.Provider, Model: variant.Model})
	} else {
		model, sel, err = e.provider(def, use)
		if variant.Model != "" {
			// The subtask use's provider must serve this model.
			sel.Model = variant.Model
		}
	}
	if err != nil {
		return events.Result{}, err
	}
	modelName := sel.Model
	tr.rec.Provider, tr.rec.Model = e.scrub.Text(sel.Provider), e.scrub.Text(sel.Model)
	// Compaction summaries use their own selection, or else the provider and
	// model this turn runs on.
	summaryProvider, summaryModel := model, modelName
	if sel, ok := def.Use[definition.UseSummarizer]; ok {
		if summaryProvider, sel, err = e.resolve(def, definition.UseSummarizer, sel); err != nil {
			return events.Result{}, err
		}
		summaryModel = sel.Model
	}
	// The selected model is the one this turn asks for and reports in the
	// environment slot.
	def.Runtime.Model = modelName
	catalog := skills.NewCatalog(def.Skills, hostSkills)
	lease, warnings := e.mcp.Reconcile(ctx, def.Snapshot.MCP)
	defer lease.Close()
	mcpTools, discoveryWarnings := lease.Tools(ctx)
	warnings = append(warnings, discoveryWarnings...)
	if variantWarning != "" {
		warnings = append(warnings, variantWarning)
	}
	for i, warning := range warnings {
		warnings[i] = e.scrub.Text(warning)
		e.cfg.Logger.WarnContext(ctx, warnings[i])
	}
	pending, err := e.cfg.Store.PendingNotices(ctx, info.ID)
	if err != nil {
		return events.Result{}, err
	}
	skillListing := catalog.Listing()
	if !e.cfg.Builtins.Has(tools.BuiltinSkills) {
		skillListing = "" // Without the skill tools a listing would name things the agent cannot load.
	}
	assembled, err := e.assemble(ctx, input, slots.TurnInfo{ConversationID: info.ID, TurnID: r.id, UserText: input.Text, Role: info.Role}, def, state, prev, skillListing, variant.Prompt, pdp.Mode(), warnings, pending)
	if err != nil {
		return events.Result{}, err
	}
	e.warnCuts(ctx, info.ID, assembled.Report)
	x := &execution{engine: e, run: r, info: info, definition: def, provider: model, assembled: assembled, policy: pdp, loop: loopdetect.New(),
		summaryProvider: summaryProvider, summaryModel: summaryModel,
		variants: variants, variant: variant, chain: chain, depth: depth, root: root, lineage: lineage, source: input.Source}
	if depth > 0 {
		// The title's goal was scrubbed when the child was created.
		x.origin = &permission.SubtaskOrigin{ChildID: info.ID, Variant: variant.Name, Goal: strings.TrimPrefix(info.Title, subtaskTitlePrefix),
			Depth: depth, CallID: e.subtaskCall(info.ID), Dispatched: e.dispatchedChain(lineage[:len(lineage)-1])}
	}
	if variant.Budgets.Wall > 0 {
		x.deadline = time.Now().Add(variant.Budgets.Wall)
	}
	x.shared = sharedBudget(ctx, depth, e.cfg.TurnTokenBudget)
	tr.x = x
	tr.rec.Slots, tr.rec.Trimmed = traceSlots(assembled, e.scrub.Text)
	if err := x.prepareTools(hostTools, mcpTools, catalog); err != nil {
		return events.Result{}, err
	}
	x.sizeWindow()
	if x.schemaTokens > 0 {
		// The trace shows the tool schemas' share of the request.
		tr.rec.Slots = append(tr.rec.Slots, trace.Slot{Name: "tool_schemas", Tokens: x.schemaTokens})
	}
	journal, err := e.cfg.Store.OpenJournal(ctx, info.ID)
	if err != nil {
		return events.Result{}, err
	}
	defer func() { retErr = errors.Join(retErr, journal.Close()) }()
	x.journal = journal
	summary, history, parent, err := e.cfg.Store.ModelHistory(ctx, info.ID)
	if err != nil {
		return events.Result{}, err
	}
	if summary != "" {
		x.messages = append(x.messages, llmtypes.ChatMessage{Role: "user", Content: summary})
		x.messageIDs = append(x.messageIDs, "")
	}
	if parent != "" {
		x.parent = &parent
	}
	for _, m := range history {
		x.addMessage(m)
	}

	now := time.Now().UTC()
	record := store.Turn{ID: r.id, UserMessageID: store.NewID(now)}
	if interrupted != nil {
		record.UserMessageID = interrupted.UserMessageID
		x.result.Steps = resume.Steps
		x.run.steps.Store(int64(resume.Steps))
		if err := journal.ResumeTurn(record, now); err != nil {
			return events.Result{}, err
		}
		if len(resume.Missing) > 0 {
			err = x.message("tool", resume.Missing)
		}
	} else {
		if err := journal.StartTurn(record, now); err != nil {
			return events.Result{}, err
		}
		message := store.Message{ID: record.UserMessageID, TurnID: r.id, Role: "user", CreatedAt: now,
			Blocks: []llmtypes.ContentBlock{{Type: "text", Text: e.scrub.Text(input.Text)}}}
		// Slots placed in the turn go in front of what was said, stored with
		// it: the conversation then remembers the context each message came
		// with, and the system blocks stay the same.
		if block, ok := e.turnContext(assembled); ok {
			message.Blocks = append([]llmtypes.ContentBlock{block}, message.Blocks...)
		}
		err = x.append(message)
	}
	// In-turn slots travel in the messages (this turn's or an earlier one);
	// drop them from the window's accounting so compaction does not count
	// them twice. This holds for a resumed turn too.
	for _, name := range slots.Names() {
		if e.placement(name) == slots.InTurn && assembled.Window.Slot(string(name)) != nil {
			assembled.Window.SetContent(string(name), "")
		}
	}
	if err == nil {
		e.mu.Lock()
		e.previous[info.ID] = assembled.Hashes
		e.mu.Unlock()
		r.emit(ctx, func(meta events.EventMeta) events.Event {
			return events.SlotsAssembled{EventMeta: meta, Report: assembled.Report}
		})
		// Mark delivery only after a model request has received the session
		// slot. A panic in the model loop records the turn as failed.
		err = e.panics.Call("model loop", func() error { return x.generate(ctx) }, panics.With("conversation", info.ID), panics.With("run", r.id))
		if x.submitted {
			err = errors.Join(err, e.cfg.Store.MarkNoticesDelivered(context.WithoutCancel(ctx), info.ID, r.id, deliveredNoticeIDs(assembled.Report, pending)))
		}
	}
	record.Status = "completed"
	record.StopReason = x.result.StopReason
	record.Usage, _ = json.Marshal(x.result.Usage)
	if err != nil {
		record.Status = "failed"
		if ctx.Err() != nil || errors.Is(err, context.Canceled) {
			record.Status = "cancelled"
		}
		record.Error = e.scrub.Text(err.Error())
	}
	err = errors.Join(err, journal.FinishTurn(record, time.Now().UTC()))
	return x.result, err
}

func (e *Engine) finalizeInterrupted(ctx context.Context, conversation string, turn store.InterruptedTurn, plan recovery.Plan, planErr error) (events.Result, error) {
	j, err := e.cfg.Store.OpenJournal(ctx, conversation)
	if err != nil {
		return events.Result{}, err
	}
	record := store.Turn{ID: turn.ID, UserMessageID: turn.UserMessageID, Status: "completed", StopReason: llmtypes.StopReasonEndTurn}
	if planErr != nil {
		record.Status = "failed"
		record.Error = planErr.Error()
		record.StopReason = ""
	}
	result := events.Result{Text: plan.AssistantText, Steps: plan.Steps, StopReason: record.StopReason}
	return result, errors.Join(planErr, j.FinishTurn(record, time.Now().UTC()), j.Close())
}

func (x *execution) addMessage(message store.Message) {
	role := message.Role
	if role == "tool" {
		role = "user"
	}
	x.parent = &message.ID
	blocks := slices.DeleteFunc(slices.Clone(message.Blocks), func(b llmtypes.ContentBlock) bool {
		return b.Type == "thinking" && b.Signature == ""
	})
	if len(blocks) == 0 {
		return
	}
	x.messages = append(x.messages, llmtypes.ChatMessage{Role: role, ContentBlocks: blocks})
	x.messageIDs = append(x.messageIDs, message.ID)
}

func (x *execution) append(message store.Message) error {
	if err := x.journal.AppendMessage(message, x.parent); err != nil {
		return err
	}
	x.addMessage(message)
	return nil
}

func (x *execution) message(role string, blocks []llmtypes.ContentBlock) error {
	now := time.Now().UTC()
	return x.append(store.Message{ID: store.NewID(now), TurnID: x.run.id, Role: role, Blocks: blocks, CreatedAt: now})
}

type attempt struct {
	events <-chan llmtypes.StreamEvent
	cancel context.CancelFunc
}

type step struct {
	blocks  []llmtypes.ContentBlock
	calls   []llmtypes.ToolUseBlock
	text    string
	emitted bool
}

func (x *execution) generate(ctx context.Context) error {
	var runErr error
	var retries int
	var allText strings.Builder
	blocks := make([]llmtypes.SlotBlock, 0, len(x.assembled.Blocks))
	for _, b := range x.assembled.Blocks {
		if x.engine.placement(slots.Name(b.SlotName)) == slots.InTurn {
			continue // sent with the turn's user message instead
		}
		blocks = append(blocks, llmtypes.SlotBlock{Name: b.SlotName, Content: b.Content, Changed: b.Changed, CacheKey: b.CacheKey})
	}
	request := func(ctx context.Context) (attempt, runloop.Directive) {
		if ctx.Err() != nil {
			runErr = ctx.Err()
			return attempt{}, runloop.Terminate
		}
		req := llmtypes.ChatRequest{Model: x.definition.Runtime.Model, MaxTokens: x.definition.Runtime.MaxOutputTokens,
			SlotBlocks: blocks, Messages: x.messages, Tools: x.specs, CacheHints: []llmtypes.CacheHint{
				{Position: "system"}, {Position: "tools"}, {Position: "recent_message", Index: 0}, {Position: "recent_message", Index: 1},
			}}
		safeModel := x.engine.scrub.Text(req.Model)
		spanCtx, endSpan := events.ModelSpan(ctx, safeModel)
		callCtx, cancelCall := context.WithCancel(spanCtx)
		cancel := func() { cancelCall(); endSpan() }
		x.estimateRequest()
		stream, err := x.provider.StreamChat(callCtx, req)
		if err != nil {
			cancel()
			return attempt{}, x.retry(ctx, err, &retries, &runErr)
		}
		x.submitted = true
		return attempt{events: stream, cancel: cancel}, runloop.Proceed
	}
	consume := func(ctx context.Context, a attempt) (step, runloop.Directive) {
		defer a.cancel()
		got, err := x.consume(ctx, a.events)
		if err != nil {
			if !got.emitted {
				return step{}, x.retry(ctx, err, &retries, &runErr)
			}
			appendStepText(&allText, got.text)
			if len(got.blocks) > 0 {
				saveErr := x.message("assistant", got.blocks)
				if len(got.calls) > 0 && saveErr == nil {
					saveErr = x.cancelledTools(got.calls)
					x.traceCancelled(got.calls)
				}
				err = errors.Join(err, saveErr)
			}
			runErr = err
			return step{}, runloop.Terminate
		}
		retries = 0
		return got, runloop.Proceed
	}
	settle := func(ctx context.Context, got step) runloop.Directive {
		appendStepText(&allText, got.text)
		x.result.Steps++
		x.run.steps.Store(int64(x.result.Steps))
		x.repairArgs(ctx, &got)
		if len(got.blocks) > 0 {
			if runErr = x.message("assistant", got.blocks); runErr != nil {
				return runloop.Terminate
			}
		}
		if len(got.calls) == 0 {
			if x.result.StopReason == llmtypes.StopReasonToolUse {
				x.result.StopReason = llmtypes.StopReasonEndTurn
			}
			return runloop.Finish
		}
		if runErr = x.callTools(ctx, got.calls); runErr != nil {
			return runloop.Terminate
		}
		if x.returned.Load() {
			// return_result ends a subtask: the parent has what it needs.
			x.result.StopReason = llmtypes.StopReasonEndTurn
			return runloop.Finish
		}
		if x.result.Steps >= x.definition.Runtime.MaxSteps {
			x.result.StopReason = events.StopMaxSteps
			return runloop.Finish
		}
		if stop := x.budgetStop(); stop != "" {
			x.result.StopReason = stop
			return runloop.Finish
		}
		if runErr = x.compact(ctx, false); runErr != nil {
			return runloop.Terminate
		}
		return runloop.Continue
	}
	_, err := runloop.Execute[attempt, step](ctx, runloop.Ports[attempt, step]{
		Request: request, Consume: consume, Settle: settle, Finalize: func(context.Context) runloop.Directive { return runloop.Finish },
	})
	x.result.Text = x.engine.scrub.Text(allText.String())
	if x.result.StopReason == "" && runErr == nil {
		x.result.StopReason = llmtypes.StopReasonEndTurn
	}
	return errors.Join(runErr, err)
}

func (x *execution) retry(ctx context.Context, err error, retries *int, runErr *error) runloop.Directive {
	var classified *failure.Error
	if errors.Is(err, llmcontracts.ErrRequestExceedsRateBudget) {
		classified = &failure.Error{Class: failure.Overflow, Cause: err}
	} else {
		_ = errors.As(err, &classified)
	}
	if ctx.Err() == nil && *retries < 2 && classified != nil {
		*retries++
		switch classified.Class {
		case failure.Overflow:
			compactErr := x.compact(ctx, true)
			if compactErr == nil {
				return runloop.Retry
			}
			err = errors.Join(err, compactErr)
		case failure.Retryable:
			base := time.Second << (*retries - 1)
			delay := max(classified.RetryAfter, base+rand.N(base/4)) // #nosec G404 -- Retry jitter does not need unpredictability.
			select {
			case <-time.After(delay):
				return runloop.Retry
			case <-ctx.Done():
				err = ctx.Err()
			}
		case failure.Auth, failure.Fatal:
			// These failures terminate without retrying.
		}
	}

	*runErr = err
	return runloop.Terminate
}

func (x *execution) compact(ctx context.Context, force bool) (retErr error) {
	est := ctxpkg.DefaultEstimator{}
	raw, err := json.Marshal(x.messages)
	if err != nil {
		return err
	}
	window := x.assembled.Window
	window.SetContent(ctxpkg.SlotConversation, string(raw))
	defer x.calibrate(window)()
	if !force && (x.stalled || !x.needsCompaction(window)) {
		return nil
	}
	if x.compactions >= maxCompactionsPerTurn {
		x.stalled = true
		if !force {
			return nil
		}
		return errCompactionNoProgress
	}
	x.compactions++
	before := est.Estimate(string(raw))
	x.run.emit(ctx, func(meta events.EventMeta) events.Event {
		return events.CompactionStarted{EventMeta: meta, Before: before}
	})
	// Every start is followed by exactly one outcome.
	//   - A pass that fails, or changes nothing, emits CompactionFailed and
	//     stops compaction for the rest of the turn. On the step path
	//     compaction is opportunistic, so the turn continues. A forced pass,
	//     made to recover from an overflow, returns the error so the request
	//     is not retried unchanged.
	//   - A pass that changed the history, or recorded a summary, emits
	//     Compacted, even when it freed less than minCompactionGain; that
	//     small a gain only stops later step-path passes.
	//   - A store failure ends the turn on either path.
	after := before
	// recorded is set once a summary is durable; journalErr marks a store
	// failure, which ends the turn on either path.
	recorded, journalErr := false, false
	defer func() {
		failure := retErr
		if failure == nil && !recorded && after >= before {
			failure = errCompactionNoProgress
		}
		if failure == nil && float64(before-after) < minCompactionGain*float64(before) {
			x.stalled = true
		}
		if failure != nil {
			x.stalled = true
			if force || journalErr || ctx.Err() != nil {
				retErr = failure
			} else {
				x.engine.cfg.Logger.WarnContext(ctx, "compaction skipped for the rest of the turn", "error", x.engine.scrub.Text(failure.Error()))
				retErr = nil
			}
		}
		pass := trace.Compaction{Before: before, After: after, Forced: force}
		if failure != nil {
			pass.Failed, pass.Error = true, x.engine.scrub.Text(failure.Error())
		}
		x.passes = append(x.passes, pass)
		x.run.emit(context.WithoutCancel(ctx), func(meta events.EventMeta) events.Event {
			if failure != nil {
				return events.CompactionFailed{EventMeta: meta, Err: failure}
			}
			return events.Compacted{EventMeta: meta, Before: before, After: after}
		})
	}()
	summaryProvider, summaryModel := x.summaryProvider, x.summaryModel
	if summaryProvider == nil {
		summaryProvider, summaryModel = x.provider, x.definition.Runtime.Model
	}
	summarizer := &capturingSummarizer{Summarizer: ctxpkg.NewProviderSummarizer(summaryProvider, summaryModel), messages: x.messages, messageIDs: x.messageIDs, model: x.engine.scrub.Text(summaryModel),
		// A summary is model spend too. The summarizer's Complete call
		// reports no usage, so the shared budget is charged an estimate.
		spend: func(prompt string, span []llmtypes.ChatMessage, summary string) {
			if x.shared == nil {
				return
			}
			raw, _ := json.Marshal(span)
			x.shared.add(est.Estimate(prompt) + est.Estimate(string(raw)) + est.Estimate(summary))
		}}
	pipeline := ctxpkg.CompactionPipeline{Window: window, Estimator: est, ConversationMessages: slices.Clone(x.messages), Summarizer: summarizer, SessionID: x.info.ID}
	if force {
		_, err = pipeline.RunForce(ctx)
	} else {
		_, err = pipeline.Run(ctx)
	}
	if err != nil {
		return err
	}
	if summarizer.count == 0 || strings.TrimSpace(summarizer.summary) == "" {
		// Stage-only changes preserve message positions. A durable summary must
		// carry a message ID so future turns can reconstruct exactly its coverage.
		if len(pipeline.ConversationMessages) == len(x.messageIDs) {
			x.messages = pipeline.ConversationMessages
			if remaining, err := json.Marshal(x.messages); err == nil {
				after = est.Estimate(string(remaining))
			}
		}
		return nil
	}
	count := summarizer.count
	through := x.messageIDs[count-1]
	if through == "" {
		return nil
	}
	summary := fmt.Sprintf("[Conversation summary — %d messages compacted]\n\n%s", count, x.engine.scrub.Text(summarizer.summary))
	compacted := append([]llmtypes.ChatMessage{{Role: "user", Content: summary}}, x.messages[count:]...)
	remaining, err := json.Marshal(compacted)
	if err != nil {
		return err
	}
	shrunk := est.Estimate(string(remaining))
	now := time.Now().UTC()
	if err := x.journal.RecordCompaction(store.NewID(now), now, store.Compaction{
		TurnID: x.run.id, ThroughMessageID: through, Summary: summary, Before: before, After: shrunk,
	}); err != nil {
		journalErr = true
		return err
	}
	recorded = true
	x.engine.forgetTurnContext(x.info.ID)
	x.messages = compacted
	x.messageIDs = append([]string{""}, x.messageIDs[count:]...)
	after = shrunk
	return nil
}

type capturingSummarizer struct {
	ctxpkg.Summarizer
	messages   []llmtypes.ChatMessage
	messageIDs []string
	model      string
	count      int
	summary    string
	// spend, when set, is told of each summary call made.
	spend func(prompt string, span []llmtypes.ChatMessage, summary string)
}

func (s *capturingSummarizer) Summarize(ctx context.Context, prompt string, span []llmtypes.ChatMessage) (string, error) {
	end := len(span)
	// Keep a tool call and its immediately following result together.
	if end < len(s.messages) && slices.ContainsFunc(s.messages[end].ContentBlocks, func(b llmtypes.ContentBlock) bool { return b.Type == "tool_result" }) {
		end--
	}
	if end < 1 || s.messageIDs[end-1] == "" {
		return "", nil
	}
	spanCtx, endSpan := events.ModelSpan(ctx, s.model)
	defer endSpan()
	summary, err := s.Summarizer.Summarize(spanCtx, prompt, span[:end])
	if s.spend != nil && err == nil {
		// A failed summary call is not charged: it reports nothing to count.
		s.spend(prompt, span[:end], summary)
	}
	if err == nil && strings.TrimSpace(summary) == "" {
		// An empty summary would drop the span with nothing in its place.
		err = errEmptySummary
	}
	if err == nil {
		s.count, s.summary = end, summary
	}
	return summary, err
}

func (x *execution) consume(ctx context.Context, stream <-chan llmtypes.StreamEvent) (step, error) {
	var got step
	var text strings.Builder
	reader, err := turn.NewReader(ctx, stream, 90*time.Second)
	if err != nil {
		return got, err
	}
	defer reader.Close()
	for {
		ev, ok, err := reader.Next()
		if err != nil {
			return got, err
		}
		if !ok {
			return got, turn.ErrTurnTruncated
		}
		switch ev.Type {
		case llmtypes.EventDone:
			return got, nil
		case llmtypes.EventError:
			return got, failure.FromEvent(ev)
		case llmtypes.EventDelta:
			clean := x.engine.scrub.Text(ev.Content)
			text.WriteString(clean)
			got.text = text.String()
			if n := len(got.blocks); n > 0 && got.blocks[n-1].Type == "text" {
				got.blocks[n-1].Text += clean
			} else {
				got.blocks = append(got.blocks, llmtypes.ContentBlock{Type: "text", Text: clean})
			}
			got.emitted = true
			x.run.emit(ctx, func(meta events.EventMeta) events.Event { return events.TextDelta{EventMeta: meta, Text: clean} })
		case llmtypes.EventThinking:
			if ev.ThinkingBlock == nil {
				continue
			}
			text := x.engine.scrub.Text(ev.ThinkingBlock.Thinking)
			signature := ev.ThinkingBlock.Signature
			if text != ev.ThinkingBlock.Thinking {
				signature = ""
			}
			got.blocks = append(got.blocks, llmtypes.ContentBlock{Type: "thinking", Text: text, Signature: signature})
			got.emitted = true
			x.run.emit(ctx, func(meta events.EventMeta) events.Event { return events.Thinking{EventMeta: meta, Text: text} })
		case llmtypes.EventToolUse:
			if ev.ToolUse == nil {
				continue
			}
			tool := *ev.ToolUse
			args := tool.Input
			if args == nil {
				args = make(map[string]any)
			}
			raw, err := json.Marshal(args)
			if err != nil {
				return got, fmt.Errorf("encode tool input: %w", err)
			}
			var clean map[string]any
			if err := json.Unmarshal(x.engine.scrub.Bytes(raw), &clean); err != nil {
				return got, fmt.Errorf("redact tool input: %w", err)
			}
			got.calls = append(got.calls, tool)
			got.blocks = append(got.blocks, llmtypes.ContentBlock{Type: "tool_use", ID: tool.ID, Name: tool.Name, Input: &clean})
			got.emitted = true
		case llmtypes.EventSessionID:
			// Nanite owns the conversation identifier.
		case llmtypes.EventUsage:
			if ev.Usage == nil {
				continue
			}
			u := ev.Usage
			x.usageMu.Lock()
			x.result.Usage.InputTokens += u.InputTokens
			x.result.Usage.OutputTokens += u.OutputTokens
			x.result.Usage.CacheReadTokens += u.CacheReadTokens
			x.result.Usage.CacheWriteTokens += u.CacheCreationTokens
			x.ownUsage.InputTokens += u.InputTokens
			x.ownUsage.OutputTokens += u.OutputTokens
			x.ownUsage.CacheReadTokens += u.CacheReadTokens
			x.ownUsage.CacheWriteTokens += u.CacheCreationTokens
			x.usageMu.Unlock()
			x.shared.add(u.InputTokens + u.OutputTokens + u.CacheReadTokens + u.CacheCreationTokens)
			// What the provider counted for the request just sent: its input,
			// cached or not. Compaction checks it against the budget too,
			// since a size estimate can be well off.
			if in := u.InputTokens + u.CacheReadTokens + u.CacheCreationTokens; in > 0 {
				x.lastRequestTokens = in
			}
			if u.StopReason != "" {
				x.result.StopReason = u.StopReason
			}
			x.run.emit(ctx, func(meta events.EventMeta) events.Event {
				return events.Usage{EventMeta: meta, InputTokens: u.InputTokens, OutputTokens: u.OutputTokens, CacheReadTokens: u.CacheReadTokens, CacheWriteTokens: u.CacheCreationTokens}
			})
		}
	}
}

// appendStepText adds one model step's text to the turn's reply, separating
// steps so text written before and after tool calls does not run together.
func appendStepText(all *strings.Builder, text string) {
	if text == "" {
		return
	}
	if all.Len() > 0 {
		all.WriteString("\n\n")
	}
	all.WriteString(text)
}

// budgetStop is the stop reason when a child has spent its variant's token
// or wall-clock budget, or any turn has spent the token budget its root turn
// shares with its subtasks, checked between steps; "" while within budget.
func (x *execution) budgetStop() string {
	if limit := x.variant.Budgets.Tokens; limit > 0 {
		// Every token counts, cached ones included, and a lead's spend
		// includes the usage its finished children rolled up into it.
		x.usageMu.Lock()
		u := x.result.Usage
		x.usageMu.Unlock()
		spent := u.InputTokens + u.OutputTokens + u.CacheReadTokens + u.CacheWriteTokens
		if spent >= limit {
			return events.StopTokenBudget
		}
	}
	if x.shared.spent() {
		return events.StopTokenBudget
	}
	if !x.deadline.IsZero() && !time.Now().Before(x.deadline) {
		return events.StopWallBudget
	}
	return ""
}

// warnCuts logs every entry the selector trimmed or left out, so a host
// never loses context without a trace of it, once each time the slot
// changes. Pointers are by design and are not warned about.
func (e *Engine) warnCuts(ctx context.Context, conversation string, report slots.Report) {
	for _, name := range slots.Names() {
		// An unchanged slot was warned about when it last changed.
		if !report[name].Changed {
			continue
		}
		for _, d := range report[name].Decisions {
			if d.Disposition == slots.Trim || d.Disposition == slots.Skip {
				e.cfg.Logger.WarnContext(ctx, "nanite: slot entry cut", "conversation", conversation, "slot", string(name),
					"owner", e.scrub.Text(d.Owner), "key", e.scrub.Text(d.Key), "disposition", dispositionName(d.Disposition), "reason", d.Reason)
			}
		}
	}
}

func dispositionName(d slots.Disposition) string {
	switch d {
	case slots.Ship:
		return "ship"
	case slots.Pointer:
		return "pointer"
	case slots.Trim:
		return "trim"
	case slots.Skip:
	}
	return "skip"
}
