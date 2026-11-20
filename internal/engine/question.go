package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/hollis-labs/nanite/internal/elicitation"
	"github.com/hollis-labs/nanite/internal/events"
	"github.com/hollis-labs/nanite/internal/permission"
	"github.com/hollis-labs/nanite/internal/store"
)

type pendingQuestion struct {
	schema *jsonschema.Resolved
	answer chan json.RawMessage
}

func (r *Run) ask(ctx context.Context, prompt string, schema, displaySchema json.RawMessage, origin *permission.SubtaskOrigin) (json.RawMessage, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if strings.TrimSpace(prompt) == "" {
		return nil, errors.New("question prompt required")
	}
	if len(schema) == 0 {
		schema = json.RawMessage(`{"type":"string"}`)
		displaySchema = schema
	}
	resolved, err := elicitation.ResolveLimited(schema)
	if err != nil {
		return nil, fmt.Errorf("question schema: %w", err)
	}
	id := store.NewID(time.Now())
	pending := &pendingQuestion{schema: resolved, answer: make(chan json.RawMessage, 1)}
	r.questionMu.Lock()
	if r.questions == nil {
		r.questions = make(map[string]*pendingQuestion)
	}
	r.questions[id] = pending
	r.questionMu.Unlock()
	defer func() { r.questionMu.Lock(); delete(r.questions, id); r.questionMu.Unlock() }()
	r.emit(ctx, func(meta events.EventMeta) events.Event {
		return events.Question{EventMeta: meta, ID: id, Prompt: prompt, Schema: slices.Clone(displaySchema), Subtask: origin}
	})
	select {
	case answer := <-pending.answer:
		return answer, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// Answer submits a response to the pending question with the given ID.
func (r *Run) Answer(id string, answer events.Answer) error {
	r.questionMu.Lock()
	defer r.questionMu.Unlock()
	pending := r.questions[id]
	if pending == nil {
		return fmt.Errorf("nanite: no pending question %q", id)
	}
	if err := elicitation.ValidateResponse(pending.schema, answer.Value); err != nil {
		return err
	}
	pending.answer <- slices.Clone(answer.Value)
	delete(r.questions, id)
	return nil
}
