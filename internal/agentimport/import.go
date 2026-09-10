package agentimport

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/hollis-labs/nanite/internal/agent"
	"github.com/hollis-labs/nanite/internal/store"
)

// SourceProvenance is the value stamped into agent_profiles.source for every
// imported row. It must classify as agent.ManageClassExternal — see
// source_class.go's Classify, whose `managed` case lists "cli", "nanite",
// "project", "user" and the empty string. Reusing any of those here would
// silently make imported agents editable in place, which is the opposite of
// the ownership rule this package exists to hold. TestImportProvenanceIsExternal
// pins the mapping so a later edit to that list cannot quietly break it.
const SourceProvenance = "import"

// DefaultOriginSystem is written to agent_profiles.origin_system when the
// parser does not name a more specific originating ecosystem. A format
// adapter supplies its own name (e.g. "claude") instead.
const DefaultOriginSystem = "import"

// State is the current position in the import pipeline.
type State string

const (
	StateNotStarted State = "not_started"
	StateParsing    State = "parsing"
	StateValidating State = "validating"
	StateWriting    State = "writing"
	StateReady      State = "ready"
	StateFailed     State = "failed"
)

// Action reports what one definition's write step actually did.
type Action string

const (
	// ActionCreated is a first import: no row held this slug.
	ActionCreated Action = "created"
	// ActionSynced is a re-import over a row this package already owns
	// (a previous import, i.e. external provenance).
	ActionSynced Action = "synced"
	// ActionSkipped is a definition that was parsed but deliberately not
	// written. Outcome.Reason always explains why.
	ActionSkipped Action = "skipped"
)

// Event is emitted on each state transition.
type Event struct {
	Slug    string
	State   State
	Message string
	Err     error
}

// EventFunc consumes import events. Pass nil to ignore.
type EventFunc func(Event)

// Source describes the single target this Import call names — one path on
// disk. What that path may be (a file, a directory of definitions, a
// materialized boot directory) is the Parser's business, not this package's:
// Nanite mandates the target TYPE (agent.Definition), never a source format.
type Source struct {
	// Path is the operator-named filesystem path to import from.
	Path string

	// Parser overrides the Importer's default parser for this call — the
	// seam a `--adapter` flag resolves to. Nil uses Importer.Parse.
	Parser Parser
}

// Parser turns one operator-named path into zero or more definitions. It is
// the only place in the import pipeline that knows what a source format
// looks like.
//
// Returning (nil, nil) means "this path is not mine" — a registry of
// parsers uses that to try the next one. Returning an error means "this
// path IS mine and it is broken," which stops the pipeline.
//
// A Parser MAY expand a directory into N definitions. That expansion lives
// here rather than in the CLI deliberately: it keeps "what a directory of
// this format means" inside the thing that understands the format, and it
// leaves room for a single directory to be ONE definition (a materialized
// boot directory) without the caller having to know the difference.
type Parser interface {
	Parse(path string) ([]*agent.Definition, error)
}

// ProfileStore is the narrow slice of *store.Store this package writes
// through. A real *store.Store satisfies it directly.
type ProfileStore interface {
	GetAgentBySlug(ctx context.Context, slug string) (*store.AgentProfile, error)
	CreateAgent(ctx context.Context, a *store.AgentProfile) error
	UpdateAgent(ctx context.Context, a *store.AgentProfile) error
	SetAgentDefaultTrustTier(ctx context.Context, agentID, tier string) error
}

// ChildSeeder writes a definition's relational children (procedures,
// roleTools) for a row that was just created or synced. It is injected
// rather than implemented here so this package reuses internal/service's
// existing, tested seeders instead of growing a second copy of them.
type ChildSeeder func(ctx context.Context, agentID string, def *agent.Definition)

// Outcome is what happened to one parsed definition.
type Outcome struct {
	Slug   string
	Name   string
	Action Action
	// Reason explains an ActionSkipped outcome in prose, for a human.
	// Empty otherwise.
	Reason string
	// Err carries the same refusal as a wrapped sentinel so a caller can
	// classify it (errors.Is against ErrSlugNotImportable) rather than
	// matching on Reason's wording. Nil for created/synced outcomes.
	Err error
	// BlockedBy names the ManageClass of the profile that already holds
	// this slug, for an ActionSkipped outcome. Empty otherwise.
	BlockedBy agent.ManageClass
	// Profile is the resulting row for created/synced outcomes, nil for
	// skipped ones.
	Profile *store.AgentProfile
}

