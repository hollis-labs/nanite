package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	sdkprocess "github.com/hollis-labs/libs/plugin-mcp/plugin-sdk/subprocess"
	"github.com/hollis-labs/nanite/internal/contextbroker"
	"github.com/hollis-labs/nanite/internal/plugin"
	"github.com/hollis-labs/nanite/internal/plugin/subprocess"
	"github.com/hollis-labs/nanite/pkg/pluginapi"
	ctxpkg "github.com/hollis-labs/substrate/agent/contextwindow"
)

const alwaysShipLateReserve = 256
const alwaysShipOwnerTokens = 1500

// PluginAlwaysShipSources owns reviewed leases independently of intent-skipped
// dynamic retrieval. Each turn snapshots leases; publication rechecks identity.
type PluginAlwaysShipSources struct {
	mu      sync.RWMutex
	sources map[string]*alwaysShipSource
}
type alwaysShipSource struct {
	owner       string
	declaration pluginapi.AlwaysShipSource
	scope       pluginapi.AlwaysShipScope
	caller      contextHTTPCaller
	check       func(context.Context) error
	lifetime    context.Context
	cancel      context.CancelFunc
	approved    *atomic.Bool
}

func NewPluginAlwaysShipSources() *PluginAlwaysShipSources {
	return &PluginAlwaysShipSources{sources: map[string]*alwaysShipSource{}}
}
func (r *PluginAlwaysShipSources) AddPluginAlwaysShipSources(owner string, declarations []pluginapi.AlwaysShipSource, scope pluginapi.AlwaysShipScope, child *subprocess.SubprocessPlugin, check func(context.Context) error) error {
	if child == nil || child.ID() != owner {
		return fmt.Errorf("always-ship sources require matching subprocess owner")
	}
	return r.add(owner, declarations, scope, child, check)
}
func (r *PluginAlwaysShipSources) add(owner string, declarations []pluginapi.AlwaysShipSource, scope pluginapi.AlwaysShipScope, caller contextHTTPCaller, check func(context.Context) error) error {
	if plugin.ValidatePluginID(owner) != nil || caller == nil || check == nil {
		return fmt.Errorf("always-ship sources require owner, caller and approval check")
	}
	if err := scope.Validate(); err != nil {
		return err
	}
	if err := (pluginapi.Block{Registers: pluginapi.Registrations{AlwaysShipSources: declarations}}).Validate(); err != nil {
		return err
	}
	if err := plugin.CheckAlwaysShipCoreTitles(declarations); err != nil {
		return err
	}
	if len(declarations) != len(scope.SourceIDs) {
		return fmt.Errorf("always-ship declarations differ from scope")
	}
	for _, source := range declarations {
		if !slices.Contains(scope.SourceIDs, source.ID) {
			return fmt.Errorf("always-ship declaration outside scope")
		}
	}
	scope.SourceIDs = slices.Clone(scope.SourceIDs)
	scope.SessionIDs = slices.Clone(scope.SessionIDs)
	r.mu.Lock()
	defer r.mu.Unlock()
	owners := map[string]bool{}
	for _, source := range r.sources {
		owners[source.owner] = true
	}
	if owners[owner] {
		return fmt.Errorf("always-ship owner already registered")
	}
	existing := map[string][]pluginapi.AlwaysShipSource{}
	for _, source := range r.sources {
		existing[source.owner] = append(existing[source.owner], source.declaration)
	}
	if err := plugin.CheckAlwaysShipAdmission(owner, declarations, existing); err != nil {
		return err
	}
	approval := &atomic.Bool{}
	approval.Store(true)
	for _, declaration := range declarations {
		lifetime, cancel := context.WithCancel(context.Background())
		r.sources[owner+"/"+declaration.ID] = &alwaysShipSource{owner: owner, declaration: declaration, scope: scope, caller: caller, check: check, lifetime: lifetime, cancel: cancel, approved: approval}
	}
	return nil
}
func (r *PluginAlwaysShipSources) RemovePluginAlwaysShipSources(owner string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for key, source := range r.sources {
		if source.owner == owner {
			source.cancel()
			delete(r.sources, key)
		}
	}
}

