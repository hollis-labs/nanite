package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	svcerr "github.com/hollis-labs/go-svcerr"

	"github.com/hollis-labs/nanite/internal/a2a"
	"github.com/hollis-labs/nanite/internal/agent"
	"github.com/hollis-labs/nanite/internal/store"
)

// AgentConfigService is the shared database write path for operator-managed
// agent profiles. SourceRef is retained on existing rows as historical
// provenance only; this service never reads, writes, renames, or deletes the
// referenced path.
//
// Procedures written here are the bulk seed that accompanies a profile write
// (Create, Update, CopyToManaged). Editing a single procedure row, and every
// other per-row capability edit, belongs to AgentCapabilitiesService.
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
	var message string
	if class.CopyToManagedAllowed() {
		message = fmt.Sprintf("agent is not an editable database config: %q is %s and read-only in place; copy it to the managed layer (CopyToManaged) and %s the copy", slug, class.Describe(), verb)
	} else {
		message = fmt.Sprintf("agent is not an editable database config: %q is %s, which Nanite manages; there is no copy-to-managed path for it", slug, class.Describe())
	}
	return svcerr.Wrap(ErrAgentNotManaged, svcerr.CodePermission, message)
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
	return s.CreateWithAssignments(context.Background(), profile, procedures, AgentAssignments{})
}

// CreateWithAssignments validates client input before an atomic profile/child write.
func (s *AgentConfigService) CreateWithAssignments(ctx context.Context, profile *store.AgentProfile, procedures []agent.ProcedureDefinition, a AgentAssignments) (*AgentConfigResult, error) {
	if profile == nil {
		return nil, fmt.Errorf("profile is required")
	}
	if err := agent.ValidateSlug(profile.Slug); err != nil {
		return nil, svcerr.Wrap(err, svcerr.CodeInvalid, err.Error(), svcerr.WithField("slug"))
	}
	if profile.Slug == a2a.UserSentinel {
		return nil, svcerr.New(svcerr.CodeInvalid, "slug \"user\" is reserved for messaging", svcerr.WithField("slug"))
	}
	if field, err := store.ValidateAgentBehaviorFields(profile); err != nil {
		return nil, svcerr.Wrap(err, svcerr.CodeInvalid, err.Error(), svcerr.WithField(field))
	}
	if err := validateAssignmentACP(profile, a); err != nil {
		return nil, err
	}
	if existing, err := s.store.GetAgentBySlug(ctx, profile.Slug); err == nil && existing != nil {
		return nil, svcerr.Wrap(ErrManagedSlugExists, svcerr.CodeConflict, "a managed agent with this slug already exists")
	} else if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, svcerr.Wrap(err, svcerr.CodeInternal, "failed to create agent")
	}

	profile.ID = ""
	profile.Source = "user"
	profile.SourceRef = ""
	profile.ImportedAt = ""
	profile.OriginSystem = ""
	saved, err := s.store.CreateAgentConfig(ctx, profile, a, agentConfigSeeds(profile, procedures))
	if err != nil {
		return nil, agentConfigWriteError(err, "failed to create agent")
	}
	s.emit(saved.Slug, "created")
	return &AgentConfigResult{Profile: saved, Class: s.Classify(saved)}, nil
}

// Update persists a managed profile directly in the database. Procedures are
// relational data and remain untouched unless explicitly supplied. The former
// revision token is accepted for wire compatibility but has no filesystem
// concurrency meaning.
func (s *AgentConfigService) Update(existing, updated *store.AgentProfile, procedures []agent.ProcedureDefinition, revision string) (*AgentConfigResult, error) {
	return s.UpdateWithAssignments(context.Background(), existing, updated, procedures, revision, AgentAssignments{})
}

