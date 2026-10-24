package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	mesh "github.com/hollis-labs/libs/mesh/core"
	"github.com/hollis-labs/libs/mesh/core/agentdef"
	"github.com/hollis-labs/nanite/internal/agentpolicy"
)

// Skill grants for native views (CW-20261024-0089). A definition revision
// requests a skill by name with a digest-pinned packed tree; a grant records the
// operator's authority for one (host settings revision, definition pin) to
// receive that skill at exactly that digest. It follows the tool grant rules in
// every other respect: it hangs off the live native_authorities row, is written
// only by LocalAuthority on behalf of an operator-authenticated caller, records
// the granting credential id (never anything from the request body), and leaves
// an append-only audit row in the same transaction.

var (
	ErrSkillNotRequested   = errors.New("skill_not_requested: the authority's definition does not request this skill")
	ErrSkillDigestMismatch = errors.New("skill_digest_mismatch: the digest is not the one the definition pins for this skill")
	ErrSkillTreeMissing    = errors.New("skill_tree_missing: the pinned skill content is not stored or does not decode")
	ErrSkillGrantConflict  = errors.New("skill_grant_conflict: this skill is already granted at a different digest; revoke it first")
	ErrSkillInvalid        = errors.New("invalid skill name or digest")
)

var (
	skillNameRE   = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)
	skillDigestRE = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
)

// ValidSkillName reports whether name is a legal requested-skill name.
func ValidSkillName(name string) bool { return len(name) <= 64 && skillNameRE.MatchString(name) }

// ValidSkillDigest reports whether digest is a sha256:<64 hex> digest.
func ValidSkillDigest(digest string) bool { return skillDigestRE.MatchString(digest) }

// NativeAuthoritySkill is one granted skill.
type NativeAuthoritySkill struct {
	Name      string `json:"name"`
	Digest    string `json:"digest"`
	GrantedAt string `json:"granted_at"`
}

// SkillGrantResult is the outcome of GrantSkill.
type SkillGrantResult struct {
	AuthorityID    string `json:"authority_id"`
	Skill          string `json:"skill"`
	Digest         string `json:"digest"`
	GrantedAt      string `json:"granted_at"`
	AlreadyGranted bool   `json:"already_granted"`
}

type skillReceipt struct {
	receipt
	SkillDigest string `json:"skill_digest"`
}

func nowRFC3339() string { return time.Now().UTC().Format(time.RFC3339Nano) }

// requestedSkill finds the skill ref the authority's definition pins under name.
func (s *Store) requestedSkill(ctx context.Context, pin mesh.DefinitionRef, name string) (agentdef.Ref, error) {
	art, err := s.GetAgentDefinitionArtifact(ctx, pin)
	if err != nil {
		return agentdef.Ref{}, fmt.Errorf("read the authority's definition: %w", err)
	}
	d, err := agentdef.Parse(art.Data, agentpolicy.Option())
	if err != nil {
		return agentdef.Ref{}, fmt.Errorf("parse the authority's definition: %w", err)
	}
	for _, sk := range d.Requirements.Skills {
		if sk.Name == name {
			return sk.Content, nil
		}
	}
	return agentdef.Ref{}, ErrSkillNotRequested
}

