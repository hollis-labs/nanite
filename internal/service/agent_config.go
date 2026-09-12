package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/hollis-labs/nanite/internal/agent"
	"github.com/hollis-labs/nanite/internal/store"
)

// AgentConfigService is the shared database write path for operator-managed
// agent profiles. SourceRef is retained on existing rows as historical
// provenance only; this service never reads, writes, renames, or deletes the
// referenced path.
type AgentConfigService struct {
	store          *store.Store
	classification agent.Classification
	notify         func(slug, action string)
}

func NewAgentConfigService(st *store.Store, classification agent.Classification, notify func(slug, action string)) *AgentConfigService {
	return &AgentConfigService{store: st, classification: classification, notify: notify}
}

var (
	ErrAgentNotManaged     = errors.New("agent is not an editable database config")
	ErrAgentAlreadyManaged = errors.New("agent is already a managed config")
	ErrManagedSlugExists   = errors.New("a managed agent with this slug already exists")
)

// notManagedError wraps ErrAgentNotManaged with what is actually in the way
// and, when one exists, the way out.
//
// The sentinel alone ("agent is not an editable database config") tells a
// caller that a write was refused but not what to do instead, which is how a
// read-only boundary reads as a dead end. internal/api's writeNotManaged has
// said the useful thing for a while — this puts the same information behind
// the service call, so a CLI, a self-tool or a test sees it too.
//
// Every caller matches with errors.Is, so enriching the message is safe.
func notManagedError(p *store.AgentProfile, class agent.ManageClass, verb string) error {
	slug := ""
	if p != nil {
		slug = p.Slug
	}
	if class.CopyToManagedAllowed() {
		return fmt.Errorf("%w: %q is %s and read-only in place; copy it to the managed layer (CopyToManaged) and %s the copy",
			ErrAgentNotManaged, slug, class.Describe(), verb)
	}
	return fmt.Errorf("%w: %q is %s, which Nanite manages; there is no copy-to-managed path for it",
		ErrAgentNotManaged, slug, class.Describe())
}

// AgentConfigResult retains Revision for wire compatibility. It is always
// empty now that the database, rather than a file-content hash, is canonical.
type AgentConfigResult struct {
	Profile  *store.AgentProfile
	Class    agent.ManageClass
	Revision string
}

func (s *AgentConfigService) Classify(p *store.AgentProfile) agent.ManageClass {
	if p == nil {
		return agent.ManageClassExternal
	}
	return s.classification.Classify(p.Source)
}

func (s *AgentConfigService) Persisted(p *store.AgentProfile) bool {
	if p == nil || strings.TrimSpace(p.Slug) == "" {
		return false
	}
	row, err := s.store.GetAgentBySlug(context.TODO() /* TODO(ctx-sweep): no ctx available at this call site */, p.Slug)
	return err == nil && row != nil && row.ID == p.ID
}

func (*AgentConfigService) Revision(*store.AgentProfile) string { return "" }

// Create inserts a new operator-owned profile directly into agent_profiles.
// The procedures argument remains as an API compatibility seam and seeds the
// relational procedure rows; it is never serialized to a projection file.
func (s *AgentConfigService) Create(profile *store.AgentProfile, procedures []agent.ProcedureDefinition) (*AgentConfigResult, error) {
	if profile == nil {
		return nil, fmt.Errorf("profile is required")
	}
	if err := agent.ValidateSlug(profile.Slug); err != nil {
		return nil, err
	}
	if existing, err := s.store.GetAgentBySlug(context.TODO() /* TODO(ctx-sweep): no ctx available at this call site */, profile.Slug); err == nil && existing != nil {
		return nil, ErrManagedSlugExists
	} else if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}

	profile.ID = ""
	profile.Source = "user"
	profile.SourceRef = ""
	profile.ImportedAt = ""
	profile.OriginSystem = ""
	if err := s.store.CreateAgent(context.TODO() /* TODO(ctx-sweep): no ctx available at this call site */, profile); err != nil {
		return nil, err
	}
	if _, err := s.store.DB.ExecContext(context.TODO(), /* TODO(ctx-sweep): no ctx available at this call site */
		`UPDATE agent_profiles SET default_trust_tier = 'untrusted' WHERE id = ?`, profile.ID); err != nil {
		return nil, fmt.Errorf("set operator agent trust tier: %w", err)
	}
	seedRoleToolsFromIngest(context.Background(), s.store, profile.ID, roleToolNames(profile.RoleTools))
	seedProcedures(context.Background(), s.store, profile.ID, procedures)
	return s.result(profile.Slug, "created")
}