// Result is what a completed Import call returns. A Result with skipped
// outcomes is still a successful call — see Importer.Import.
type Result struct {
	// Path is the operator-named path this call targeted.
	Path string
	// Outcomes is one entry per parsed definition, in parse order.
	Outcomes []Outcome
}

// Counts returns how many outcomes fell into each action.
func (r Result) Counts() (created, synced, skipped int) {
	for _, o := range r.Outcomes {
		switch o.Action {
		case ActionCreated:
			created++
		case ActionSynced:
			synced++
		case ActionSkipped:
			skipped++
		}
	}
	return created, synced, skipped
}

// Skipped reports whether any definition was parsed but not written.
func (r Result) Skipped() bool {
	_, _, skipped := r.Counts()
	return skipped > 0
}

var (
	// ErrNoDefinitions means no parser recognized the path, or the path
	// held nothing importable. It is distinct from a parse failure: this
	// is "nothing here to import," not "this is broken."
	ErrNoDefinitions = errors.New("agentimport: no agent definitions found at path")

	// ErrSlugNotImportable means the slug is already held by a row this
	// package does not own — an internal harness primitive, an
	// operator-managed profile, or a plugin-provided one. Import never
	// overwrites those and never silently renames around them.
	ErrSlugNotImportable = errors.New("agentimport: slug is held by a profile import does not own")
)

// Importer drives the Parse -> Validate -> Write pipeline. See the package
// doc for the one-way/one-time/with-provenance contract it implements.
type Importer struct {
	// Store is the required write target.
	Store ProfileStore

	// Parse is the default parser. Nil uses NativeParser{}.
	Parse Parser

	// SeedChildren, when set, writes each created or synced row's
	// procedures and roleTools.
	SeedChildren ChildSeeder

	// OriginSystem overrides DefaultOriginSystem for rows this Importer
	// writes — a format adapter names its own ecosystem here.
	OriginSystem string

	// Now overrides time.Now for the ImportedAt stamp. Tests set it;
	// production leaves it nil.
	Now func() time.Time

	Emit EventFunc

	mu    sync.Mutex
	state State
}

// State returns the current pipeline state. Safe for concurrent read.
func (i *Importer) State() State {
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.state == "" {
		return StateNotStarted
	}
	return i.state
}

func (i *Importer) setState(s State) {
	i.mu.Lock()
	i.state = s
	i.mu.Unlock()
}

func (i *Importer) transition(slug string, s State, msg string) {
	i.setState(s)
	if i.Emit != nil {
		i.Emit(Event{Slug: slug, State: s, Message: msg})
	}
}

func (i *Importer) fail(slug string, from State, err error) error {
	i.setState(StateFailed)
	if i.Emit != nil {
		i.Emit(Event{Slug: slug, State: StateFailed, Message: "failed in " + string(from), Err: err})
	}
	return err
}

func (i *Importer) now() string {
	fn := i.Now
	if fn == nil {
		fn = time.Now
	}
	return fn().UTC().Format(time.RFC3339)
}

func (i *Importer) originSystem() string {
	if i.OriginSystem != "" {
		return i.OriginSystem
	}
	return DefaultOriginSystem
}

