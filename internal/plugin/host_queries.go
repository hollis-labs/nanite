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

type hostQueryGrant struct {
	owner    string
	scope    pluginapi.QueryScope
	ctx      context.Context
	cancel   context.CancelFunc
	lease    *subprocess.GrantLease
	validate func(context.Context) error
}

// HostQueryPermit is an authenticated, narrowed read lease. Unload cancels its
// context so in-flight reads can stop; callers must not retain it for new calls.
type HostQueryPermit struct {
	PluginID string
	Scope    pluginapi.QueryScope
	Context  context.Context
}

// SetHostQueryURL wires the literal loopback origin before discovery. It is
// separate from serving: the API enforces these credentials at its core route.
func (h *Host) SetHostQueryURL(origin string) error {
	if _, err := pluginapi.NewClient(origin, nil); err != nil {
		return err
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.queryGrants) != 0 || len(h.wakeGrants) != 0 {
		return fmt.Errorf("cannot change host query origin while grants exist")
	}
	h.queryURL = origin
	return nil
}

func cloneQueryScope(scope pluginapi.QueryScope) pluginapi.QueryScope {
	scope.Resources = slices.Clone(scope.Resources)
	scope.SessionIDs = slices.Clone(scope.SessionIDs)
	return scope
}

// prepareHostQueryGrant allocates an unbound credential. BeforeSpawn binds it
// only after accepted bytes have been checked; a rejected load creates no grant.
func (h *Host) prepareHostQueryGrant(owner string, scope pluginapi.QueryScope) (pluginapi.QueryGrant, error) {
	h.mu.RLock()
	origin := h.queryURL
	h.mu.RUnlock()
	if origin == "" {
		return pluginapi.QueryGrant{}, fmt.Errorf("host read-only queries are unavailable")
	}
	var secret [32]byte
	if _, err := rand.Read(secret[:]); err != nil {
		return pluginapi.QueryGrant{}, fmt.Errorf("allocate host query credential: %w", err)
	}
	grant := pluginapi.QueryGrant{Protocol: pluginapi.QueryProtocol, PluginID: owner, HostURL: origin,
		Token: base64.RawURLEncoding.EncodeToString(secret[:]), Scope: cloneQueryScope(scope)}
	return grant, grant.Validate()
}

func (h *Host) bindHostQueryGrant(ctx context.Context, grant pluginapi.QueryGrant) error {
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
		return fmt.Errorf("host query origin changed before launch")
	}
	if existing, found := h.queryGrants[hash]; found {
		if existing.owner != grant.PluginID {
			return fmt.Errorf("host query credential owner mismatch")
		}
		return nil // supervised restart keeps the connection's accepted scope
	}
	for _, existing := range h.queryGrants {
		if existing.owner == grant.PluginID {
			return fmt.Errorf("plugin already holds a host query connection")
		}
	}
	if h.queryGrants == nil {
		h.queryGrants = make(map[[32]byte]hostQueryGrant)
	}
	lifetime, _, policyErr := pluginGrantPolicy("bearer-connection")
	if policyErr != nil {
		return policyErr
	}
	lease, cancel := context.WithTimeout(h.ctx, lifetime)
	h.queryGrants[hash] = hostQueryGrant{owner: grant.PluginID, scope: cloneQueryScope(grant.Scope), ctx: lease, cancel: cancel}
	return nil
}

func (h *Host) revokeHostQueryGrant(token string) {
	hash := sha256.Sum256([]byte(token))
	h.mu.Lock()
	grant, exists := h.queryGrants[hash]
	delete(h.queryGrants, hash)
	h.mu.Unlock()
	if exists {
		grant.cancel()
		if grant.lease != nil {
			grant.lease.Revoke()
		}
	}
}

// AuthorizeHostQuery authenticates the connection, never a caller-supplied ID.
// Only hashes are retained in the host registry. The raw token travels solely
// through subprocess init and participates in the manager's secret redaction.
func (h *Host) AuthorizeHostQuery(token string) (HostQueryPermit, bool) {
	if len(token) != 43 {
		return HostQueryPermit{}, false
	}
	hash := sha256.Sum256([]byte(token))
	h.mu.RLock()
	grant, exists := h.queryGrants[hash]
	h.mu.RUnlock()
	if !exists || grant.ctx.Err() != nil {
		return HostQueryPermit{}, false
	}
	if grant.validate != nil && grant.validate(h.ctx) != nil {
		if grant.lease != nil {
			grant.lease.Revoke()
		}
		return HostQueryPermit{}, false
	}
	permitContext := grant.ctx
	if grant.lease != nil {
		var err error
		permitContext, err = grant.lease.Context()
		if err != nil {
			return HostQueryPermit{}, false
		}
	}
	return HostQueryPermit{PluginID: grant.owner, Scope: cloneQueryScope(grant.scope), Context: permitContext}, true
}

func (h *Host) attachHostQueryLease(token string, lease *subprocess.GrantLease, validate func(context.Context) error) {
	hash := sha256.Sum256([]byte(token))
	h.mu.Lock()
	defer h.mu.Unlock()
	grant, ok := h.queryGrants[hash]
	if ok {
		// The incarnation lease owns expiry from now on. Stop the initial
		// provisional bearer timeout so a valid renewal can extend it.
		grant.cancel()
		grant.ctx, grant.cancel = context.WithCancel(h.ctx)
		grant.lease = lease
		grant.validate = validate
		h.queryGrants[hash] = grant
	}
}