func (r *PluginAlwaysShipSources) snapshot(sessionID string) []*alwaysShipSource {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	var sources []*alwaysShipSource
	for _, source := range r.sources {
		if source.scope.Allows(source.declaration.ID, sessionID) {
			sources = append(sources, source)
		}
	}
	rank := func(title string) int {
		title = plugin.NormalizeAlwaysShipTitle(title)
		switch title {
		case "session documents":
			return 0
		case "pinned context":
			return 1
		default:
			return 2
		}
	}
	slices.SortFunc(sources, func(a, b *alwaysShipSource) int {
		if delta := rank(a.declaration.Title) - rank(b.declaration.Title); delta != 0 {
			return delta
		}
		if delta := strings.Compare(a.owner, b.owner); delta != 0 {
			return delta
		}
		return strings.Compare(a.declaration.ID, b.declaration.ID)
	})
	return sources
}

// Filter revoked authority before reserving core space or naming a fallback.
// A shared owner flag emits one diagnostic per approval transition, even when
// that owner declares multiple sources. Caller cancellation is not revocation.
func approvedAlwaysShipSources(ctx context.Context, sources []*alwaysShipSource) []*alwaysShipSource {
	active := make([]*alwaysShipSource, 0, len(sources))
	for _, source := range sources {
		err := source.check(ctx)
		if err != nil && ctx.Err() == nil {
			if source.approved.Swap(false) {
				slog.Warn("context-service: always-ship approval unavailable", "owner", source.owner, "err", err)
			}
			continue
		}
		if err == nil && !source.approved.Swap(true) {
			slog.Info("context-service: always-ship approval restored", "owner", source.owner)
		}
		active = append(active, source)
	}
	return active
}

func hasActiveAlwaysShipOwner(sources []*alwaysShipSource) bool {
	for _, source := range sources {
		if source.approved.Load() && source.lifetime.Err() == nil {
			return true
		}
	}
	return false
}

func alwaysShipFallback(sources []*alwaysShipSource) string {
	if len(sources) == 0 {
		return ""
	}
	entries := make([]string, 0, len(sources))
	for _, source := range sources {
		entries = append(entries, source.owner+"/"+source.declaration.ID+": use "+source.declaration.ListTool)
	}
	return "Additional context not included: " + strings.Join(entries, "; ") + " (requires an agent tool grant; if unavailable, open the plugin's UI)."
}
func joinAlwaysShip(parts ...string) string {
	var present []string
	for _, part := range parts {
		if part != "" {
			present = append(present, part)
		}
	}
	return strings.Join(present, "\n\n")
}
func alwaysShipEstimate(est ctxpkg.TokenEstimator, text string) int {
	// Round bytes up too: Window's byte clamp must never erase an admitted
	// section even when a custom estimator counts fewer tokens.
	return max(est.Estimate(text), contextbroker.EstimateTokens(text))
}
func alwaysShipReserve(sources []*alwaysShipSource, est ctxpkg.TokenEstimator) int {
	if len(sources) == 0 {
		return 0
	}
	return alwaysShipLateReserve + alwaysShipEstimate(est, "\n\n"+alwaysShipFallback(sources))
}

func (source *alwaysShipSource) fetch(ctx context.Context, intent contextbroker.Intent, maxBytes int) (string, error) {
	callCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	stop := context.AfterFunc(source.lifetime, cancel)
	defer stop()
	if source.lifetime.Err() != nil {
		return "", context.Canceled
	}
	if err := source.check(callCtx); err != nil {
		return "", err
	}
	if err := plugin.CheckAlwaysShipCoreTitles([]pluginapi.AlwaysShipSource{source.declaration}); err != nil {
		return "", err
	}
	body, err := json.Marshal(pluginapi.AlwaysShipRequest{Protocol: pluginapi.AlwaysShipProtocol, SourceID: source.declaration.ID, SessionID: intent.SessionID, AgentID: intent.AgentID, Intent: intent.Type, MaxBytes: maxBytes})
	if err != nil {
		return "", err
	}
	request := &sdkprocess.HTTPRequest{Method: "POST", Path: pluginapi.AlwaysShipFetchPath, SessionID: intent.SessionID, Body: body}
	if _, requestErr := pluginapi.DecodeAlwaysShipRequest(request); requestErr != nil {
		return "", requestErr
	}
	response, err := source.caller.CallHTTP(callCtx, request)
	if err != nil {
		return "", err
	}
	if callCtx.Err() != nil || source.lifetime.Err() != nil {
		return "", context.Canceled
	}
	if response == nil || response.Status != 200 {
		return "", fmt.Errorf("always-ship fetch failed")
	}
	decoded, err := pluginapi.DecodeAlwaysShipResponse(response.Body, maxBytes)
	return decoded.Body, err
}

