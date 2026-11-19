package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/hollis-labs/libs/agent/trace"
	"github.com/hollis-labs/libs/core/panics"
	"github.com/hollis-labs/nanite/internal/events"
	"github.com/hollis-labs/nanite/internal/slots"
	"github.com/hollis-labs/nanite/internal/store"
)

// newTraceRecord starts a turn's trace record with what is known up front.
func newTraceRecord(at time.Time, run string, info store.Conversation, resumed bool) trace.Turn {
	source := trace.SourceUser
	switch {
	case resumed:
		source = trace.SourceResume
	case info.ParentID != "":
		source = trace.SourceSubagent
	}
	return trace.Turn{Version: trace.Version, At: at, ConversationID: info.ID, RunID: run, TurnID: run,
		ParentConversationID: info.ParentID, Role: info.Role, Source: source,
		Slots: []trace.Slot{}, Trimmed: []string{}, ToolsOffered: []string{}, ToolCalls: []trace.ToolCall{}, Compactions: []trace.Compaction{}}
}

// traceSlots lists the shipped slots in request order, and the entries the
// selector cut or replaced with a pointer as "slot/owner/key".
func traceSlots(assembled slots.Assembled) ([]trace.Slot, []string) {
	out := []trace.Slot{}
	for _, block := range assembled.Blocks {
		report := assembled.Report[slots.Name(block.SlotName)]
		out = append(out, trace.Slot{Name: block.SlotName, Tokens: report.Tokens, Changed: report.Changed})
	}
	trimmed := []string{}
	for _, name := range slots.Names() {
		for _, d := range assembled.Report[name].Decisions {
			if d.Disposition != slots.Ship {
				trimmed = append(trimmed, string(name)+"/"+d.Owner+"/"+d.Key)
			}
		}
	}
	return out, trimmed
}

// writeTrace completes a turn's record and hands it to every sink: the daily
// JSONL file, the host's sink and the turn span. A sink failure is logged and
// never fails the turn.
func (e *Engine) writeTrace(ctx context.Context, rec trace.Turn, x *execution, result events.Result, err error) {
	rec.DurationMS = time.Since(rec.At).Milliseconds()
	if x != nil {
		for name := range x.catalog {
			rec.ToolsOffered = append(rec.ToolsOffered, e.scrub.Text(name))
		}
		slices.Sort(rec.ToolsOffered)
		rec.ToolCalls = append(rec.ToolCalls, x.toolCalls...)
		rec.Compactions = append(rec.Compactions, x.passes...)
	}
	u := result.Usage
	rec.Usage = trace.Usage{InputTokens: u.InputTokens, OutputTokens: u.OutputTokens, CacheReadTokens: u.CacheReadTokens, CacheWriteTokens: u.CacheWriteTokens}
	rec.Outcome = trace.Outcome{Status: trace.StatusCompleted, FinishReason: result.StopReason, Steps: result.Steps}
	if err != nil {
		rec.Outcome.Status = trace.StatusFailed
		if ctx.Err() != nil || errors.Is(err, context.Canceled) {
			rec.Outcome.Status = trace.StatusCancelled
		}
		rec.Outcome.Error = e.scrub.Text(err.Error())
	}
	if verr := rec.Validate(); verr != nil {
		e.cfg.Logger.WarnContext(ctx, "nanite: trace record invalid", "error", verr)
		return
	}
	events.SetTurnAttributes(ctx, rec.Attributes())
	if e.cfg.TraceDir != "" {
		if werr := e.appendTrace(rec); werr != nil {
			e.cfg.Logger.WarnContext(ctx, "nanite: write trace record", "error", e.scrub.Text(werr.Error()))
		}
	}
	if sink := e.cfg.TraceSink; sink != nil {
		_ = e.panics.Call("trace sink", func() error { sink(context.WithoutCancel(ctx), rec); return nil }, panics.With("conversation", rec.ConversationID))
	}
}

// appendTrace appends rec to the day's JSONL file under TraceDir.
func (e *Engine) appendTrace(rec trace.Turn) error {
	line, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	e.traceMu.Lock()
	defer e.traceMu.Unlock()
	if err := store.PreparePrivateDir(e.cfg.TraceDir); err != nil {
		return err
	}
	path := filepath.Join(e.cfg.TraceDir, rec.At.Format("2006-01-02")+".jsonl")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600) // #nosec G304 -- The path is the engine's own trace directory and a date.
	if err != nil {
		return err
	}
	if _, err := file.Write(append(line, '\n')); err != nil {
		_ = file.Close()
		return fmt.Errorf("append trace: %w", err)
	}
	return file.Close()
}