// GrantSkill grants a requested skill, by name and pinned digest, to a live
// authority. A repeat grant of the same digest is a no-op that keeps the
// original receipt; a different digest for an already granted skill is refused.
func (l *LocalAuthority) GrantSkill(ctx context.Context, grantedBy, authorityID, skill, digest, reason string) (SkillGrantResult, error) {
	var res SkillGrantResult
	if strings.TrimSpace(grantedBy) == "" {
		return res, ErrNativeAuthorityActor
	}
	if !ValidSkillName(skill) || !ValidSkillDigest(digest) {
		return res, ErrSkillInvalid
	}
	a, err := l.GetAuthority(ctx, authorityID)
	if err != nil {
		return res, err
	}
	if !a.Enabled || a.RevokedAt != "" {
		return res, ErrNativeAuthorityRevoked
	}
	pin := mesh.DefinitionRef{ID: a.DefinitionID, Revision: a.DefinitionRevision, Digest: a.SemanticDigest}
	ref, err := l.s.requestedSkill(ctx, pin, skill)
	if err != nil {
		return res, err
	}
	if ref.Digest != digest {
		return res, ErrSkillDigestMismatch
	}
	body, err := l.s.GetAgentDefinitionResource(ctx, pin, ref)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return res, ErrSkillTreeMissing
		}
		return res, err
	}
	if _, err = agentpolicy.DecodeSkillTree(body); err != nil {
		return res, ErrSkillTreeMissing
	}
	rc, err := json.Marshal(skillReceipt{receipt: receipt{Authority: NativeAuthorityName, V: 1, GrantedBy: strings.TrimSpace(grantedBy), HostSettingsRevision: a.HostSettingsRevision, DefinitionDigest: a.SemanticDigest, Reason: reason, At: nowRFC3339()}, SkillDigest: digest})
	if err != nil {
		return res, err
	}
	tx, err := l.s.DB.BeginTx(ctx, nil)
	if err != nil {
		return res, err
	}
	defer func() { _ = tx.Rollback() }()
	// The authority must still be the one in force for the host settings
	// revision it reviewed, as createAuthorityTx checks.
	var current string
	if err = tx.QueryRowContext(ctx, `SELECT h.revision FROM agent_host_settings h JOIN native_authorities n ON n.host_settings_id=h.id WHERE n.id=? AND h.enabled=1 AND n.enabled=1 AND n.revoked_at IS NULL`, authorityID).Scan(&current); err != nil || current != a.HostSettingsRevision {
		return res, ErrNativeAuthorityStale
	}
	var haveDigest, grantedAt string
	err = tx.QueryRowContext(ctx, `SELECT digest,granted_at FROM native_authority_skills WHERE authority_id=? AND skill_name=?`, authorityID, skill).Scan(&haveDigest, &grantedAt)
	switch {
	case err == nil && haveDigest == digest:
		return SkillGrantResult{AuthorityID: authorityID, Skill: skill, Digest: digest, GrantedAt: grantedAt, AlreadyGranted: true}, nil
	case err == nil:
		return res, ErrSkillGrantConflict
	case !errors.Is(err, sql.ErrNoRows):
		return res, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO native_authority_skills(authority_id,skill_name,digest,receipt) VALUES(?,?,?,?)`, authorityID, skill, digest, string(rc)); err != nil {
		return res, err
	}
	if err = auditSkill(ctx, tx, authorityID, "skill_granted", skill, digest, grantedBy, reason); err != nil {
		return res, err
	}
	if err = tx.QueryRowContext(ctx, `SELECT granted_at FROM native_authority_skills WHERE authority_id=? AND skill_name=?`, authorityID, skill).Scan(&grantedAt); err != nil {
		return res, err
	}
	if err = tx.Commit(); err != nil {
		return res, err
	}
	return SkillGrantResult{AuthorityID: authorityID, Skill: skill, Digest: digest, GrantedAt: grantedAt}, nil
}

// RevokeSkill removes one skill grant; revoking a skill that is not granted is
// a no-op. It works on a revoked authority too (the grant has stopped
// resolving, but removing it keeps the record clean).
func (l *LocalAuthority) RevokeSkill(ctx context.Context, grantedBy, authorityID, skill, reason string) error {
	if strings.TrimSpace(grantedBy) == "" {
		return ErrNativeAuthorityActor
	}
	if !ValidSkillName(skill) {
		return ErrSkillInvalid
	}
	if _, err := l.GetAuthority(ctx, authorityID); err != nil {
		return err
	}
	tx, err := l.s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var digest string
	err = tx.QueryRowContext(ctx, `SELECT digest FROM native_authority_skills WHERE authority_id=? AND skill_name=?`, authorityID, skill).Scan(&digest)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM native_authority_skills WHERE authority_id=? AND skill_name=?`, authorityID, skill); err != nil {
		return err
	}
	if err = auditSkill(ctx, tx, authorityID, "skill_revoked", skill, digest, grantedBy, reason); err != nil {
		return err
	}
	return tx.Commit()
}

