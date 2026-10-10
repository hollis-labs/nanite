package store

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"regexp"
	"strings"

	"github.com/google/uuid"
	"github.com/hollis-labs/nanite/internal/agentpolicy"
	"github.com/hollis-labs/nanite/internal/harnessprofile"
	mesh "github.com/hollis-labs/substrate/mesh"
	"github.com/hollis-labs/substrate/mesh/agentdef"
)

var (
	ErrDefinitionRevisionConflict = errors.New("definition revision already names different artifact bytes")
	ErrDefinitionContent          = errors.New("definition content does not satisfy its pin")
	ErrAgentHostSlugConflict      = errors.New("agent host settings slug already exists")
	ErrAgentHostRevisionConflict  = errors.New("agent host settings revision changed")
	ErrVerifiedActorRequired      = errors.New("verified actor binding is required")
)

type DefinitionResource struct {
	URI     string
	Content []byte
}
type DefinitionArtifact struct {
	Ref            mesh.DefinitionRef
	ArtifactDigest string
	Data           []byte
}

// InstallAgentDefinition is explicit authoring/install work, not a seed or a
// profile converter. Every referenced byte is checked before any transaction;
// installation creates no actor, binding, familiar catalog or grant rows.
func (s *Store) InstallAgentDefinition(ctx context.Context, data []byte, resources []DefinitionResource) (mesh.DefinitionRef, error) {
	if len(data) > 1<<20 {
		return mesh.DefinitionRef{}, errors.New("definition exceeds 1 MiB")
	}
	d, err := agentdef.Parse(data, agentpolicy.Option())
	if err != nil {
		return mesh.DefinitionRef{}, err
	}
	if len(d.Requirements.Skills) != 0 {
		return mesh.DefinitionRef{}, errors.New("skill installation requires a verified whole packaged tree")
	}
	digest, err := agentdef.Digest(d)
	if err != nil {
		return mesh.DefinitionRef{}, err
	}
	pin := mesh.DefinitionRef{ID: d.DefinitionID, Revision: d.Revision, Digest: digest}
	refs := append([]agentdef.Ref{}, d.Behavior.Instructions...)
	refs = append(refs, d.Behavior.SOPs...)
	refs = append(refs, d.Requirements.Resources...)
	refs = append(refs, d.HarnessProfile.Steering...)
	refs = append(refs, d.HarnessProfile.Context.Sources...)
	refs = append(refs, d.HarnessProfile.Approvals...)
	refs = append(refs, d.HarnessProfile.Escalation...)
	for _, r := range []*agentdef.Ref{d.HarnessProfile.Context.Policy, d.Continuity.MemoryPolicy, d.Continuity.RecoveryStrategy} {
		if r != nil {
			refs = append(refs, *r)
		}
	}
	for _, c := range d.Capabilities {
		for _, r := range []*agentdef.Ref{c.Input, c.Output} {
			if r != nil {
				refs = append(refs, *r)
			}
		}
	}
	var reflexRef *agentdef.Ref
	if e, ok := d.Extensions[agentpolicy.ReflexNamespace]; ok {
		p, policyErr := agentpolicy.DecodeReflex(e)
		if policyErr != nil {
			return pin, policyErr
		}
		reflexRef = &p.Bundle
		refs = append(refs, p.Bundle)
	}
	contents := map[string][]byte{}
	for _, r := range resources {
		if _, exists := contents[r.URI]; exists || len(r.Content) > 1<<20 {
			return pin, errors.New("duplicate or oversized definition resource")
		}
		contents[r.URI] = r.Content
	}
	used := map[string]string{}
	for _, r := range refs {
		body, ok := contents[r.URI]
		if !ok {
			return pin, fmt.Errorf("%w: missing resource %q", ErrDefinitionContent, r.URI)
		}
		if prior, ok := used[r.URI]; ok && prior != r.Digest {
			return pin, fmt.Errorf("%w: conflicting resource pins", ErrDefinitionContent)
		}
		used[r.URI] = r.Digest
		if reflexRef != nil && r == *reflexRef {
			if _, bundleErr := agentpolicy.ParseReflexBundle(body, r); bundleErr != nil {
				return pin, bundleErr
			}
		} else if agentdef.ArtifactDigest(body) != r.Digest {
			return pin, fmt.Errorf("%w: resource %q", ErrDefinitionContent, r.URI)
		}
	}
	if len(used) != len(contents) {
		return pin, errors.New("unreferenced definition resource")
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return pin, err
	}
	defer rollbackUnlessCommitted(tx)
	artifactDigest := agentdef.ArtifactDigest(data)
	if _, err := tx.ExecContext(ctx, `INSERT INTO agent_definitions(definition_id,revision,semantic_digest,artifact_digest,artifact) VALUES(?,?,?,?,?) ON CONFLICT(definition_id,revision) DO NOTHING`, pin.ID, pin.Revision, pin.Digest, artifactDigest, data); err != nil {
		return pin, err
	}
	var existingDigest string
	var existing []byte
	if err := tx.QueryRowContext(ctx, `SELECT artifact_digest,artifact FROM agent_definitions WHERE definition_id=? AND revision=?`, pin.ID, pin.Revision).Scan(&existingDigest, &existing); err != nil {
		return pin, err
	}
	if existingDigest != artifactDigest || !bytes.Equal(existing, data) {
		return pin, ErrDefinitionRevisionConflict
	}
	for uri, digest := range used {
		body := contents[uri]
		if _, err := tx.ExecContext(ctx, `INSERT INTO agent_definition_resources(digest,content) VALUES(?,?) ON CONFLICT(digest) DO NOTHING`, digest, body); err != nil {
			return pin, err
		}
		var stored []byte
		if err := tx.QueryRowContext(ctx, `SELECT content FROM agent_definition_resources WHERE digest=?`, digest).Scan(&stored); err != nil {
			return pin, err
		}
		if !bytes.Equal(stored, body) {
			return pin, ErrDefinitionContent
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO agent_definition_resource_refs(definition_id,revision,uri,digest) VALUES(?,?,?,?) ON CONFLICT(definition_id,revision,uri) DO NOTHING`, pin.ID, pin.Revision, uri, digest); err != nil {
			return pin, err
		}
		var linked string
		if err := tx.QueryRowContext(ctx, `SELECT digest FROM agent_definition_resource_refs WHERE definition_id=? AND revision=? AND uri=?`, pin.ID, pin.Revision, uri).Scan(&linked); err != nil {
			return pin, err
		}
		if linked != digest {
			return pin, ErrDefinitionContent
		}
	}
	return pin, tx.Commit()
}

func (s *Store) GetAgentDefinitionArtifact(ctx context.Context, pin mesh.DefinitionRef) (DefinitionArtifact, error) {
	r := DefinitionArtifact{}
	err := s.DB.QueryRowContext(ctx, `SELECT definition_id,revision,semantic_digest,artifact_digest,artifact FROM agent_definitions WHERE definition_id=? AND revision=?`, pin.ID, pin.Revision).Scan(&r.Ref.ID, &r.Ref.Revision, &r.Ref.Digest, &r.ArtifactDigest, &r.Data)
	if err != nil {
		return r, err
	}
	if r.Ref != pin || agentdef.ArtifactDigest(r.Data) != r.ArtifactDigest {
		return r, ErrDefinitionContent
	}
	return r, nil
}

func (s *Store) GetAgentDefinitionResource(ctx context.Context, pin mesh.DefinitionRef, ref agentdef.Ref) ([]byte, error) {
	var body []byte
	err := s.DB.QueryRowContext(ctx, `SELECT r.content FROM agent_definition_resources r JOIN agent_definition_resource_refs l ON l.digest=r.digest JOIN agent_definitions d ON d.definition_id=l.definition_id AND d.revision=l.revision WHERE d.definition_id=? AND d.revision=? AND d.semantic_digest=? AND l.uri=? AND l.digest=?`, pin.ID, pin.Revision, pin.Digest, ref.URI, ref.Digest).Scan(&body)
	if err != nil {
		return nil, err
	}
	if agentdef.ArtifactDigest(body) != ref.Digest {
		return nil, ErrDefinitionContent
	}
	return body, nil
}

// NativeHostSettings is an execution-input document, never intrinsic behavior
// or an actor grant. Model authorization and workspace custody are enforced by
// the using host. Numeric zero is retained distinctly from an absent pointer.
type NativeHostSettings struct {
	Version         string               `json:"version"`
	Provider        string               `json:"provider,omitempty"`
	Model           string               `json:"model,omitempty"`
	Runtime         string               `json:"runtime"`
	Protocol        string               `json:"protocol,omitempty"`
	Transport       string               `json:"transport,omitempty"`
	HarnessProfile  string               `json:"harness_profile,omitempty"`
	NativeLoop      harnessprofile.Layer `json:"native_loop"`
	RecallLimit     *int                 `json:"recall_limit,omitempty"`
	RecallTimeoutMS *int64               `json:"recall_timeout_ms,omitempty"`
	Directories     []string             `json:"directories,omitempty"`
	MCPServers      []string             `json:"mcp_servers,omitempty"`
	Debug           bool                 `json:"debug,omitempty"`
}

func (h NativeHostSettings) Validate() error {
	if h.Version != "1" || (h.Runtime != "api" && h.Runtime != "cli") {
		return errors.New("host settings require version 1 and runtime api or cli")
	}
	if err := ValidateAgentACPFields(h.Protocol, h.Transport); err != nil {
		return err
	}
	if h.Runtime == "api" && (h.Protocol != "" || h.Transport != "") {
		return errors.New("native API host settings cannot select a CLI transport")
	}
	if (h.RecallLimit != nil && *h.RecallLimit < 0) || (h.RecallTimeoutMS != nil && *h.RecallTimeoutMS < 0) {
		return errors.New("recall host limits must not be negative")
	}
	if h.RecallTimeoutMS != nil && *h.RecallTimeoutMS > math.MaxInt64/1_000_000 {
		return errors.New("recall timeout exceeds duration range")
	}
	// Validate authored host knobs with the authoritative host profile parser.
	b, err := json.Marshal(harnessprofile.Profile{Name: "host-settings", Layer: h.NativeLoop})
	if err != nil {
		return err
	}
	_, err = harnessprofile.ParseProfile(b)
	return err
}

type AgentHostSettings struct {
	ID            string             `json:"id"`
	Slug          string             `json:"slug"`
	Title         string             `json:"title"`
	DefinitionRef mesh.DefinitionRef `json:"definition_ref"`
	Settings      NativeHostSettings `json:"settings"`
	Enabled       bool               `json:"enabled"`
	Source        string             `json:"source"`
	PluginID      string             `json:"plugin_id,omitempty"`
	Revision      string             `json:"revision"`
}

var hostSlugPattern = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

func (s *Store) CreateAgentHostSettings(ctx context.Context, input AgentHostSettings) (AgentHostSettings, error) {
	if !hostSlugPattern.MatchString(input.Slug) || input.Slug == "user" || strings.TrimSpace(input.Title) == "" || strings.TrimSpace(input.Source) == "" {
		return input, errors.New("host settings require slug, title and accountable source")
	}
	if err := input.Settings.Validate(); err != nil {
		return input, err
	}
	if input.ID != "" || input.Revision != "" {
		return input, errors.New("host settings identity and revision are assigned by the host")
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return input, err
	}
	defer rollbackUnlessCommitted(tx)
	var duplicate bool
	if queryErr := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM agent_host_settings WHERE slug=?)`, input.Slug).Scan(&duplicate); queryErr != nil {
		return input, queryErr
	}
	if duplicate {
		return input, ErrAgentHostSlugConflict
	}
	if retiredErr := checkProfileIngestionRetired(ctx, tx, "", input.Slug); retiredErr != nil {
		return input, retiredErr
	}
	input.ID = uuid.NewString()
	input.Revision = uuid.NewString()
	b, err := json.Marshal(input.Settings)
	if err != nil {
		return input, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO agent_host_settings(id,slug,title,definition_id,definition_revision,semantic_digest,settings_json,enabled,source,plugin_id,revision) VALUES(?,?,?,?,?,?,?,?,?,?,?)`, input.ID, input.Slug, input.Title, input.DefinitionRef.ID, input.DefinitionRef.Revision, input.DefinitionRef.Digest, string(b), input.Enabled, input.Source, input.PluginID, input.Revision)
	if err != nil {
		if IsUniqueConstraint(err) {
			return input, ErrAgentHostSlugConflict
		}
		return input, err
	}
	return input, tx.Commit()
}

func (s *Store) GetAgentHostSettings(ctx context.Context, id string) (AgentHostSettings, error) {
	var h AgentHostSettings
	var settings string
	err := s.DB.QueryRowContext(ctx, `SELECT id,slug,title,definition_id,definition_revision,semantic_digest,settings_json,enabled,source,plugin_id,revision FROM agent_host_settings WHERE id=?`, id).Scan(&h.ID, &h.Slug, &h.Title, &h.DefinitionRef.ID, &h.DefinitionRef.Revision, &h.DefinitionRef.Digest, &settings, &h.Enabled, &h.Source, &h.PluginID, &h.Revision)
	if err != nil {
		return h, err
	}
	dec := json.NewDecoder(strings.NewReader(settings))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&h.Settings); err != nil {
		return h, err
	}
	if err := dec.Decode(new(any)); err != io.EOF {
		return h, errors.New("host settings must be one JSON document")
	}
	return h, h.Settings.Validate()
}

func (s *Store) ListAgentHostSettings(ctx context.Context) ([]AgentHostSettings, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id FROM agent_host_settings ORDER BY title,id`)
	if err != nil {
		return nil, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			closeRows(rows)
			return nil, err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	closeRows(rows)
	if err != nil {
		return nil, err
	}
	out := make([]AgentHostSettings, 0, len(ids))
	for _, id := range ids {
		h, err := s.GetAgentHostSettings(ctx, id)
		if err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, nil
}

// UpdateAgentHostSettings is a complete host-input replacement with a revision
// compare-and-swap. It changes no immutable content, actor identity or grant.
func (s *Store) UpdateAgentHostSettings(ctx context.Context, input AgentHostSettings, expectedRevision string) (AgentHostSettings, error) {
	if input.ID == "" || expectedRevision == "" || input.Revision != expectedRevision {
		return input, ErrAgentHostRevisionConflict
	}
	if !hostSlugPattern.MatchString(input.Slug) || input.Slug == "user" || strings.TrimSpace(input.Title) == "" {
		return input, errors.New("host settings require slug and title")
	}
	if err := input.Settings.Validate(); err != nil {
		return input, err
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return input, err
	}
	defer rollbackUnlessCommitted(tx)
	if err = checkProfileIngestionRetired(ctx, tx, input.ID, input.Slug); err != nil {
		return input, err
	}
	// An existing actor receipt is not approval for a different definition.
	// Until a verified rebind port is adopted, edits may change host execution
	// inputs but cannot substitute intrinsic content under an enabled actor.
	var pinChangeBound bool
	if bindingErr := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM agent_actor_bindings b JOIN agent_host_settings h ON h.id=b.host_settings_id WHERE h.id=? AND b.enabled=1 AND (h.definition_id!=? OR h.definition_revision!=? OR h.semantic_digest!=?))`, input.ID, input.DefinitionRef.ID, input.DefinitionRef.Revision, input.DefinitionRef.Digest).Scan(&pinChangeBound); bindingErr != nil {
		return input, bindingErr
	}
	if pinChangeBound {
		return input, ErrVerifiedActorRequired
	}
	b, err := json.Marshal(input.Settings)
	if err != nil {
		return input, err
	}
	input.Revision = uuid.NewString()
	res, err := tx.ExecContext(ctx, `UPDATE agent_host_settings SET slug=?,title=?,definition_id=?,definition_revision=?,semantic_digest=?,settings_json=?,enabled=?,revision=?,updated_at=strftime('%Y-%m-%dT%H:%M:%fZ','now') WHERE id=? AND revision=?`, input.Slug, input.Title, input.DefinitionRef.ID, input.DefinitionRef.Revision, input.DefinitionRef.Digest, string(b), input.Enabled, input.Revision, input.ID, expectedRevision)
	if err != nil {
		return input, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return input, err
	}
	if n != 1 {
		return input, ErrAgentHostRevisionConflict
	}
	return input, tx.Commit()
}

func (s *Store) DeleteAgentHostSettings(ctx context.Context, id, expectedRevision string) error {
	if id == "" || expectedRevision == "" {
		return ErrAgentHostRevisionConflict
	}
	res, err := s.DB.ExecContext(ctx, `DELETE FROM agent_host_settings WHERE id=? AND revision=?`, id, expectedRevision)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrAgentHostRevisionConflict
	}
	return nil
}

func (s *Store) ListAgentDefinitionArtifacts(ctx context.Context) ([]DefinitionArtifact, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT definition_id,revision,semantic_digest,artifact_digest,artifact FROM agent_definitions ORDER BY definition_id,revision`)
	if err != nil {
		return nil, err
	}
	defer closeRows(rows)
	out := make([]DefinitionArtifact, 0)
	for rows.Next() {
		var a DefinitionArtifact
		if err = rows.Scan(&a.Ref.ID, &a.Ref.Revision, &a.Ref.Digest, &a.ArtifactDigest, &a.Data); err != nil {
			return nil, err
		}
		if agentdef.ArtifactDigest(a.Data) != a.ArtifactDigest {
			return nil, ErrDefinitionContent
		}
		out = append(out, a)
	}
	return out, rows.Err()
}
