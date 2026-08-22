package skillinstall

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"github.com/hollis-labs/nanite/internal/skill"
	"github.com/hollis-labs/nanite/internal/skillvendor"
	"github.com/hollis-labs/nanite/internal/store"
)

// State is the current position in the install/sync state machine.
type State string

const (
	StateNotInstalled State = "not_installed"
	StateParsing      State = "parsing"
	StateValidating   State = "validating"
	StateVendoring    State = "vendoring"
	StateIndexing     State = "indexing"
	StateReady        State = "ready"
	StateFailed       State = "failed"
)

// Event is emitted on each state transition.
type Event struct {
	Slug    string
	State   State
	Message string
	Err     error
}

// EventFunc consumes install events. Pass nil to ignore.
type EventFunc func(Event)

// Source describes the single package this Install call targets — a local
// directory on disk containing a SKILL.md file at its root (this batch's
// scope; see TASKS/skills/README.md's ecosystem-format-adaptation scope
// fence for what's deliberately not built here).
type Source struct {
	// Path is the absolute (or process-relative, caller's responsibility)
	// filesystem directory to parse and install/sync.
	Path string
}

// Validator confirms a parsed package matches the real Agent-Skills-spec
// shape before anything is vendored or indexed. See DefaultValidator for
// the concrete rules this package enforces when no Validator is injected.
type Validator interface {
	Validate(def *skill.Definition, files skill.PackageFiles) error
}

// Vendorer is the narrow slice of internal/skillvendor.(*Store)'s API this
// package depends on. A real *skillvendor.Store satisfies this directly;
// interfacing it lets tests substitute a fake for failure-path coverage
// without hand-corrupting a real vendored directory on disk.
type Vendorer interface {
	Write(ctx context.Context, files skillvendor.FileMap) (skillvendor.WriteResult, error)
}

// vendorDeleter is an optional refinement of Vendorer. When the Indexing
// step fails after a fresh (non-Reused) vendor write, Install attempts to
// roll that write back via Delete, so a failed install never leaves a
// vendored address behind with no index row pointing at it. A Reused
// write is never rolled back this way — its content already existed
// before this call (either from a prior install of this same package, or
// coincidentally shared with something else already vendored), so an
// unrelated indexing failure must not delete it. A Vendorer that doesn't
// implement this (e.g. a minimal test fake) simply skips the rollback:
// the resulting orphaned vendored content is inert — unreferenced by any
// index row, safe to garbage-collect later — not a correctness hazard.
type vendorDeleter interface {
	Delete(address string) error
}

// IndexStore is the narrow slice of *store.Store's API this package
// depends on for the index-upsert step. A real *store.Store satisfies
// this directly.
type IndexStore interface {
	GetSkillBySlug(slug string) (*store.Skill, error)
	CreateSkill(sk *store.Skill) error
	UpdateSkill(sk *store.Skill) error
}

// Result is what a successful Install call returns.
type Result struct {
	// Skill is the resulting index row — freshly created on a first
	// install, or the updated existing row on a re-sync.
	Skill store.Skill

	// Address is the vendored content address the package's files were
	// written to (or already existed at), matching Skill.ContentHash.
	Address string

	// Reused reports whether the vendored content already existed
	// (byte-identical) before this call — true for a no-op re-sync of
	// unchanged content, or for a fresh install whose exact file tree
	// happens to already be vendored under a different skill.
	Reused bool
}

// Installer drives the explicit, single-target install/sync pipeline:
// Parse → Validate → Vendor → Index. See the package doc for the full
// state-machine rationale and the failure-handling/rollback contract.
type Installer struct {
	Vendor Vendorer
	Index  IndexStore

	// Validate overrides the default validation rules
	// (DefaultValidator{}) when set.
	Validate Validator

	// MaxDependencyDepth overrides DefaultMaxDependencyDepth for this
	// Installer's install-time cycle/recursion-limit check
	// (dependency_graph.go, TASKS/skills/07) when set to a positive
	// value. Zero (the default) uses DefaultMaxDependencyDepth.
	MaxDependencyDepth int

	Emit EventFunc

	mu    sync.Mutex
	state State
}

