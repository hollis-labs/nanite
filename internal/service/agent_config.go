package service

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
	"github.com/hollis-labs/nanite/internal/agent"
	"github.com/hollis-labs/nanite/internal/store"
)

// AgentConfigService is the single shared write path for managed file-backed
// agent configs. GUI, API, CLI, and MCP callers all route mutations through
// it so the write contract is enforced uniformly:
//
//	validated request -> normalized agent config document -> atomic file write
//	-> DB upsert/reindex -> live registry reload -> event
//
// Files remain the durable source of truth; the DB row is the indexed runtime
// projection. The service owns source classification (managed-writable vs
// internal-embedded vs plugin/vendor vs unknown-external), identity stamping
// (UUID in frontmatter), optimistic concurrency (file-content revision), and
// the copy-to-managed / make-editable path for read-only sources.
type AgentConfigService struct {
	store          *store.Store
	classification agent.Classification
	// managedRoot is the destination config root for newly created managed
	// agents and copies (the project .nanite by default). Existing agents are
	// rewritten in place at their own SourceRef.
	managedRoot string
	reloader    AgentRegistryReloader     // nil-safe live-registry refresh
	notify      func(slug, action string) // nil-safe best-effort change event
}

// NewAgentConfigService constructs the shared managed-agent write service.
func NewAgentConfigService(st *store.Store, classification agent.Classification, managedRoot string, reloader AgentRegistryReloader, notify func(slug, action string)) *AgentConfigService {
	return &AgentConfigService{
		store:          st,
		classification: classification,
		managedRoot:    managedRoot,
		reloader:       reloader,
		notify:         notify,
	}
}

// Sentinel errors. Handlers map these to HTTP status codes.
var (
	// ErrAgentNotManaged is returned when a write/delete targets an agent
	// whose source is not a writable managed file (internal/plugin/external).
	ErrAgentNotManaged = errors.New("agent is not a writable managed config")
	// ErrAgentRevisionConflict is returned when the on-disk file changed
	// underneath an edit (optimistic-concurrency guard).
	ErrAgentRevisionConflict = errors.New("agent config changed on disk since it was loaded")
	// ErrAgentAlreadyManaged is returned by CopyToManaged when the source is
	// already a writable managed agent.
	ErrAgentAlreadyManaged = errors.New("agent is already a managed config")
	// ErrManagedSlugExists is returned when a create/copy target slug already
	// has a managed file.
	ErrManagedSlugExists = errors.New("a managed agent with this slug already exists")
)

// AgentConfigResult is returned by mutating operations.
type AgentConfigResult struct {
	Profile  *store.AgentProfile
	Class    agent.ManageClass
	Revision string
}

// Classify returns the management class of a profile.
func (s *AgentConfigService) Classify(p *store.AgentProfile) agent.ManageClass {
	if p == nil {
		return agent.ManageClassExternal
	}
	return s.classification.Classify(p.Source, p.SourceRef)
}

// Revision returns the optimistic-concurrency token (file-content hash) for a
// managed profile, or "" when there is no writable on-disk file.
func (s *AgentConfigService) Revision(p *store.AgentProfile) string {
	if p == nil || strings.TrimSpace(p.SourceRef) == "" {
		return ""
	}
	rev, err := agent.FileRevision(p.SourceRef)
	if err != nil {
		return ""
	}
	return rev
}

// Create writes a brand-new managed agent: mints a UUID identity, serializes
// the document into the managed root, ingests the DB projection, and reloads
// the live registry. profile.ID is ignored (always minted).
func (s *AgentConfigService) Create(profile *store.AgentProfile, procedures []agent.ProcedureDefinition) (*AgentConfigResult, error) {
	if profile == nil {
		return nil, fmt.Errorf("profile is required")
	}
	if strings.TrimSpace(profile.Slug) == "" {
		return nil, fmt.Errorf("slug is required")
	}
	path, err := agent.ManagedAgentPath(s.managedRoot, profile.Slug)
	if err != nil {
		return nil, err
	}
	if _, statErr := os.Stat(path); statErr == nil {
		return nil, ErrManagedSlugExists
	}
	profile.ID = uuid.New().String()
	profile.Source = "user"
	return s.writeManaged(path, "", profile, procedures, "created")
}