// Update persists a managed profile directly in the database. Procedures are
// relational data and remain untouched unless explicitly supplied. The former
// revision token is accepted for wire compatibility but has no filesystem
// concurrency meaning.
func (s *AgentConfigService) Update(existing, updated *store.AgentProfile, procedures []agent.ProcedureDefinition, _ string) (*AgentConfigResult, error) {
	if existing == nil || updated == nil {
		return nil, fmt.Errorf("existing and updated profiles are required")
	}
	if class := s.Classify(existing); !class.Editable() {
		return nil, notManagedError(existing, class, "update")
	}
	if strings.TrimSpace(updated.Slug) == "" {
		updated.Slug = existing.Slug
	}
	if err := agent.ValidateSlug(updated.Slug); err != nil {
		return nil, err
	}
	updated.ID = existing.ID
	updated.Source = existing.Source
	if updated.Source == "" {
		updated.Source = "user"
	}
	// A legacy SourceRef may explain where a row was first imported from, but
	// once edited through the canonical API it must not round-trip to disk.
	updated.SourceRef = ""
	if err := s.store.UpdateAgent(context.TODO() /* TODO(ctx-sweep): no ctx available at this call site */, updated); err != nil {
		return nil, err
	}
	seedRoleToolsFromIngest(context.Background(), s.store, updated.ID, roleToolNames(updated.RoleTools))
	if len(procedures) > 0 {
		seedProcedures(context.Background(), s.store, updated.ID, procedures)
	}
	return s.result(updated.Slug, "updated")
}

func (s *AgentConfigService) Delete(profile *store.AgentProfile) error {
	if profile == nil {
		return fmt.Errorf("profile is required")
	}
	if class := s.Classify(profile); !class.Editable() {
		return notManagedError(profile, class, "delete")
	}
	if err := s.store.DeleteAgent(context.TODO() /* TODO(ctx-sweep): no ctx available at this call site */, profile.Slug); err != nil {
		return fmt.Errorf("delete agent: %w", err)
	}
	s.emit(profile.Slug, "deleted")
	return nil
}

// CopyToManaged forks plugin or explicitly external provenance into a fresh
// operator-owned database row. SourceRef is deliberately not dereferenced.
func (s *AgentConfigService) CopyToManaged(source *store.AgentProfile, procedures []agent.ProcedureDefinition) (*AgentConfigResult, error) {
	if source == nil {
		return nil, fmt.Errorf("source profile is required")
	}
	class := s.Classify(source)
	if class.Editable() {
		return nil, ErrAgentAlreadyManaged
	}
	if !class.CopyToManagedAllowed() {
		return nil, notManagedError(source, class, "copy")
	}
	if procedures == nil {
		rows, err := s.store.ListAgentProcedures(context.TODO() /* TODO(ctx-sweep): no ctx available at this call site */, source.ID)
		if err != nil {
			return nil, fmt.Errorf("list source agent procedures: %w", err)
		}
		procedures = make([]agent.ProcedureDefinition, 0, len(rows))
		for _, row := range rows {
			procedures = append(procedures, agent.ProcedureDefinition{Name: row.Name, Body: row.Body, Scope: row.Scope})
		}
	}
	clone := *source
	clone.ID = ""
	clone.Source = "user"
	clone.SourceRef = ""
	clone.Kind = ""
	clone.PluginID = ""
	clone.ConsumerID = ""
	clone.RoleID = ""
	clone.ModelID = ""
	clone.CreatedAt = ""
	clone.UpdatedAt = ""
	clone.ImportedAt = ""
	clone.OriginSystem = ""
	clone.AgentHash = ""
	clone.Version = 0
	clone.Slug = s.uniqueManagedSlug(source.Slug + "-copy")
	if clone.Name != "" {
		clone.Name = source.Name + " (copy)"
	}
	return s.Create(&clone, procedures)
}

func (s *AgentConfigService) result(slug, action string) (*AgentConfigResult, error) {
	saved, err := s.store.GetAgentBySlug(context.TODO() /* TODO(ctx-sweep): no ctx available at this call site */, slug)
	if err != nil {
		return nil, err
	}
	s.emit(slug, action)
	return &AgentConfigResult{Profile: saved, Class: s.classification.Classify(saved.Source)}, nil
}

func (s *AgentConfigService) uniqueManagedSlug(base string) string {
	taken := func(slug string) bool {
		row, err := s.store.GetAgentBySlug(context.TODO() /* TODO(ctx-sweep): no ctx available at this call site */, slug)
		return err == nil && row != nil
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

func roleToolNames(raw string) []string {
	var names []string
	if json.Unmarshal([]byte(raw), &names) != nil {
		return nil
	}
	return names
}

func (s *AgentConfigService) emit(slug, action string) {
	if s.notify != nil {
		s.notify(slug, action)
	}
}
