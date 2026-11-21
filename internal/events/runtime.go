package events

import (
	"context"
	"log/slog"
	"os"
	"sync"

	"github.com/hollis-labs/libs/harness/core/adapters/runtimeevents"
)

type Runtime struct {
	sink     Sink
	logger   *slog.Logger
	mu       sync.Mutex
	sessions map[string]*runtimeSession
	seq      *runtimeevents.Sequencer
}

type runtimeSession struct {
	mu      sync.Mutex
	ready   bool
	emitter runtimeevents.Emitter
}

func NewRuntime(sink Sink, logger *slog.Logger) *Runtime {
	if sink == nil {
		return nil
	}
	return &Runtime{sink: sink, logger: logger, sessions: make(map[string]*runtimeSession), seq: runtimeevents.NewSequencer()}
}

func (r *Runtime) Observe(ctx context.Context, conversation, turn string, event Event) {
	if r == nil {
		return
	}
	r.mu.Lock()
	session := r.sessions[conversation]
	if session == nil {
		session = &runtimeSession{emitter: runtimeevents.Emitter{Sink: r.sink, App: "nanite", SessionID: runtimeevents.NewSessionID(), Sequencer: r.seq,
			Process: runtimeevents.Process{PID: os.Getpid(), Runtime: "native"}}}
		r.sessions[conversation] = session
	}
	r.mu.Unlock()
	session.mu.Lock()
	defer session.mu.Unlock()
	emit := func(kind runtimeevents.EventKind, payload map[string]any) {
		if payload == nil {
			payload = make(map[string]any)
		}
		payload["conversation_id"] = conversation
		err := session.emitter.Emit(ctx, kind, runtimeevents.Source{Channel: runtimeevents.SourceChannel("native"), Confidence: runtimeevents.ConfidenceExact},
			payload, runtimeevents.WithTurnID(turn))
		if err != nil {
			r.logger.WarnContext(ctx, "write runtime event failed", "kind", kind)
		}
	}
	switch e := event.(type) {
	case BudgetWarning:
		emit(runtimeevents.KindAgentDelta, map[string]any{"phase": "budget_warning", "day": e.Day, "tokens": e.Tokens, "limit": e.Limit, "text": "Daily token budget reached 80%."})
	case IntegrityChecked:
		codes := make(map[string]any, len(e.Codes))
		for code, n := range e.Codes {
			codes[code] = n
		}
		emit(runtimeevents.KindAgentDelta, map[string]any{"phase": "integrity", "stage": e.Stage, "disposition": e.Disposition,
			"severity": e.Severity, "codes": codes})
	case RunStarted:
		if !session.ready {
			emit(runtimeevents.KindSessionReady, nil)
			session.ready = true
		}
		emit(runtimeevents.KindSessionProcessing, nil)
		emit(runtimeevents.KindTurnStarted, nil)
	case TextDelta:
		emit(runtimeevents.KindAgentDelta, map[string]any{"text": e.Text})
	case Thinking:
		emit(runtimeevents.KindAgentDelta, map[string]any{"text": e.Text, "phase": "thinking"})
	case ToolCallStarted:
		emit(runtimeevents.KindAgentToolUse, map[string]any{"call_id": e.CallID, "name": e.Name, "input": e.Input})
	case ToolCallFinished:
		emit(runtimeevents.KindAgentToolResult, map[string]any{"call_id": e.CallID, "output": e.Result.Text, "is_error": e.IsError, "duration_ms": e.Duration.Milliseconds()})
	case SubagentStarted:
		emit(runtimeevents.KindAgentSubagentSpawn, map[string]any{"child_id": e.ID, "variant": e.Variant, "background": e.Background})
	case SubagentFinished:
		payload := map[string]any{"child_id": e.ID, "variant": e.Variant, "status": e.Status, "is_error": e.Status != "ok"}
		if e.Limit != "" {
			payload["limit"] = e.Limit
		}
		emit(runtimeevents.KindAgentSubagentStop, payload)
	case PermissionAsked:
		emit(runtimeevents.KindAgentPermissionRequested, map[string]any{"id": e.Request.ID, "tool": e.Request.Tool, "reason": e.Request.Reason})
	case PermissionResolved:
		payload := map[string]any{"id": e.ID, "allow": e.Decision.Allow, "reason": e.Decision.Reason}
		emit(runtimeevents.KindAgentPermissionResolved, payload)
		if !e.Decision.Allow {
			emit(runtimeevents.KindAgentPermissionDenied, map[string]any{"id": e.ID, "reason": e.Decision.Reason})
		}
	case RunFinished:
		emit(runtimeevents.KindTurnCompleted, map[string]any{"stop_reason": e.Result.StopReason, "steps": e.Result.Steps})
		emit(runtimeevents.KindSessionIdle, nil)
	case RunFailed:
		emit(runtimeevents.KindTurnFailed, map[string]any{"error": e.Err.Error()})
		emit(runtimeevents.KindSessionIdle, nil)
	}
}
