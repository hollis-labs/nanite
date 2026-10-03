package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/pkg/pluginapi"
	"github.com/hollis-labs/plugin-sdk/manifest"
	sdkprocess "github.com/hollis-labs/plugin-sdk/subprocess"
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
	if !manifest.ValidID(owner) || len(owner) > 63 {
		return fmt.Errorf("invalid plugin reflex owner")
	}
	raw, err := json.Marshal(scope)
	if err != nil {
		return err
	}
	block := pluginapi.Block{Registers: pluginapi.Registrations{ReflexSeeds: seeds}}
	if _, checkErr := pluginapi.ReflexScopeFor(block, []sdkprocess.CapabilityRequest{{Name: pluginapi.CapabilityReflexSeed, Metadata: raw}}); checkErr != nil {
		return checkErr
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.owners[owner]; ok {
		return fmt.Errorf("plugin reflex seeds already prepared")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	definitions := make([]store.PluginReflexSeed, 0, len(seeds))
	for _, seed := range seeds {
		agent, checkErr := r.store.GetAgentBySlug(ctx, seed.AgentSlug)
		if checkErr != nil {
			return fmt.Errorf("plugin reflex target %q: %w", seed.AgentSlug, checkErr)
		}
		if agent == nil {
			return fmt.Errorf("plugin reflex target %q unavailable", seed.AgentSlug)
		}
		trigger, checkErr := json.Marshal(seed.Trigger)
		if checkErr != nil {
			return checkErr
		}
		action, checkErr := json.Marshal(map[string]string{"body": seed.Reminder})
		if checkErr != nil {
			return checkErr
		}
		digest := sha256.Sum256([]byte(owner + "\x00" + seed.ID + "\x00" + agent.ID))
		row := store.AgentReflex{ID: "plugin-rfx-" + hex.EncodeToString(digest[:]), AgentID: agent.ID, Name: owner + ":" + seed.ID, TriggerKind: store.ReflexTriggerPredicate, TriggerSpec: string(trigger), ActionKind: store.ReflexActionInjectReminder, ActionSpec: string(action), Status: store.ReflexStatusActive, Priority: int64(seed.Priority), CreatedBy: "plugin:" + owner, ProvenanceTier: "plugin", OptOutAllowed: true}
		if problems := r.validator.ValidateDefinition(ctx, row); len(problems) != 0 {
			return fmt.Errorf("plugin reflex definition: %s", strings.Join(problems, "; "))
		}
		definitions = append(definitions, store.PluginReflexSeed{SeedID: seed.ID, Definition: row})
	}
	if owner == "nanite.loom" {
		names := map[string]struct{ slug, name string }{"check-before-answer": {"loom-weaver", "check_before_answer"}, "weaver-capture-on-discovery": {"loom-weaver", "capture_on_discovery"}, "curator-capture-on-discovery": {"loom-curator", "capture_on_discovery"}}
		lease := pluginSeedLease{}
		if len(seeds) != len(names) {
			return fmt.Errorf("loom adoption requires its three known pilot seeds")
		}
		for i, seed := range seeds {
			source, known := names[seed.ID]
			if !known || source.slug != seed.AgentSlug {
				return fmt.Errorf("unknown Loom pilot seed adoption target")
			}
			lease.existing = append(lease.existing, store.ExistingPluginReflexSeed{SeedID: seed.ID, AgentID: definitions[i].Definition.AgentID, LegacyName: source.name})
		}
		r.owners[owner] = lease
		return nil
	}
	rows, err := r.store.BindPluginReflexSeeds(ctx, owner, definitions)
	if err != nil {
		return err
	}
	lease := pluginSeedLease{}
	for _, row := range rows {
		lease.ids = append(lease.ids, row.ID)
	}
	r.owners[owner] = lease
	return nil
}
func (r *PluginReflexSeeds) ActivatePluginReflexSeeds(owner string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	lease, ok := r.owners[owner]
	if !ok {
		return fmt.Errorf("plugin reflex seeds not prepared")
	}
	if len(lease.existing) > 0 {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		rows, err := r.store.BindExistingPluginReflexSeeds(ctx, owner, lease.existing)
		if err != nil {
			return err
		}
		lease.ids = nil
		for _, row := range rows {
			lease.ids = append(lease.ids, row.ID)
		}
	}
	lease.active = true
	r.owners[owner] = lease
	r.publish()
	return nil
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
