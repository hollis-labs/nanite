package mcpclient

import (
	"context"
	"encoding/json"
	"sync"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

type Ask func(context.Context, string, json.RawMessage) (json.RawMessage, error)
type askKey struct{}

func WithAsk(ctx context.Context, ask Ask) context.Context {
	return context.WithValue(ctx, askKey{}, ask)
}

type callQuestion struct {
	mu  sync.Mutex
	ask Ask
	ctx context.Context
}

func (r *registration) bind(ctx context.Context) (func(), error) {
	select {
	case r.callSlot <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	ask, _ := ctx.Value(askKey{}).(Ask)
	r.question.mu.Lock()
	r.question.ask, r.question.ctx = ask, ctx
	r.question.mu.Unlock()
	return func() {
		r.question.mu.Lock()
		r.question.ask, r.question.ctx = nil, nil
		r.question.mu.Unlock()
		<-r.callSlot
	}, nil
}

func (c *Client) elicit(key string) func(context.Context, *sdk.ElicitRequest) (*sdk.ElicitResult, error) {
	return func(ctx context.Context, req *sdk.ElicitRequest) (*sdk.ElicitResult, error) {
		value, ok := c.byKey.Load(key)
		if !ok || req == nil || req.Params == nil || req.Params.Mode == "url" || req.Params.URL != "" {
			return &sdk.ElicitResult{Action: "decline"}, nil
		}
		reg := value.(*registration)
		reg.question.mu.Lock()
		ask, callCtx := reg.question.ask, reg.question.ctx
		reg.question.mu.Unlock()
		if ask == nil {
			return &sdk.ElicitResult{Action: "decline"}, nil
		}
		schema, err := json.Marshal(req.Params.RequestedSchema)
		if err != nil {
			return nil, err
		}
		if req.Params.RequestedSchema == nil {
			schema = []byte(`{"type":"object"}`)
		}
		questionCtx, cancel := context.WithCancel(callCtx)
		stop := context.AfterFunc(ctx, cancel)
		defer func() { stop(); cancel() }()
		valueJSON, err := ask(questionCtx, "MCP server "+reg.name+": "+req.Params.Message, schema)
		if err != nil {
			if questionCtx.Err() != nil {
				return &sdk.ElicitResult{Action: "cancel"}, nil
			}
			return nil, err
		}
		var answer map[string]any
		if err := json.Unmarshal(valueJSON, &answer); err != nil {
			return nil, err
		}
		if answer == nil {
			return &sdk.ElicitResult{Action: "decline"}, nil
		}
		return &sdk.ElicitResult{Action: "accept", Content: answer}, nil
	}
}