// compose runs after core's stash decision. Every non-admitted source retains
// its fallback; bodies never acquire ordinary tool grants or a cache marker.
func (r *PluginAlwaysShipSources) compose(ctx context.Context, sources []*alwaysShipSource, core string, intent contextbroker.Intent, est ctxpkg.TokenEstimator, budget int) string {
	sources = approvedAlwaysShipSources(ctx, sources)
	if len(sources) == 0 {
		return core
	}
	limit := budget - alwaysShipLateReserve
	owners := map[string]bool{}
	for _, source := range sources {
		owners[source.owner] = true
	}
	bodyBytes, fallback := 0, true
	limited := alwaysShipEstimate(est, joinAlwaysShip(core, alwaysShipFallback(sources))) > limit
	defer func() {
		slog.Info("context-service: always-ship composition", "session_id", intent.SessionID, "owners", len(owners), "sources", len(sources), "bytes", bodyBytes, "fallback", fallback, "core_reserve_unavailable", limited)
	}()
	if limited {
		fallback = false
		// INV5: retain inline core when stashing failed. Its later Window clamp
		// can consume the whole slot; plugin fallback is not promised here.
		slog.Warn("context-service: always-ship reserve unavailable; preserving inline core, plugin context unavailable", "session_id", intent.SessionID)
		return core
	}
	fetchCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	replies := make(map[*alwaysShipSource]string)
	ownerBytes := map[string]int{}
	// Reserve the complete fallback until final publication. Exact combined
	// estimates admit responses; the byte allowance is only an upper bound.
	used := core
	ownerSections := map[string]string{}
	for _, source := range sources {
		heading := "## " + source.declaration.Title + "\n"
		remaining := limit - alwaysShipEstimate(est, joinAlwaysShip(used, heading, alwaysShipFallback(sources)))
		ownerRemaining := alwaysShipOwnerTokens - alwaysShipEstimate(est, joinAlwaysShip(ownerSections[source.owner], heading))
		allowance := min(min(source.scope.MaxBytes-ownerBytes[source.owner], max(0, remaining)*4), max(0, ownerRemaining)*4)
		if allowance <= 0 || fetchCtx.Err() != nil {
			continue
		}
		body, err := source.fetch(fetchCtx, intent, allowance)
		if err == nil {
			replies[source] = body
			ownerBytes[source.owner] += len(body)
			if body != "" {
				used = joinAlwaysShip(used, heading+body)
				ownerSections[source.owner] = joinAlwaysShip(ownerSections[source.owner], heading+body)
			}
		}
	}
	// Approval may have been revoked during another owner's fetch. Check it
	// again before publication, outside the registry lock.
	// The fetch deadline bounds collection, not publication of already-collected
	// bodies. The parent context still honors cancellation and shutdown.
	sources = approvedAlwaysShipSources(ctx, sources)
	r.mu.RLock()
	defer r.mu.RUnlock()
	pending := slices.Clone(sources)
	result := core
	ownerText := map[string]string{}
	for _, source := range sources {
		body, ok := replies[source]
		if !ok || source.lifetime.Err() != nil || r.sources[source.owner+"/"+source.declaration.ID] != source {
			continue
		}
		rest := slices.DeleteFunc(slices.Clone(pending), func(candidate *alwaysShipSource) bool { return candidate == source })
		section := ""
		if body != "" {
			section = "## " + source.declaration.Title + "\n" + body
		}
		ownerTrial := joinAlwaysShip(ownerText[source.owner], section)
		trial := joinAlwaysShip(result, section, alwaysShipFallback(rest))
		if alwaysShipEstimate(est, trial) > limit || alwaysShipEstimate(est, ownerTrial) > alwaysShipOwnerTokens {
			continue
		}
		result = joinAlwaysShip(result, section)
		ownerText[source.owner] = ownerTrial
		pending = rest
		bodyBytes += len(body)
	}
	fallback = len(pending) != 0
	return joinAlwaysShip(result, alwaysShipFallback(pending))
}