// State returns the current state. Safe for concurrent read.
func (i *Installer) State() State {
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.state == "" {
		return StateNotInstalled
	}
	return i.state
}

func (i *Installer) setState(s State) {
	i.mu.Lock()
	i.state = s
	i.mu.Unlock()
}

func (i *Installer) transition(slug string, s State, msg string) {
	i.setState(s)
	if i.Emit != nil {
		i.Emit(Event{Slug: slug, State: s, Message: msg})
	}
}

// maxDependencyDepth returns i.MaxDependencyDepth when it's been set to
// a positive value, or DefaultMaxDependencyDepth otherwise.
func (i *Installer) maxDependencyDepth() int {
	if i.MaxDependencyDepth > 0 {
		return i.MaxDependencyDepth
	}
	return DefaultMaxDependencyDepth
}

func (i *Installer) fail(slug string, from State, err error) error {
	i.setState(StateFailed)
	if i.Emit != nil {
		i.Emit(Event{
			Slug:    slug,
			State:   StateFailed,
			Message: fmt.Sprintf("failed in %s", from),
			Err:     err,
		})
	}
	return err
}

// Install runs the full Parse → Validate → Vendor → Index pipeline for
// src. Whether this creates a brand-new skills index row or re-syncs an
// existing one (same slug, changed or unchanged content at the same
// source path) is decided internally by the Indexing step, keyed on the
// parsed package's own frontmatter slug — see the package doc's "Re-sync
// is the same pipeline" section. There is no separate Sync method and no
// directory-sweep entry point: exactly one named package path is
// installed per call.
func (i *Installer) Install(ctx context.Context, src Source) (Result, error) {
	if src.Path == "" {
		return Result{}, errors.New("skillinstall: Source.Path is empty")
	}
	if i.Vendor == nil {
		return Result{}, errors.New("skillinstall: Vendor is nil")
	}
	if i.Index == nil {
		return Result{}, errors.New("skillinstall: Index is nil")
	}

	i.setState(StateNotInstalled)

	i.transition("", StateParsing, "parsing package: "+src.Path)
	def, files, err := skill.ParsePackageDir(src.Path)
	if err != nil {
		return Result{}, i.fail("", StateParsing, fmt.Errorf("parse: %w", err))
	}
	slug := def.Slug

	i.transition(slug, StateValidating, "validating package")
	validator := i.Validate
	if validator == nil {
		validator = DefaultValidator{}
	}
	if err := validator.Validate(def, files); err != nil {
		return Result{}, i.fail(slug, StateValidating, fmt.Errorf("validate: %w", err))
	}
	deps := extractDeclaredDependencies(def)

	// TASKS/skills/07: install-time cycle/recursion-limit detection
	// against the graph of already-installed skills' own declared
	// dependencies — docs/engineering/architecture/20-skills.md's own
	// explicit instruction that this check runs once here, not
	// discovered live during a materialization pass. Runs as part of
	// the same Validating phase since it's still a pre-vendor gate; a
	// package whose own single-package shape (DefaultValidator, above)
	// is fine can still be rejected here for what it would do to the
	// broader, already-installed graph.
	if err := checkDependencyGraph(i.Index, slug, deps, i.maxDependencyDepth()); err != nil {
		return Result{}, i.fail(slug, StateValidating, fmt.Errorf("dependency graph: %w", err))
	}

	i.transition(slug, StateVendoring, "vendoring package")
	wr, err := i.Vendor.Write(ctx, skillvendor.FileMap(files))
	if err != nil {
		return Result{}, i.fail(slug, StateVendoring, fmt.Errorf("vendor: %w", err))
	}

	i.transition(slug, StateIndexing, "updating index")
	sk, err := i.upsertIndex(def, wr.Address, deps)
	if err != nil {
		// Best-effort rollback: never leave a freshly-vendored (not
		// reused) address behind with no index row pointing at it. See
		// vendorDeleter's doc comment for why Reused writes are exempt.
		if !wr.Reused {
			if d, ok := i.Vendor.(vendorDeleter); ok {
				_ = d.Delete(wr.Address)
			}
		}
		return Result{}, i.fail(slug, StateIndexing, fmt.Errorf("index: %w", err))
	}

	i.transition(slug, StateReady, "ready")
	return Result{Skill: *sk, Address: wr.Address, Reused: wr.Reused}, nil
}