// Update rewrites an existing managed agent in place (or moves the file on a
// slug rename). existing is the current registry/DB profile (carries the
// canonical ID + SourceRef); updated carries the merged new field values.
// expectedRevision guards against clobbering an out-of-band file edit; pass ""
// to skip the guard (not recommended for interactive edits).
func (s *AgentConfigService) Update(existing, updated *store.AgentProfile, procedures []agent.ProcedureDefinition, expectedRevision string) (*AgentConfigResult, error) {
	if existing == nil || updated == nil {
		return nil, fmt.Errorf("existing and updated profiles are required")
	}
	if class := s.Classify(existing); !class.Editable() {
		return nil, ErrAgentNotManaged
	}
	// Preserve the canonical identity across the edit (rename-safe).
	updated.ID = existing.ID
	if strings.TrimSpace(updated.Slug) == "" {
		updated.Slug = existing.Slug
	}

	// A DB-only operator agent (no on-disk file yet) materializes a managed
	// file on first edit, in the default managed root. File-backed agents are
	// rewritten in place (renamed when the slug changes).
	var dest, oldPath string
	if strings.TrimSpace(existing.SourceRef) == "" {
		path, err := agent.ManagedAgentPath(s.managedRoot, updated.Slug)
		if err != nil {
			return nil, err
		}
		dest = path
	} else {
		// Optimistic concurrency: compare the supplied revision against the
		// current on-disk file before we overwrite it.
		if expectedRevision != "" {
			current, err := agent.FileRevision(existing.SourceRef)
			if err != nil {
				return nil, fmt.Errorf("read current revision: %w", err)
			}
			if current != "" && current != expectedRevision {
				return nil, ErrAgentRevisionConflict
			}
		}
		dest = filepath.Join(filepath.Dir(existing.SourceRef), updated.Slug+".md")
		if dest != existing.SourceRef {
			oldPath = existing.SourceRef
		}
	}
	// Preserve provenance unless empty.
	if updated.Source == "" {
		updated.Source = existing.Source
	}
	return s.writeManaged(dest, oldPath, updated, procedures, "updated")
}

// Delete removes a managed agent's file, DB projection (and FK children), and
// drops it from the live registry. Read-only sources are rejected.
func (s *AgentConfigService) Delete(profile *store.AgentProfile) error {
	if profile == nil {
		return fmt.Errorf("profile is required")
	}
	if class := s.Classify(profile); !class.Editable() {
		return ErrAgentNotManaged
	}
	if ref := strings.TrimSpace(profile.SourceRef); ref != "" {
		if err := os.Remove(ref); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove managed file: %w", err)
		}
	}
	if err := s.store.DeleteAgent(profile.Slug); err != nil {
		return fmt.Errorf("delete agent projection: %w", err)
	}
	if s.reloader != nil {
		s.reloader.RemoveFileAgent(profile.Slug)
	}
	s.emit(profile.Slug, "deleted")
	return nil
}

// CopyToManaged forks a read-only source agent (internal/plugin/external) into
// a fresh editable managed config with a new identity. Returns
// ErrAgentAlreadyManaged when the source is already writable.
func (s *AgentConfigService) CopyToManaged(source *store.AgentProfile, procedures []agent.ProcedureDefinition) (*AgentConfigResult, error) {
	if source == nil {
		return nil, fmt.Errorf("source profile is required")
	}
	if s.Classify(source).Editable() {
		return nil, ErrAgentAlreadyManaged
	}
	// Clone the field set; mint a new identity and managed provenance. The
	// slug must be distinct from the read-only source (slug is unique), so the
	// fork doesn't collide with / overwrite the original's projection row.
	clone := *source
	clone.ID = ""
	clone.Source = "user"
	clone.SourceRef = ""
	clone.Kind = ""
	clone.Slug = s.uniqueManagedSlug(source.Slug + "-copy")
	if clone.Name != "" {
		clone.Name = source.Name + " (copy)"
	}
	// If procedures weren't supplied and the source is a real file, parse them.
	if len(procedures) == 0 && source.SourceRef != "" && !strings.HasPrefix(source.SourceRef, "embedded:") {
		if def, err := agent.ParseMDFile(source.SourceRef); err == nil {
			procedures = def.Procedures
		}
	}
	return s.Create(&clone, procedures)
}