// Import runs the full pipeline for one operator-named path.
//
// It returns an error only for a failure of the call as a whole: an empty
// path, a parser that reported the target broken, a store write that failed,
// or a path nothing recognized (ErrNoDefinitions). A definition that parsed
// fine but must not be written — because its slug belongs to a profile this
// package does not own — is reported as an ActionSkipped Outcome rather than
// aborting the rest of a multi-definition path. Callers that need "nothing
// was skipped" as a hard condition check Result.Skipped(); the CLI does.
//
// There is no separate Sync method and no directory-sweep entry point. A
// re-import over an already-imported slug IS the sync, decided by the write
// step from what is in the database — see the package doc.
func (i *Importer) Import(ctx context.Context, src Source) (Result, error) {
	if src.Path == "" {
		return Result{}, errors.New("agentimport: Source.Path is empty")
	}
	if i.Store == nil {
		return Result{}, errors.New("agentimport: Store is nil")
	}

	// Resolve the operator-named path to an absolute one before any parser
	// sees it, so the SourceRef a parser derives from it is a provenance
	// record that survives a change of working directory. A relative path
	// stored as provenance answers "where did this come from" with "it
	// depends," which is not an answer.
	absPath, err := filepath.Abs(src.Path)
	if err != nil {
		return Result{}, fmt.Errorf("agentimport: resolve %s: %w", src.Path, err)
	}
	src.Path = absPath

	i.setState(StateNotStarted)

	parser := src.Parser
	if parser == nil {
		parser = i.Parse
	}
	if parser == nil {
		parser = NativeParser{}
	}

	i.transition("", StateParsing, "parsing: "+src.Path)
	defs, parseErr := parser.Parse(src.Path)
	if parseErr != nil {
		// Every reader declining is "nothing here to import" (with the
		// reasons attached), not "this is broken" — keep the two distinct
		// so a caller can tell a wrong path from a wrong file.
		if errors.Is(parseErr, ErrNotThisFormat) {
			return Result{}, i.fail("", StateParsing,
				fmt.Errorf("%w: %s — %s", ErrNoDefinitions, src.Path, unwrapDecline(parseErr)))
		}
		return Result{}, i.fail("", StateParsing, fmt.Errorf("parse %s: %w", src.Path, parseErr))
	}
	if len(defs) == 0 {
		return Result{}, i.fail("", StateParsing,
			fmt.Errorf("%w: %s (tried: %s)", ErrNoDefinitions, src.Path, parserName(parser)))
	}

	result := Result{Path: src.Path}
	for _, def := range defs {
		if def == nil {
			continue
		}
		i.transition(def.Slug, StateValidating, "validating definition")
		if err := agent.ValidateSlug(def.Slug); err != nil {
			return result, i.fail(def.Slug, StateValidating, fmt.Errorf("validate %s: %w", src.Path, err))
		}

		i.transition(def.Slug, StateWriting, "writing agent_profiles row")
		outcome, err := i.write(ctx, def)
		if err != nil {
			return result, i.fail(def.Slug, StateWriting, err)
		}
		result.Outcomes = append(result.Outcomes, outcome)
	}

	i.transition("", StateReady, "import complete")
	return result, nil
}