// ListSkills returns the skills granted to an authority, by name.
func (l *LocalAuthority) ListSkills(ctx context.Context, authorityID string) ([]NativeAuthoritySkill, error) {
	rows, err := l.s.DB.QueryContext(ctx, `SELECT skill_name,digest,granted_at FROM native_authority_skills WHERE authority_id=? ORDER BY skill_name`, authorityID)
	if err != nil {
		return nil, err
	}
	defer closeRows(rows)
	out := make([]NativeAuthoritySkill, 0)
	for rows.Next() {
		var k NativeAuthoritySkill
		if err = rows.Scan(&k.Name, &k.Digest, &k.GrantedAt); err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

func auditSkill(ctx context.Context, tx *sql.Tx, authorityID, action, skill, digest, grantedBy, reason string) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO native_authority_audit(authority_id,action,granted_by,reason,skill_name,skill_digest) VALUES(?,?,?,?,?,?)`, authorityID, action, strings.TrimSpace(grantedBy), reason, skill, digest)
	return err
}

// GrantedSkillDigests returns skill name -> granted digest for the live
// authority in force for (host settings id, revision), but only when that
// authority's definition pin still equals the host's current pin. A grant is
// never carried to another host revision or definition pin.
func (s *Store) GrantedSkillDigests(ctx context.Context, hostID, revision string) (map[string]string, error) {
	rows, err := s.DB.QueryContext(ctx, `
		SELECT k.skill_name,k.digest
		  FROM agent_host_settings h
		  JOIN native_authorities a ON a.host_settings_id = h.id
		   AND a.host_settings_revision = h.revision
		   AND a.definition_id = h.definition_id
		   AND a.definition_revision = h.definition_revision
		   AND a.semantic_digest = h.semantic_digest
		  JOIN native_authority_skills k ON k.authority_id = a.id
		 WHERE h.id = ? AND h.revision = ? AND h.enabled = 1 AND a.enabled = 1 AND a.revoked_at IS NULL`, hostID, revision)
	if err != nil {
		return nil, fmt.Errorf("resolve native skill grants: %w", err)
	}
	defer closeRows(rows)
	out := map[string]string{}
	for rows.Next() {
		var name, digest string
		if err = rows.Scan(&name, &digest); err != nil {
			return nil, err
		}
		out[name] = digest
	}
	return out, rows.Err()
}

// GrantedSkillTrees returns, for each requested skill of d that is granted at
// its currently pinned digest for the host settings revision, its decoded file
// tree (relative path -> bytes). A skill that is not granted, whose granted
// digest differs from the pin, or whose stored tree is missing or malformed is
// left out: nothing unauthorized or unverifiable is ever delivered.
func (s *Store) GrantedSkillTrees(ctx context.Context, d *agentdef.Definition, host AgentHostSettings) (map[string]map[string][]byte, error) {
	out := map[string]map[string][]byte{}
	if d == nil || len(d.Requirements.Skills) == 0 {
		return out, nil
	}
	granted, err := s.GrantedSkillDigests(ctx, host.ID, host.Revision)
	if err != nil {
		return nil, err
	}
	for _, sk := range d.Requirements.Skills {
		if granted[sk.Name] == "" || granted[sk.Name] != sk.Content.Digest {
			continue
		}
		body, rerr := s.GetAgentDefinitionResource(ctx, host.DefinitionRef, sk.Content)
		if rerr != nil {
			if errors.Is(rerr, sql.ErrNoRows) {
				continue
			}
			return nil, rerr
		}
		tree, derr := agentpolicy.DecodeSkillTree(body)
		if derr != nil {
			continue
		}
		out[sk.Name] = tree
	}
	return out, nil
}

// SortedKeys is a small helper for deterministic iteration in callers.
func SortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// carrySkillGrantsTx re-grants to authority the skills `from` holds, as part of
// an explicit grant_same_as, but only those the new definition d still requests
// at the very same pinned digest (and whose tree this transaction can read). A
// changed digest, a dropped request or a skill already granted is skipped:
// re-approval of changed content is a separate, explicit act.
func carrySkillGrantsTx(ctx context.Context, tx *sql.Tx, d *agentdef.Definition, from AgentHostSettings, authority NativeAuthority, grantedBy, reason string) ([]string, error) {
	if len(d.Requirements.Skills) == 0 {
		return nil, nil
	}
	rows, err := tx.QueryContext(ctx, `SELECT k.skill_name,k.digest FROM native_authorities a JOIN native_authority_skills k ON k.authority_id=a.id WHERE a.host_settings_id=? AND a.host_settings_revision=? AND a.enabled=1 AND a.revoked_at IS NULL`, from.ID, from.Revision)
	if err != nil {
		return nil, err
	}
	held := map[string]string{}
	for rows.Next() {
		var name, digest string
		if err = rows.Scan(&name, &digest); err != nil {
			_ = rows.Close()
			return nil, err
		}
		held[name] = digest
	}
	if err = rows.Close(); err != nil {
		return nil, err
	}
	var added []string
	for _, sk := range d.Requirements.Skills {
		if held[sk.Name] == "" || held[sk.Name] != sk.Content.Digest {
			continue
		}
		var body []byte
		err = tx.QueryRowContext(ctx, `SELECT content FROM agent_definition_resources WHERE digest=?`, sk.Content.Digest).Scan(&body)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if _, derr := agentpolicy.DecodeSkillTree(body); derr != nil {
			continue
		}
		rc, err := json.Marshal(skillReceipt{receipt: receipt{Authority: NativeAuthorityName, V: 1, GrantedBy: strings.TrimSpace(grantedBy), HostSettingsRevision: authority.HostSettingsRevision, DefinitionDigest: authority.SemanticDigest, Reason: reason, At: nowRFC3339()}, SkillDigest: sk.Content.Digest})
		if err != nil {
			return nil, err
		}
		res, err := tx.ExecContext(ctx, `INSERT INTO native_authority_skills(authority_id,skill_name,digest,receipt) VALUES(?,?,?,?) ON CONFLICT(authority_id,skill_name) DO NOTHING`, authority.ID, sk.Name, sk.Content.Digest, string(rc))
		if err != nil {
			return nil, err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			continue
		}
		if err = auditSkill(ctx, tx, authority.ID, "skill_granted", sk.Name, sk.Content.Digest, grantedBy, reason); err != nil {
			return nil, err
		}
		added = append(added, sk.Name)
	}
	return added, nil
}