// writeManaged performs the shared tail of the write contract: stamp identity
// into the frontmatter, atomic file write, optional old-file cleanup (rename),
// DB upsert/reindex, live registry reload, and event emission.
func (s *AgentConfigService) writeManaged(path, oldPath string, profile *store.AgentProfile, procedures []agent.ProcedureDefinition, action string) (*AgentConfigResult, error) {
	if err := agent.EnsureManagedConfigDirs(s.managedRoot); err != nil {
		// Non-fatal for in-place edits whose dir already exists; only the
		// default managed root is ensured here. The atomic write below
		// creates the destination dir regardless.
		slog.Warn("agent-config: ensure managed dirs", "err", err)
	}
	if strings.TrimSpace(profile.ID) == "" {
		profile.ID = uuid.New().String()
	}
	if err := agent.WriteManagedAgentProfile(path, profile, procedures); err != nil {
		return nil, err
	}
	// Remove the prior file on a slug rename, after the new file has landed.
	if oldPath != "" && oldPath != path {
		if err := os.Remove(oldPath); err != nil && !os.IsNotExist(err) {
			slog.Warn("agent-config: remove renamed file", "old", oldPath, "err", err)
		}
	}
	def, err := agent.ParseMDFile(path)
	if err != nil {
		return nil, err
	}
	def.Source = profile.Source
	if def.Source == "" {
		def.Source = "user"
	}
	def.SourceRef = path
	if err := IngestAgentDefinition(s.store, def); err != nil {
		return nil, err
	}
	// Refresh the live registry so reads reflect the edit without a restart.
	if s.reloader != nil {
		s.reloader.ReloadFileAgent(def)
	}
	saved, err := s.store.GetAgentBySlug(profile.Slug)
	if err != nil {
		return nil, err
	}
	rev, _ := agent.FileRevision(path)
	s.emit(profile.Slug, action)
	return &AgentConfigResult{
		Profile:  saved,
		Class:    s.classification.Classify(saved.Source, saved.SourceRef),
		Revision: rev,
	}, nil
}

// uniqueManagedSlug returns base, or base-2/base-3/... when a DB row or a
// managed file already claims the slug.
func (s *AgentConfigService) uniqueManagedSlug(base string) string {
	taken := func(slug string) bool {
		if row, err := s.store.GetAgentBySlug(slug); err == nil && row != nil {
			return true
		}
		if path, err := agent.ManagedAgentPath(s.managedRoot, slug); err == nil {
			if _, statErr := os.Stat(path); statErr == nil {
				return true
			}
		}
		return false
	}
	if !taken(base) {
		return base
	}
	for i := 2; ; i++ {
		candidate := fmt.Sprintf("%s-%d", base, i)
		if !taken(candidate) {
			return candidate
		}
	}
}

func (s *AgentConfigService) emit(slug, action string) {
	if s.notify != nil {
		s.notify(slug, action)
	}
}

// ReconcileManagedAgentIDs is the boot pass that durably stamps identity into
// writable managed agent files. For each managed-writable definition that has
// no `id:` yet, it adopts the existing DB row's UUID (if the slug is already
// known) or mints a fresh one, writes it back into the file's frontmatter,
// and sets def.ID so the subsequent ingest uses the UUID as the DB row PK.
// Idempotent: already-stamped files are skipped; re-running maps every file to
// the same row, so existing UUID rows reconcile themselves with no migration.
func ReconcileManagedAgentIDs(st *store.Store, defs []*agent.Definition, classification agent.Classification) {
	for _, def := range defs {
		if def == nil || def.Slug == "" {
			continue
		}
		if strings.TrimSpace(def.ID) != "" {
			continue // already stamped
		}
		if classification.Classify(def.Source, def.SourceRef) != agent.ManageClassManaged {
			continue // only writable managed files get a stamped UUID
		}
		if !classification.IsWritablePath(def.SourceRef) {
			continue // defensive: never write outside a known writable root
		}
		id := uuid.New().String()
		if existing, err := st.GetAgentBySlug(def.Slug); err == nil && existing != nil && existing.ID != "" && !agent.IsFileBasedID(existing.ID) {
			id = existing.ID // adopt the existing projection's identity
		}
		if err := agent.InjectFrontmatterID(def.SourceRef, id); err != nil {
			slog.Warn("agent-config: stamp managed id", "slug", def.Slug, "path", def.SourceRef, "err", err)
			continue
		}
		def.ID = id
	}
}
