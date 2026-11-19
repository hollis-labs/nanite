package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"slices"
	"strings"
	"time"

	ctxpkg "github.com/hollis-labs/libs/agent/core/context"
	"github.com/hollis-labs/libs/agent/core/loopdetect"
	"github.com/hollis-labs/libs/agent/core/runloop"
	"github.com/hollis-labs/libs/agent/core/turn"
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
	// compactions counts compaction passes in this turn; stalled is set once
	// a pass frees too little, so later steps stop retrying it.
	compactions int
	stalled     bool
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
	// A panic while preparing the turn fails it with a ref; the host and the
	// conversation stay usable.
	err := e.panics.Call("turn", func() (err error) {
		result, err = e.turn(station.WithSessionID(ctx, info.ID), r, info, input, state, prev, hostTools, hostSkills, interrupted)
		return err
	}, panics.With("conversation", info.ID), panics.With("run", r.id))
	if err != nil {
		if clean := e.scrub.Text(err.Error()); clean != err.Error() {
			err = errors.New(clean)
		}
	}
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
	}
	finish()
}

func (e *Engine) turn(ctx context.Context, r *Run, info store.Conversation, input Input, state slots.Snapshot, prev map[string]string, hostTools []tools.Tool, hostSkills []skills.Skill, interrupted *store.InterruptedTurn) (_ events.Result, retErr error) {
	r.emit(ctx, func(meta events.EventMeta) events.Event { return events.RunStarted{EventMeta: meta} })
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
	use := definition.UseChat
	if info.ParentID != "" {
		use = definition.UseSubagent
	}
	model, modelName, err := e.provider(def, use)
	if err != nil {
		return events.Result{}, err
	}
	// Compaction summaries use their own selection, or else the provider and
	// model this turn runs on.
	summaryProvider, summaryModel := model, modelName
	if sel, ok := def.Use[definition.UseSummarizer]; ok {
		if summaryProvider, summaryModel, err = e.resolve(def, definition.UseSummarizer, sel); err != nil {
			return events.Result{}, err
		}
	}
	// The selected model is the one this turn asks for and reports in the
	// environment slot.
	def.Runtime.Model = modelName
	catalog := skills.NewCatalog(def.Skills, hostSkills)
	lease, warnings := e.mcp.Reconcile(ctx, def.Snapshot.MCP)
	defer lease.Close()
	mcpTools, discoveryWarnings := lease.Tools(ctx)
	warnings = append(warnings, discoveryWarnings...)
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
	assembled, err := e.assemble(ctx, input, slots.TurnInfo{ConversationID: info.ID, TurnID: r.id, UserText: input.Text, Role: info.Role}, def, state, prev, skillListing, pdp.Mode(), warnings, pending)
	if err != nil {
		return events.Result{}, err
	}
	x := execution{engine: e, run: r, info: info, definition: def, provider: model, assembled: assembled, policy: pdp, loop: loopdetect.New(),
		summaryProvider: summaryProvider, summaryModel: summaryModel}
	if err := x.prepareTools(hostTools, mcpTools, catalog); err != nil {
		return events.Result{}, err
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
		err = x.append(message)
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
		if x.result.Steps >= x.definition.Runtime.MaxSteps {
			x.result.StopReason = events.StopMaxSteps
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
	if !force && (x.stalled || !window.NeedsCompaction()) {
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
	summarizer := &capturingSummarizer{Summarizer: ctxpkg.NewProviderSummarizer(summaryProvider, summaryModel), messages: x.messages, messageIDs: x.messageIDs, model: x.engine.scrub.Text(summaryModel)}
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
			x.result.Usage.InputTokens += u.InputTokens
			x.result.Usage.OutputTokens += u.OutputTokens
			x.result.Usage.CacheReadTokens += u.CacheReadTokens
			x.result.Usage.CacheWriteTokens += u.CacheCreationTokens
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