// write performs the create-or-sync decision for one definition, holding the
// ownership boundary on the way in.
func (i *Importer) write(ctx context.Context, def *agent.Definition) (Outcome, error) {
	// Two different questions, two different columns.
	//
	// A parser sets Definition.Source to the ecosystem it read FROM
	// ("nanite", "claude") — that is provenance, and it lands in
	// origin_system. The stored `source` column answers a different
	// question: how this row came to exist, and therefore who owns it. Only
	// the pipeline answers that, and its answer is always the same. A format
	// adapter describes an agent; it does not get to declare that agent
	// operator-owned.
	origin := i.originSystem()
	if named := strings.TrimSpace(def.Source); named != "" && named != SourceProvenance {
		origin = named
	}
	def.Source = SourceProvenance

	profile := def.ToProfile()
	profile.Source = SourceProvenance
	profile.SourceRef = def.SourceRef
	profile.ImportedAt = i.now()
	profile.OriginSystem = origin
	profile.Format = "markdown"

	// A hardcoded model in an imported definition is the same mistake
	// AutoIngestAgents warns about (parser.go's Model doc comment: ten
	// profiles independently made it). Warn identically rather than
	// silently dropping the value — an import that quietly edited the
	// content it imported would be its own kind of drift.
	if profile.DefaultModel != "" {
		slog.Warn("agentimport: imported agent profile hardcodes a model, opting out of the system default (ResolveProviderAndModel) — leave `model:` blank unless there is a deliberate, documented reason to pin it",
			"slug", def.Slug, "hardcoded_model", profile.DefaultModel, "source_ref", def.SourceRef)
	}

	// store.GetAgentBySlug reports "no such slug" as a wrapped sql.ErrNoRows
	// rather than (nil, nil) — the same discrimination AgentConfigService.Create
	// makes. Anything else is a real database failure and must not be
	// mistaken for an empty slug.
	existing, err := i.Store.GetAgentBySlug(ctx, def.Slug)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return Outcome{}, fmt.Errorf("look up slug %q: %w", def.Slug, err)
	}

	if existing == nil {
		// kind stays at CreateAgent's 'internal' default. That column is
		// NOT ManageClass: migration 015 defines kind as internal =
		// DB/file-based agent, external = auto-registered on first
		// messaging call, cli = deterministic CLI caller. An imported
		// agent is a DB-backed profile, so 'internal' is the correct
		// kind even though its ManageClass is external. Setting
		// kind='external' here would mislabel it as a messaging
		// auto-registration.
		if err := i.Store.CreateAgent(ctx, profile); err != nil {
			return Outcome{}, fmt.Errorf("create agent %q: %w", def.Slug, err)
		}
		if err := i.afterWrite(ctx, profile, def); err != nil {
			return Outcome{}, err
		}
		return Outcome{Slug: def.Slug, Name: def.Name, Action: ActionCreated, Profile: profile}, nil
	}

	// The ownership boundary, enforced inbound. Only a row this package
	// already owns may be rewritten; everything else is reported and left
	// exactly as it was. See the package doc for the boot-pass stomp this
	// refusal closes.
	class := agent.NewClassification().Classify(existing.Source)
	if class != agent.ManageClassExternal {
		return Outcome{
			Slug:      def.Slug,
			Name:      def.Name,
			Action:    ActionSkipped,
			BlockedBy: class,
			Err:       fmt.Errorf("%w: slug %q is held by %s", ErrSlugNotImportable, def.Slug, class.Describe()),
			Reason: fmt.Sprintf("slug %q is already held by %s (source=%q) — import never overwrites a profile it does not own",
				def.Slug, class.Describe(), existing.Source),
		}, nil
	}

	// Preserve everything the source format has no representation for, so a
	// sync updates content without wiping configuration that only exists in
	// the database. This mirrors upsertAgentDef's preservation set for the
	// same reason it exists there: role_id / consumer_id / model_id are
	// written by the composition path and protocol / transport by
	// UpdateAgentACPConfig, and def.ToProfile() always returns their empty
	// zero-value.
	profile.ID = existing.ID
	profile.AgentHash = existing.AgentHash
	profile.Version = existing.Version
	profile.Kind = existing.Kind
	if profile.CapabilitiesJSON == "" {
		profile.CapabilitiesJSON = existing.CapabilitiesJSON
	}
	if profile.LimitsJSON == "" {
		profile.LimitsJSON = existing.LimitsJSON
	}
	profile.RoleID = existing.RoleID
	profile.ConsumerID = existing.ConsumerID
	profile.ModelID = existing.ModelID
	profile.Protocol = existing.Protocol
	profile.Transport = existing.Transport
	profile.CreatedAt = existing.CreatedAt

	if err := i.Store.UpdateAgent(ctx, profile); err != nil {
		return Outcome{}, fmt.Errorf("update agent %q: %w", def.Slug, err)
	}
	if err := i.afterWrite(ctx, profile, def); err != nil {
		return Outcome{}, err
	}
	return Outcome{Slug: def.Slug, Name: def.Name, Action: ActionSynced, Profile: profile}, nil
}

// afterWrite applies the trust tier and seeds relational children for a row
// that was just created or synced.
func (i *Importer) afterWrite(ctx context.Context, profile *store.AgentProfile, def *agent.Definition) error {
	// Imported content arrives untrusted, matching AutoIngestAgents' H1
	// treatment of user/plugin-dropped definitions and
	// AgentConfigService.Create's treatment of a fresh operator profile.
	// Reconciled on every sync rather than only on create: a promotion to
	// trusted is an operator decision about the content that was reviewed,
	// and a sync replaces that content.
	if err := i.Store.SetAgentDefaultTrustTier(ctx, profile.ID, "untrusted"); err != nil {
		return fmt.Errorf("set trust tier for %q: %w", def.Slug, err)
	}
	if i.SeedChildren != nil {
		i.SeedChildren(ctx, profile.ID, def)
	}
	return nil
}
