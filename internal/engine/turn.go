package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	ctxpkg "github.com/hollis-labs/libs/agent/core/context"
	"github.com/hollis-labs/libs/agent/core/loopdetect"
	"github.com/hollis-labs/libs/agent/core/runloop"
	"github.com/hollis-labs/libs/agent/core/turn"
	"github.com/hollis-labs/libs/llm/core/llmcontracts"
	"github.com/hollis-labs/libs/llm/core/llmtypes"
	"github.com/hollis-labs/libs/security/policy"
	"github.com/hollis-labs/nanite/internal/definition"
	"github.com/hollis-labs/nanite/internal/events"
	"github.com/hollis-labs/nanite/internal/permission"
	"github.com/hollis-labs/nanite/internal/provider/station"
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
	assembled  slots.Assembled
	policy     policy.PDP
	journal    *store.Journal
	messages   []llmtypes.ChatMessage
	parent     *string
	catalogue  map[string]tools.Tool
	specs      []llmtypes.ToolDefinition
	loop       *loopdetect.Detector
	result     events.Result
}

func (e *Engine) execute(ctx context.Context, r *Run, info store.Conversation, input Input, state slots.Snapshot, prev map[string]string, hostTools []tools.Tool, skills string) {
	result, err := e.turn(station.WithSessionID(ctx, info.ID), r, info, input, state, prev, hostTools, skills)
	if err != nil {
		if clean := e.scrub.Text(err.Error()); clean != err.Error() {
			err = errors.New(clean)
		}
	}
	r.cancel()
	e.mu.Lock()
	delete(e.active, info.ID)
	r.finish(result, err)
	e.mu.Unlock()
}

func (e *Engine) turn(ctx context.Context, r *Run, info store.Conversation, input Input, state slots.Snapshot, prev map[string]string, hostTools []tools.Tool, skills string) (events.Result, error) {
	r.emit(ctx, func(meta events.EventMeta) events.Event { return events.RunStarted{EventMeta: meta} })
	def, err := definition.Load(e.cfg.Dir, e.cfg.GlobalSkillsDir)
	if err != nil {
		return events.Result{}, err
	}
	pdp, err := permission.Rules(def.Snapshot.Profile.Permissions.Mode, def.Rules)
	if err != nil {
		return events.Result{}, err
	}
	model, err := e.provider(def)
	if err != nil {
		return events.Result{}, err
	}
	assembled, err := e.assemble(ctx, input, slots.TurnInfo{ConversationID: info.ID, TurnID: r.id, UserText: input.Text, Role: info.Role}, def, state, prev, skills)
	if err != nil {
		return events.Result{}, err
	}
	x := execution{engine: e, run: r, info: info, definition: def, provider: model, assembled: assembled, policy: pdp, loop: loopdetect.New()}
	if err := x.prepareTools(hostTools); err != nil {
		return events.Result{}, err
	}
	journal, err := e.cfg.Store.OpenJournal(ctx, info.ID)
	if err != nil {
		return events.Result{}, err
	}
	defer journal.Close()
	x.journal = journal
	history, err := e.cfg.Store.History(ctx, info.ID)
	if err != nil {
		return events.Result{}, err
	}
	for _, m := range history {
		x.addMessage(m)
	}

	now := time.Now().UTC()
	record := store.Turn{ID: r.id, UserMessageID: store.NewID(now)}
	if err := journal.StartTurn(record, now); err != nil {
		return events.Result{}, err
	}
	message := store.Message{ID: record.UserMessageID, TurnID: r.id, Role: "user", CreatedAt: now, Blocks: []llmtypes.ContentBlock{{Type: "text", Text: e.scrub.Text(input.Text)}}}

	if err = x.append(message); err == nil {
		e.mu.Lock()
		e.previous[info.ID] = assembled.Hashes
		e.mu.Unlock()
		r.emit(ctx, func(meta events.EventMeta) events.Event {
			return events.SlotsAssembled{EventMeta: meta, Report: assembled.Report}
		})
		err = x.generate(ctx)
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

func (x *execution) addMessage(message store.Message) {
	role := message.Role
	if role == "tool" {
		role = "user"
	}
	x.messages = append(x.messages, llmtypes.ChatMessage{Role: role, ContentBlocks: message.Blocks})
	x.parent = &message.ID
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
			SlotBlocks: blocks, Messages: x.messages, Tools: x.specs, CacheHints: []llmtypes.CacheHint{{Position: "system"}}}
		callCtx, cancel := context.WithCancel(ctx)
		stream, err := x.provider.StreamChat(callCtx, req)
		if err != nil {
			cancel()
			return attempt{}, x.retry(ctx, err, &retries, &runErr)
		}
		return attempt{events: stream, cancel: cancel}, runloop.Proceed
	}
	consume := func(ctx context.Context, a attempt) (step, runloop.Directive) {
		defer a.cancel()
		got, err := x.consume(ctx, a.events)
		if err != nil {
			if !got.emitted {
				return step{}, x.retry(ctx, err, &retries, &runErr)
			}
			allText.WriteString(got.text)
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
		allText.WriteString(got.text)
		x.result.Steps++
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
			x.result.StopReason = "max_steps"
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
	if ctx.Err() == nil && *retries < 2 {
		*retries++
		if ctxpkg.IsContextOverflow(err) {
			if compactErr := x.compact(ctx, true); compactErr == nil {
				return runloop.Retry
			} else {
				err = errors.Join(err, compactErr)
			}
		} else if strings.Contains(strings.ToLower(err.Error()), "rate limit") || strings.Contains(strings.ToLower(err.Error()), "overload") {
			select {
			case <-time.After(time.Duration(*retries) * 200 * time.Millisecond):
				return runloop.Retry
			case <-ctx.Done():
				err = ctx.Err()
			}
		}
	}
	*runErr = err
	return runloop.Terminate
}

func (x *execution) compact(ctx context.Context, force bool) error {
	est := ctxpkg.DefaultEstimator{}
	raw, err := json.Marshal(x.messages)
	if err != nil {
		return err
	}
	window := x.assembled.Window
	window.SetContent(ctxpkg.SlotConversation, string(raw))
	if !force && !window.NeedsCompaction() {
		return nil
	}
	before := est.Estimate(string(raw))
	pipeline := ctxpkg.CompactionPipeline{Window: window, Estimator: est, ConversationMessages: x.messages,
		Summarizer: ctxpkg.NewProviderSummarizer(x.provider, x.definition.Runtime.Model), SessionID: x.info.ID}
	var result *ctxpkg.CompactionResult
	if force {
		result, err = pipeline.RunForce(ctx)
	} else {
		result, err = pipeline.Run(ctx)
	}
	if err != nil {
		return err
	}
	x.messages = pipeline.ConversationMessages
	if result != nil && result.TokensSaved > 0 {
		now := time.Now().UTC()
		payload := struct {
			TurnID        string
			Before, After int
			Summary       string
		}{
			x.run.id, before, max(0, before-result.TokensSaved), x.engine.scrub.Text(result.Summary),
		}
		if err := x.journal.RecordCompaction(store.NewID(now), now, payload); err != nil {
			return err
		}
		x.run.emit(ctx, func(meta events.EventMeta) events.Event {
			return events.Compacted{EventMeta: meta, Before: before, After: max(0, before-result.TokensSaved)}
		})
	}
	return nil
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
			return got, errors.New(ev.Error)
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
			got.blocks = append(got.blocks, llmtypes.ContentBlock{Type: "thinking", Text: text, Signature: ev.ThinkingBlock.Signature})
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
