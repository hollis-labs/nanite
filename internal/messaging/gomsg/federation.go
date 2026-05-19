package gomsg

import (
	"context"
	"fmt"
	"sync"

	messaging "github.com/hollis-labs/go-messaging"
)

// Router is the authority-routing messaging.Store decorator — the
// federation seam described in docs/torque-messaging-design.md. It wraps
// a local Store and a registry of foreign-authority peer Stores, and
// dispatches address-keyed operations on the target's Authority:
//
//   - local authority   → the local Store
//   - registered peer   → that peer's Store
//   - unknown authority  → ErrStoreUnavailable
//
// A standalone Nanite install registers no peers, so every address is
// local and Router behaves exactly as the bare local Store — federation
// is purely additive and zero-config-by-default.
//
// Address-keyed operations (Send, Inbox, Subscribe) route on the target
// address. ID-keyed operations (Get, Thread, Consume, Cancel) take an
// opaque envelope id, not an address, and always target the local Store:
// cross-authority envelope-id resolution requires the federation
// transport + auth mechanism specified by task M2 (CW-20260518-0040) and
// is intentionally out of scope here. A caller holding a peer Store
// reference can invoke those operations on the peer directly.
type Router struct {
	local      string
	localStore messaging.Store

	mu    sync.RWMutex
	peers map[string]messaging.Store
}

// Compile-time assertion: Router satisfies the shared contract.
var _ messaging.Store = (*Router)(nil)

// NewRouter constructs a Router whose local authority is localAuthority
// (defaulting to DefaultAuthority when empty) backed by local. It starts
// with no peers registered — i.e. a standalone install.
func NewRouter(localAuthority string, local messaging.Store) *Router {
	if localAuthority == "" {
		localAuthority = DefaultAuthority
	}
	return &Router{
		local:      localAuthority,
		localStore: local,
		peers:      make(map[string]messaging.Store),
	}
}

// LocalAuthority returns the authority string this Router treats as local.
func (r *Router) LocalAuthority() string { return r.local }

// RegisterPeer wires a foreign authority to its peer Store (for example
// an HTTP-backed go-agentmux-client store). It rejects an empty
// authority, a nil store, and an attempt to register the local
// authority as a peer.
func (r *Router) RegisterPeer(authority string, store messaging.Store) error {
	if authority == "" || store == nil {
		return fmt.Errorf("gomsg: RegisterPeer requires a non-empty authority and a non-nil store")
	}
	if authority == r.local {
		return fmt.Errorf("gomsg: cannot register local authority %q as a peer", authority)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.peers[authority] = store
	return nil
}

// routeFor resolves the Store that owns addr.
func (r *Router) routeFor(addr messaging.Address) (messaging.Store, error) {
	if IsLocal(addr, r.local) {
		return r.localStore, nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	if st, ok := r.peers[addr.Authority]; ok {
		return st, nil
	}
	return nil, fmt.Errorf("gomsg: no route for authority %q: %w",
		addr.Authority, messaging.ErrStoreUnavailable)
}

// Send routes on the recipient address (env.To).
func (r *Router) Send(ctx context.Context, env messaging.Envelope) (messaging.Envelope, error) {
	st, err := r.routeFor(env.To)
	if err != nil {
		return messaging.Envelope{}, err
	}
	return st.Send(ctx, env)
}

// Inbox routes on the recipient address.
func (r *Router) Inbox(ctx context.Context, to messaging.Address, f messaging.Filter) ([]messaging.Envelope, error) {
	st, err := r.routeFor(to)
	if err != nil {
		return nil, err
	}
	return st.Inbox(ctx, to, f)
}

// Subscribe routes on the recipient address.
func (r *Router) Subscribe(ctx context.Context, to messaging.Address, f messaging.Filter) (<-chan messaging.Envelope, error) {
	st, err := r.routeFor(to)
	if err != nil {
		return nil, err
	}
	return st.Subscribe(ctx, to, f)
}

// Get targets the local Store — see the Router type doc.
func (r *Router) Get(ctx context.Context, id string) (messaging.Envelope, error) {
	return r.localStore.Get(ctx, id)
}

// Thread targets the local Store — see the Router type doc.
func (r *Router) Thread(ctx context.Context, threadID string, f messaging.Filter) ([]messaging.Envelope, error) {
	return r.localStore.Thread(ctx, threadID, f)
}

// Consume targets the local Store — see the Router type doc.
func (r *Router) Consume(ctx context.Context, id string, recipient messaging.Address) error {
	return r.localStore.Consume(ctx, id, recipient)
}

// Cancel targets the local Store — see the Router type doc.
func (r *Router) Cancel(ctx context.Context, id string) error {
	return r.localStore.Cancel(ctx, id)
}