// UpdateWithAssignments keeps rejected edits from changing the profile or children.
func (s *AgentConfigService) UpdateWithAssignments(ctx context.Context, existing, updated *store.AgentProfile, procedures []agent.ProcedureDefinition, _ string, a AgentAssignments) (*AgentConfigResult, error) {
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
		return nil, svcerr.Wrap(err, svcerr.CodeInvalid, err.Error(), svcerr.WithField("slug"))
	}
	if field, err := store.ValidateAgentBehaviorFields(updated); err != nil {
		return nil, svcerr.Wrap(err, svcerr.CodeInvalid, err.Error(), svcerr.WithField(field))
	}
	if err := validateAssignmentACP(updated, a); err != nil {
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
	saved, err := s.store.UpdateAgentConfig(ctx, updated, a, agentConfigSeeds(updated, procedures))
	if err != nil {
		return nil, agentConfigWriteError(err, "failed to update agent")
	}
	s.emit(saved.Slug, "updated")
	return &AgentConfigResult{Profile: saved, Class: s.Classify(saved)}, nil
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

// AgentAssignments carries the DB-only columns a profile write does not
// cover: the role/consumer/model composition FKs and the ACP
// protocol/transport pair. Each field is a pointer: nil leaves the column
// untouched, a pointer to "" clears it, a non-empty value sets it.
type AgentAssignments = store.AgentAssignments

func validateAssignmentACP(p *store.AgentProfile, a AgentAssignments) error {
	protocol, transport := p.Protocol, p.Transport
	if a.Protocol != nil {
		protocol = *a.Protocol
	}
	if a.Transport != nil {
		transport = *a.Transport
	}
	if err := store.ValidateAgentACPFields(protocol, transport); err != nil {
		return svcerr.Wrap(err, svcerr.CodeInvalid, err.Error())
	}
	return nil
}

func agentConfigSeeds(p *store.AgentProfile, procedures []agent.ProcedureDefinition) store.AgentConfigSeeds {
	seeds := store.AgentConfigSeeds{Tools: jsonNameList(p.RoleTools), Skills: jsonNameList(p.RoleSkills)}
	for _, row := range procedures {
		seeds.Procedures = append(seeds.Procedures, store.AgentProcedure{Name: row.Name, Body: row.Body, Scope: row.Scope})
	}
	return seeds
}

func agentConfigWriteError(err error, fallback string) error {
	var stage *store.AgentConfigWriteError
	if errors.As(err, &stage) {
		switch stage.Step {
		case "assignments":
			return agentAssignmentWriteError(err)
		case "create", "update":
			if store.IsUniqueConstraint(err) {
				return svcerr.Wrap(errors.Join(ErrManagedSlugExists, err), svcerr.CodeConflict, "a managed agent with this slug already exists")
			}
		case "trust":
			fallback = "failed to set agent trust tier"
		case "read":
			fallback = "failed to read saved agent"
		}
	}
	return svcerr.Wrap(err, svcerr.CodeInternal, fallback)
}

// Only foreign-key/reference validation failures identify caller-correctable
// composition input. The store owns the SQLite-specific classification.
func agentAssignmentWriteError(err error) error {
	var reference *store.AgentAssignmentReferenceError
	if errors.As(err, &reference) {
		return svcerr.Wrap(err, svcerr.CodeInvalid, "agent assignment does not reference an existing role, consumer, or model", svcerr.WithField(reference.Field))
	}
	if store.IsForeignKeyViolation(err) {
		return svcerr.Wrap(err, svcerr.CodeInvalid, "agent assignment does not reference an existing role, consumer, or model")
	}
	return svcerr.Wrap(err, svcerr.CodeInternal, "failed to update agent assignments")
}

// CopyToManaged forks plugin or explicitly external provenance into a fresh
// operator-owned database row. SourceRef is deliberately not dereferenced.
func (s *AgentConfigService) CopyToManaged(source *store.AgentProfile, procedures []agent.ProcedureDefinition) (*AgentConfigResult, error) {
	if source == nil {
		return nil, fmt.Errorf("source profile is required")
	}
	class := s.Classify(source)
	if class.Editable() {
		return nil, svcerr.Wrap(ErrAgentAlreadyManaged, svcerr.CodeConflict, "agent is already a managed config")
	}
	if !class.CopyToManagedAllowed() {
		return nil, notManagedError(source, class, "copy")
	}
	if procedures == nil {
		rows, err := s.store.ListAgentProcedures(context.TODO() /* TODO(ctx-sweep): no ctx available at this call site */, source.ID)
		if err != nil {
			return nil, svcerr.Wrap(err, svcerr.CodeInternal, "failed to read source agent procedures")
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

func jsonNameList(raw string) []string {
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