// upsertIndex creates a new store.Skill row when no skill with this
// package's slug exists yet, or updates the existing row's
// pointer/hash/version/declared-dependencies otherwise. It never mutates
// the vendored bytes at the row's *previous* ContentHash — the vendored
// store's own immutability guarantee (internal/skillvendor) already
// ensures that; this function only ever changes which address the index
// row points at, and only bumps Version when the address actually
// changes (a re-sync of byte-identical content is a no-op here beyond
// refreshing name/description/category/dependencies from the package).
func (i *Installer) upsertIndex(def *skill.Definition, address string, deps []string) (*store.Skill, error) {
	depsJSON, err := json.Marshal(deps)
	if err != nil {
		return nil, fmt.Errorf("marshal declared dependencies: %w", err)
	}

	existing, err := i.Index.GetSkillBySlug(def.Slug)
	if err != nil {
		return nil, fmt.Errorf("lookup existing skill %q: %w", def.Slug, err)
	}

	fresh := def.ToStoreSkill()
	fresh.ContentHash = address
	fresh.DeclaredDependencies = string(depsJSON)

	if existing == nil {
		// ToStoreSkill sets a deterministic "file-<slug>" ID — a sentinel
		// skill.IsFileBasedID() (and the hard Update/Delete rejections in
		// service.skillServiceImpl) reserve for the old, pre-redesign
		// virtual/ephemeral file-based rows that convention was written
		// for. This is a real, persisted CreateSkill call, not one of
		// those ephemeral views, so it must not inherit that sentinel:
		// clear it and let store.Store.CreateSkill's own
		// `if sk.ID == "" { sk.ID = uuid.New().String() }` fallback mint a
		// real UUID, exactly like every other real CreateSkill caller in
		// this codebase already gets.
		fresh.ID = ""
		if err := i.Index.CreateSkill(fresh); err != nil {
			return nil, fmt.Errorf("create skill index row: %w", err)
		}
		return fresh, nil
	}

	updated := *existing
	updated.Name = fresh.Name
	updated.Description = fresh.Description
	updated.Category = fresh.Category
	updated.Icon = fresh.Icon
	updated.InputSchema = fresh.InputSchema
	updated.SourceTier = fresh.SourceTier
	updated.DeclaredDependencies = fresh.DeclaredDependencies
	if address != existing.ContentHash {
		updated.ContentHash = address
		updated.Version = existing.Version + 1
	}
	if err := i.Index.UpdateSkill(&updated); err != nil {
		return nil, fmt.Errorf("update skill index row %q: %w", updated.ID, err)
	}
	return &updated, nil
}

// extractDeclaredDependencies returns a deep copy of def's declared
// dependency slugs (TASKS/skills/04's own scope: extraction only —
// TASKS/skills/07 owns cycle/recursion-limit detection and real
// inline/fork semantics). DefaultValidator has already confirmed the raw
// list is well-formed (non-empty entries, no duplicates, no
// self-reference) by the time this runs. Never returns nil so the
// resulting JSON is always "[]" rather than "null" for a dependency-free
// package.
func extractDeclaredDependencies(def *skill.Definition) []string {
	if len(def.Dependencies) == 0 {
		return []string{}
	}
	out := make([]string, len(def.Dependencies))
	copy(out, def.Dependencies)
	return out
}
