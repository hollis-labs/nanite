package plugin

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"slices"

	"github.com/hollis-labs/nanite/internal/plugin/subprocess"

	"github.com/hollis-labs/nanite/pkg/pluginapi"
)

type hostDurableWakeGrant struct {
	owner    string
	scope    pluginapi.DurableWakeScope
	ctx      context.Context
	cancel   context.CancelFunc
	lease    *subprocess.GrantLease
	validate func(context.Context) error
}

// HostDurableWakePermit is an authenticated, narrowed wake lease. Unload cancels its
// context so in-flight requests can stop; callers must not retain it for new calls.
type HostDurableWakePermit struct {
	PluginID string
	Scope    pluginapi.DurableWakeScope
	Context  context.Context
}

func cloneDurableWakeScope(scope pluginapi.DurableWakeScope) pluginapi.DurableWakeScope {
	scope.AgentSlugs = slices.Clone(scope.AgentSlugs)
	return scope
}

// prepareHostDurableWakeGrant allocates an unbound credential. BeforeSpawn binds it
// only after accepted bytes have been checked; a rejected load creates no grant.
func (h *Host) prepareHostDurableWakeGrant(owner string, scope pluginapi.DurableWakeScope) (pluginapi.DurableWakeGrant, error) {
	h.mu.RLock()
	origin := h.queryURL
	h.mu.RUnlock()
	if origin == "" {
		return pluginapi.DurableWakeGrant{}, fmt.Errorf("host durable wakes are unavailable")
	}
	var secret [32]byte
	if _, err := rand.Read(secret[:]); err != nil {
		return pluginapi.DurableWakeGrant{}, fmt.Errorf("allocate host durable wake credential: %w", err)
	}
	grant := pluginapi.DurableWakeGrant{Protocol: pluginapi.DurableWakeProtocol, PluginID: owner, HostURL: origin,
		Token: base64.RawURLEncoding.EncodeToString(secret[:]), Scope: cloneDurableWakeScope(scope)}
	return grant, grant.Validate()
}

func (h *Host) bindHostDurableWakeGrant(ctx context.Context, grant pluginapi.DurableWakeGrant) error {
	if err := grant.Validate(); err != nil {
		return err
	}
	hash := sha256.Sum256([]byte(grant.Token))
	h.mu.Lock()
	defer h.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := h.ctx.Err(); err != nil {
		return err
	}
	if grant.HostURL != h.queryURL {
		return fmt.Errorf("host durable wake origin changed before launch")
	}
	if existing, found := h.wakeGrants[hash]; found {
		if existing.owner != grant.PluginID {
			return fmt.Errorf("host durable wake credential owner mismatch")
		}
		return nil // supervised restart keeps the connection's accepted scope
	}
	for _, existing := range h.wakeGrants {
		if existing.owner == grant.PluginID {
			return fmt.Errorf("plugin already holds a host durable wake connection")
		}
	}
	if h.wakeGrants == nil {
		h.wakeGrants = make(map[[32]byte]hostDurableWakeGrant)
	}
	lifetime, _, policyErr := pluginGrantPolicy("bearer-connection")
	if policyErr != nil {
		return policyErr
	}
	lease, cancel := context.WithTimeout(h.ctx, lifetime)
	h.wakeGrants[hash] = hostDurableWakeGrant{owner: grant.PluginID, scope: cloneDurableWakeScope(grant.Scope), ctx: lease, cancel: cancel}
	return nil
}

func (h *Host) revokeHostDurableWakeGrant(token string) {
	hash := sha256.Sum256([]byte(token))
	h.mu.Lock()
	grant, exists := h.wakeGrants[hash]
	delete(h.wakeGrants, hash)
	h.mu.Unlock()
	if exists {
		grant.cancel()
		if grant.lease != nil {
			grant.lease.Revoke()
		}
	}
}

// AuthorizeHostDurableWake authenticates the connection, never a caller-supplied ID.
// Only hashes are retained in the host registry. The raw token travels solely
// through subprocess init and participates in the manager's secret redaction.
func (h *Host) AuthorizeHostDurableWake(token string) (HostDurableWakePermit, bool) {
	if len(token) != 43 {
		return HostDurableWakePermit{}, false
	}
	hash := sha256.Sum256([]byte(token))
	h.mu.RLock()
	grant, exists := h.wakeGrants[hash]
	h.mu.RUnlock()
	if !exists || grant.ctx.Err() != nil {
		return HostDurableWakePermit{}, false
	}
	if grant.validate != nil && grant.validate(h.ctx) != nil {
		if grant.lease != nil {
			grant.lease.Revoke()
		}
		return HostDurableWakePermit{}, false
	}
	permitContext := grant.ctx
	if grant.lease != nil {
		var err error
		permitContext, err = grant.lease.Context()
		if err != nil {
			return HostDurableWakePermit{}, false
		}
	}
	return HostDurableWakePermit{PluginID: grant.owner, Scope: cloneDurableWakeScope(grant.scope), Context: permitContext}, true
}

func (h *Host) attachHostDurableWakeLease(token string, lease *subprocess.GrantLease, validate func(context.Context) error) {
	hash := sha256.Sum256([]byte(token))
	h.mu.Lock()
	defer h.mu.Unlock()
	grant, ok := h.wakeGrants[hash]
	if ok {
		// The incarnation lease owns expiry from now on. Stop the initial
		// provisional bearer timeout so a valid renewal can extend it.
		grant.cancel()
		grant.ctx, grant.cancel = context.WithCancel(h.ctx)
		grant.lease = lease
		grant.validate = validate
		h.wakeGrants[hash] = grant
	}
}
