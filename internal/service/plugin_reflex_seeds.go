package service

import (
	"sync"
	"sync/atomic"

	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/pkg/pluginapi"
)

type pluginSeedLease struct {
	ids      []string
	active   bool
	existing []store.ExistingPluginReflexSeed
}
type pluginSeedSnapshot map[string]string

// PluginReflexSeeds connects accepted declarations to core persistence and
// source eligibility. It never evaluates predicates or executes actions.
type PluginReflexSeeds struct {
	store     *store.Store
	validator *ReflexService
	mu        sync.Mutex
	owners    map[string]pluginSeedLease
	live      atomic.Pointer[pluginSeedSnapshot]
}

func NewPluginReflexSeeds(st *store.Store) *PluginReflexSeeds {
	r := &PluginReflexSeeds{store: st, validator: NewReflexService(st), owners: map[string]pluginSeedLease{}}
	r.publish()
	st.SetPluginReflexGate(func(row store.AgentReflex) bool {
		snapshot := r.live.Load()
		owner, ok := (*snapshot)[row.ID]
		return ok && row.ProvenanceTier == "plugin" && row.CreatedBy == "plugin:"+owner
	})
	return r
}
func (r *PluginReflexSeeds) PreparePluginReflexSeeds(owner string, seeds []pluginapi.ReflexSeed, scope pluginapi.ReflexScope) error {
	return store.ErrImmutableAgentProfile
}

func (r *PluginReflexSeeds) ActivatePluginReflexSeeds(owner string) error {
	return store.ErrImmutableAgentProfile
}

func (r *PluginReflexSeeds) RemovePluginReflexSeeds(owner string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.owners, owner)
	r.publish()
}
func (r *PluginReflexSeeds) publish() {
	snapshot := pluginSeedSnapshot{}
	for owner, lease := range r.owners {
		if lease.active {
			for _, id := range lease.ids {
				snapshot[id] = owner
			}
		}
	}
	r.live.Store(&snapshot)
}
