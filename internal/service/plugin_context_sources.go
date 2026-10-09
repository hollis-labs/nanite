package service

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"sync"
	"time"

	sdkprocess "github.com/hollis-labs/libs/plugin-mcp/plugin-sdk/subprocess"
	"github.com/hollis-labs/nanite/internal/contextbroker"
	"github.com/hollis-labs/nanite/internal/plugin/subprocess"
	"github.com/hollis-labs/nanite/pkg/pluginapi"
)

type contextHTTPCaller interface {
	CallHTTP(context.Context, *sdkprocess.HTTPRequest) (*sdkprocess.HTTPResponse, error)
}

type pluginContextSource struct {
	owner, id string
	scope     pluginapi.ContextScope
	caller    contextHTTPCaller
	lifetime  context.Context
	cancel    context.CancelFunc
}

// PluginContextSources is one permanent broker source with a mutable, owned
// plugin registry. Broker source order and fixed prompt slots stay unchanged.
type PluginContextSources struct {
	mu      sync.RWMutex
	sources map[string]*pluginContextSource
}

func NewPluginContextSources() *PluginContextSources {
	return &PluginContextSources{sources: map[string]*pluginContextSource{}}
}
func (*PluginContextSources) Name() string { return "plugin-context" }

func (r *PluginContextSources) AddPluginContextSources(owner string, declarations []pluginapi.ContextSource, scope pluginapi.ContextScope, child *subprocess.SubprocessPlugin) error {
	if child == nil || child.ID() != owner {
		return fmt.Errorf("context sources require matching subprocess owner")
	}
	return r.add(owner, declarations, scope, child)
}

func (r *PluginContextSources) add(owner string, declarations []pluginapi.ContextSource, scope pluginapi.ContextScope, caller contextHTTPCaller) error {
	if owner == "" || caller == nil {
		return fmt.Errorf("context sources require an owner and caller")
	}
	if err := scope.Validate(); err != nil {
		return err
	}
	if len(declarations) != len(scope.SourceIDs) || len(declarations) == 0 {
		return fmt.Errorf("context source declarations differ from scope")
	}
	block := pluginapi.Block{Registers: pluginapi.Registrations{ContextSources: declarations}}
	raw, err := json.Marshal(scope)
	if err != nil {
		return err
	}
	if _, err := pluginapi.ContextScopeFor(block, []sdkprocess.CapabilityRequest{{Name: pluginapi.CapabilityContextSource, Metadata: raw}}); err != nil {
		return err
	}
	// Scope slices belong to the accepted snapshot, never the caller's maps.
	scope.SourceIDs = append([]string(nil), scope.SourceIDs...)
	scope.SessionIDs = append([]string(nil), scope.SessionIDs...)
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, source := range r.sources {
		if source.owner == owner {
			return fmt.Errorf("plugin context sources already registered")
		}
	}
	for _, declaration := range declarations {
		lifetime, cancel := context.WithCancel(context.Background())
		key := "plugin/" + owner + "/" + declaration.ID
		r.sources[key] = &pluginContextSource{owner: owner, id: declaration.ID, scope: scope, caller: caller, lifetime: lifetime, cancel: cancel}
	}
	return nil
}

func (r *PluginContextSources) RemovePluginContextSources(owner string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for key, source := range r.sources {
		if source.owner == owner {
			source.cancel()
			delete(r.sources, key)
		}
	}
}

func (r *PluginContextSources) Fetch(ctx context.Context, intent contextbroker.Intent, budget int) ([]contextbroker.ContextItem, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if budget <= 0 {
		return nil, nil
	}
	r.mu.RLock()
	sources := make(map[string]*pluginContextSource, len(r.sources))
	var keys []string
	for key, source := range r.sources {
		if source.scope.Allows(source.id, intent.SessionID) {
			sources[key] = source
			keys = append(keys, key)
		}
	}
	r.mu.RUnlock()
	sort.Strings(keys)
	if len(keys) == 0 {
		return nil, nil
	}
	fetchCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	share := min(budget/len(keys), 50000)
	if share <= 0 {
		return nil, nil
	}
	var items []contextbroker.ContextItem
	for _, key := range keys {
		if fetchCtx.Err() != nil {
			break
		}
		source := sources[key]
		retrieved, err := source.fetch(fetchCtx, intent, share, key)
		if err == nil {
			items = append(items, retrieved...)
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// Linearize publication with registry removal/replacement. A response from
	// an earlier plugin lifetime cannot survive an unload followed by reload.
	r.mu.RLock()
	active := items[:0]
	for _, item := range items {
		if current := r.sources[item.Source]; current != nil && current == sources[item.Source] && current.lifetime.Err() == nil {
			active = append(active, item)
		}
	}
	r.mu.RUnlock()
	return active, nil
}

func (s *pluginContextSource) fetch(ctx context.Context, intent contextbroker.Intent, budget int, key string) ([]contextbroker.ContextItem, error) {
	callCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	stop := context.AfterFunc(s.lifetime, cancel)
	defer stop()
	if s.lifetime.Err() != nil {
		return nil, context.Canceled
	}
	request := pluginapi.ContextRequest{Protocol: pluginapi.ContextProtocol, SourceID: s.id, SessionID: intent.SessionID, AgentID: intent.AgentID, Intent: intent.Type, TokenBudget: budget}
	if s.scope.IncludeQuery {
		request.QueryText = intent.QueryText
		request.Keywords = append([]string(nil), intent.Keywords...)
	}
	body, err := json.Marshal(request)
	if err != nil || len(body) > pluginapi.MaxContextBytes {
		return nil, fmt.Errorf("context request exceeds limit")
	}
	response, err := s.caller.CallHTTP(callCtx, &sdkprocess.HTTPRequest{Method: "POST", Path: pluginapi.ContextFetchPath, SessionID: intent.SessionID, Body: body})
	if err != nil {
		return nil, err
	}
	if callCtx.Err() != nil || s.lifetime.Err() != nil {
		return nil, context.Canceled
	}
	if response == nil || response.Status != 200 {
		return nil, fmt.Errorf("plugin context retrieval failed")
	}
	decoded, err := pluginapi.DecodeContextResponse(response.Body)
	if err != nil {
		return nil, err
	}
	var items []contextbroker.ContextItem
	remaining := budget
	for _, item := range decoded.Items {
		// Estimate from host-observed bytes; plugins cannot bypass budgets by
		// supplying a zero or negative token count.
		tokens := len(item.Content) + len(item.Key) + len(key) + 64
		if tokens > remaining {
			continue
		}
		items = append(items, contextbroker.ContextItem{Source: key, Key: item.Key, Content: item.Content, Relevance: item.Relevance, TokenEstimate: tokens})
		remaining -= tokens
	}
	if callCtx.Err() != nil || s.lifetime.Err() != nil {
		return nil, context.Canceled
	}
	return items, nil
}
