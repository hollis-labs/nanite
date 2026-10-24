package store

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"strings"
	"testing"

	"github.com/hollis-labs/libs/mesh/core/agentdef"
	"github.com/hollis-labs/nanite/internal/agentpolicy"
	"github.com/pressly/goose/v3"
)

var reviewSkillFiles = map[string][]byte{"SKILL.md": []byte("# review\n"), "scripts/run.sh": []byte("echo ok\n")}

// skillDef is a definition that requests one skill, "review", pinned to files.
func skillDef(t *testing.T, id, revision string, files map[string][]byte) ([]byte, []DefinitionResource, string) {
	t.Helper()
	blob, err := agentpolicy.EncodeSkillTree(files)
	if err != nil {
		t.Fatal(err)
	}
	digest := agentdef.ArtifactDigest(blob)
	def := fmt.Sprintf("---\nschema_version: \"2\"\ndefinition_id: %s\nrevision: \"%s\"\nname: skill-agent\ndescription: Requests a skill.\nbehavior:\n  purpose: Complete the requested work.\nrequirements:\n  skills:\n    - name: review\n      content: {uri: \"file:skills/review\", digest: \"%s\"}\nharness_profile:\n  context: {}\n  permissions:\n    profile: default\ncontinuity:\n  mode: ephemeral\n---\nSkill agent.\n", id, revision, digest)
	return []byte(def), []DefinitionResource{{URI: "file:skills/review", Content: blob}}, digest
}

// skillHost installs a skill-requesting definition, creates host settings and a
// live authority, and returns them with the skill's pinned digest.
func skillHost(t *testing.T, s *Store, slug, revision string, files map[string][]byte) (AgentHostSettings, NativeAuthority, string) {
	t.Helper()
	ctx := context.Background()
	def, res, digest := skillDef(t, "def:skill-"+slug, revision, files)
	pin, err := s.InstallAgentDefinition(ctx, def, res)
	if err != nil {
		t.Fatal(err)
	}
	host, err := s.CreateAgentHostSettings(ctx, AgentHostSettings{Slug: slug, Title: slug, DefinitionRef: pin,
		Settings: NativeHostSettings{Version: "1", Runtime: "api", Provider: "fixture-provider", Model: "fixture-model"},
		Enabled:  true, Source: "explicit-private-test-authoring"})
	if err != nil {
		t.Fatal(err)
	}
	auth, err := s.LocalAuthority().CreateAuthority(ctx, "cred-1", host.ID, "review")
	if err != nil {
		t.Fatal(err)
	}
	return host, auth, digest
}

func skillState(t *testing.T, s *Store, hostID string) string {
	t.Helper()
	v, err := s.GetAgentView(context.Background(), hostID)
	if err != nil {
		t.Fatal(err)
	}
	if len(v.Requests.Skills) != 1 {
		t.Fatalf("skill requests = %v", v.Requests.Skills)
	}
	return v.Requests.Skills[0].State
}

func TestSkillGrantRefusals(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	la := s.LocalAuthority()
	host, auth, digest := skillHost(t, s, "refusals", "1", reviewSkillFiles)
	wrong := "sha256:" + strings.Repeat("a", 64)
	before := authorityRowCount(t, s, "native_authority_audit")
	for name, c := range map[string]struct {
		by, authority, skill, digest string
		want                         error
	}{
		"no credential":       {"", auth.ID, "review", digest, ErrNativeAuthorityActor},
		"blank credential":    {"  ", auth.ID, "review", digest, ErrNativeAuthorityActor},
		"unknown authority":   {"c", "nope", "review", digest, ErrNativeAuthorityNotFound},
		"not requested":       {"c", auth.ID, "other", digest, ErrSkillNotRequested},
		"wrong digest":        {"c", auth.ID, "review", wrong, ErrSkillDigestMismatch},
		"bad name":            {"c", auth.ID, "Bad_Name", digest, ErrSkillInvalid},
		"path in name":        {"c", auth.ID, "../x", digest, ErrSkillInvalid},
		"malformed digest":    {"c", auth.ID, "review", "sha256:abc", ErrSkillInvalid},
		"digest wrong scheme": {"c", auth.ID, "review", "md5:" + strings.Repeat("a", 64), ErrSkillInvalid},
	} {
		if _, err := la.GrantSkill(ctx, c.by, c.authority, c.skill, c.digest, "r"); !errors.Is(err, c.want) {
			t.Errorf("%s: %v, want %v", name, err, c.want)
		}
	}
	if authorityRowCount(t, s, "native_authority_skills") != 0 || authorityRowCount(t, s, "native_authority_audit") != before {
		t.Fatal("a refused grant left rows or audit behind")
	}
	if skillState(t, s, host.ID) != "pending" {
		t.Fatal("a refused grant changed the view")
	}
}

func TestSkillGrantLifecycle(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	la := s.LocalAuthority()
	host, auth, digest := skillHost(t, s, "lifecycle", "1", reviewSkillFiles)
	if skillState(t, s, host.ID) != "pending" {
		t.Fatal("a requested skill starts pending")
	}
	res, err := la.GrantSkill(ctx, "cred-admin", auth.ID, "review", digest, "approved")
	if err != nil || res.AlreadyGranted || res.Digest != digest || res.GrantedAt == "" {
		t.Fatalf("grant = %+v, %v", res, err)
	}
	if skillState(t, s, host.ID) != "granted" {
		t.Fatal("a granted skill shows granted")
	}
	var by, action, name, dg string
	if err = s.DB.QueryRowContext(ctx, `SELECT granted_by,action,skill_name,skill_digest FROM native_authority_audit WHERE action='skill_granted'`).Scan(&by, &action, &name, &dg); err != nil || by != "cred-admin" || name != "review" || dg != digest {
		t.Fatalf("audit = %q %q %q %q, %v", by, action, name, dg, err)
	}
	var receiptJSON string
	_ = s.DB.QueryRowContext(ctx, `SELECT receipt FROM native_authority_skills WHERE authority_id=?`, auth.ID).Scan(&receiptJSON)
	if !strings.Contains(receiptJSON, `"granted_by":"cred-admin"`) || !strings.Contains(receiptJSON, `"authority":"nanite-local"`) || !strings.Contains(receiptJSON, digest) {
		t.Fatalf("receipt = %s", receiptJSON)
	}

	// Idempotent: same digest changes nothing and writes no audit.
	audits := authorityRowCount(t, s, "native_authority_audit")
	again, err := la.GrantSkill(ctx, "someone-else", auth.ID, "review", digest, "again")
	if err != nil || !again.AlreadyGranted || again.GrantedAt != res.GrantedAt {
		t.Fatalf("repeat = %+v, %v", again, err)
	}
	if authorityRowCount(t, s, "native_authority_audit") != audits {
		t.Fatal("a repeat grant wrote audit noise")
	}
	var keptBy string
	_ = s.DB.QueryRowContext(ctx, `SELECT json_extract(receipt,'$.granted_by') FROM native_authority_skills WHERE authority_id=?`, auth.ID).Scan(&keptBy)
	if keptBy != "cred-admin" {
		t.Fatalf("repeat replaced the original receipt: %q", keptBy)
	}

	// A different digest already granted is a conflict, never a swap.
	if _, err = s.DB.ExecContext(ctx, `UPDATE native_authority_skills SET digest=? WHERE authority_id=?`, "sha256:"+strings.Repeat("b", 64), auth.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = la.GrantSkill(ctx, "c", auth.ID, "review", digest, "r"); !errors.Is(err, ErrSkillGrantConflict) {
		t.Fatalf("conflict = %v", err)
	}
	// A stale granted digest (does not equal the definition's pin) is not granted.
	if skillState(t, s, host.ID) != "pending" {
		t.Fatal("a grant at a digest the definition no longer pins must show pending")
	}
	if _, err = s.DB.ExecContext(ctx, `UPDATE native_authority_skills SET digest=? WHERE authority_id=?`, digest, auth.ID); err != nil {
		t.Fatal(err)
	}

	skills, err := la.ListSkills(ctx, auth.ID)
	if err != nil || len(skills) != 1 || skills[0].Name != "review" || skills[0].Digest != digest {
		t.Fatalf("list = %v, %v", skills, err)
	}

	// Revoke: audited, back to pending; revoking again is a no-op.
	if err = la.RevokeSkill(ctx, "cred-admin", auth.ID, "review", "withdrawn"); err != nil {
		t.Fatal(err)
	}
	if skillState(t, s, host.ID) != "pending" || authorityRowCount(t, s, "native_authority_skills") != 0 {
		t.Fatal("revoke did not withdraw the grant")
	}
	n := authorityRowCount(t, s, "native_authority_audit")
	if err = la.RevokeSkill(ctx, "cred-admin", auth.ID, "review", "again"); err != nil || authorityRowCount(t, s, "native_authority_audit") != n {
		t.Fatalf("second revoke = %v", err)
	}
	var revoked int
	_ = s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM native_authority_audit WHERE action='skill_revoked' AND skill_name='review' AND granted_by='cred-admin'`).Scan(&revoked)
	if revoked != 1 {
		t.Fatalf("skill_revoked audit rows = %d", revoked)
	}
	if err = la.RevokeSkill(ctx, "", auth.ID, "review", "r"); !errors.Is(err, ErrNativeAuthorityActor) {
		t.Fatalf("revoke without credential = %v", err)
	}
}

func TestSkillGrantNeedsALiveCurrentAuthority(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	la := s.LocalAuthority()

	_, revokedAuth, digest := skillHost(t, s, "revoked", "1", reviewSkillFiles)
	if err := la.RevokeAuthority(ctx, "c", revokedAuth.ID, "r"); err != nil {
		t.Fatal(err)
	}
	if _, err := la.GrantSkill(ctx, "c", revokedAuth.ID, "review", digest, "r"); !errors.Is(err, ErrNativeAuthorityRevoked) {
		t.Fatalf("revoked authority = %v", err)
	}
	// The trigger refuses a raw insert for a revoked authority as well.
	if _, err := s.DB.ExecContext(ctx, `INSERT INTO native_authority_skills(authority_id,skill_name,digest,receipt) VALUES(?,?,?,?)`, revokedAuth.ID, "review", digest, `{"authority":"nanite-local","granted_by":"x"}`); err == nil {
		t.Fatal("a skill grant on a revoked authority must be refused by the trigger")
	}

	host, auth, digest2 := skillHost(t, s, "stale", "1", reviewSkillFiles)
	host.Title = "changed"
	if _, err := s.UpdateAgentHostSettings(ctx, host, host.Revision); err != nil {
		t.Fatal(err)
	}
	if _, err := la.GrantSkill(ctx, "c", auth.ID, "review", digest2, "r"); !errors.Is(err, ErrNativeAuthorityStale) {
		t.Fatalf("stale host revision = %v", err)
	}
	// Triggers: shape and live authority.
	_, auth3, d3 := skillHost(t, s, "shape", "1", reviewSkillFiles)
	if _, err := s.DB.ExecContext(ctx, `INSERT INTO native_authority_skills(authority_id,skill_name,digest,receipt) VALUES(?,?,?,?)`, auth3.ID, "review", d3, `{"authority":"someone","granted_by":"x"}`); err == nil {
		t.Fatal("a receipt naming another authority must be refused")
	}
	if _, err := s.DB.ExecContext(ctx, `INSERT INTO native_authority_skills(authority_id,skill_name,digest,receipt) VALUES(?,?,?,?)`, "ghost", "review", d3, `{"authority":"nanite-local","granted_by":"x"}`); err == nil {
		t.Fatal("a skill grant with no authority must be refused")
	}
}

func TestSkillGrantRefusesATreeThatIsNotStored(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	_, auth, digest := skillHost(t, s, "missing-tree", "1", reviewSkillFiles)
	// Install verifies the tree and the rows are immutable, so simulate loss by
	// dropping the guard trigger in this test's private database.
	if _, err := s.DB.ExecContext(ctx, `DROP TRIGGER agent_definition_resource_refs_no_delete`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.ExecContext(ctx, `DELETE FROM agent_definition_resource_refs WHERE digest=?`, digest); err != nil {
		t.Fatal(err)
	}
	if _, err := s.LocalAuthority().GrantSkill(ctx, "c", auth.ID, "review", digest, "r"); !errors.Is(err, ErrSkillTreeMissing) {
		t.Fatalf("missing tree = %v", err)
	}
}

// A new host settings revision (or definition revision) starts with no skill
// grants and shows the request pending again.
func TestSkillGrantsAreNotCarriedToANewRevision(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	host, auth, digest := skillHost(t, s, "rev", "1", reviewSkillFiles)
	if _, err := s.LocalAuthority().GrantSkill(ctx, "c", auth.ID, "review", digest, "r"); err != nil {
		t.Fatal(err)
	}
	host.Title = "retitled"
	updated, err := s.UpdateAgentHostSettings(ctx, host, host.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if skillState(t, s, updated.ID) != "pending" {
		t.Fatal("a new host settings revision must show the skill pending")
	}
	if g, _ := s.GrantedSkillDigests(ctx, updated.ID, updated.Revision); len(g) != 0 {
		t.Fatalf("granted digests for the new revision = %v", g)
	}
	// A new definition revision, same skill: its own host row has no grant.
	def2, res2, _ := skillDef(t, "def:skill-rev", "2", reviewSkillFiles)
	pin2, err := s.InstallAgentDefinition(ctx, def2, res2)
	if err != nil {
		t.Fatal(err)
	}
	host2, err := s.CreateAgentHostSettings(ctx, AgentHostSettings{Slug: "rev-2", Title: "rev2", DefinitionRef: pin2,
		Settings: NativeHostSettings{Version: "1", Runtime: "api", Provider: "fixture-provider", Model: "fixture-model"}, Enabled: true, Source: "explicit-private-test-authoring"})
	if err != nil {
		t.Fatal(err)
	}
	if skillState(t, s, host2.ID) != "pending" {
		t.Fatal("a new definition revision must show the skill pending")
	}
}

func TestGrantedSkillTreesDeliverOnlyWhatIsGrantedAtThePinnedDigest(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	host, auth, digest := skillHost(t, s, "trees", "1", reviewSkillFiles)
	art, err := s.GetAgentDefinitionArtifact(ctx, host.DefinitionRef)
	if err != nil {
		t.Fatal(err)
	}
	d, err := agentdef.Parse(art.Data, agentpolicy.Option())
	if err != nil {
		t.Fatal(err)
	}
	if trees, err := s.GrantedSkillTrees(ctx, d, host); err != nil || len(trees) != 0 {
		t.Fatalf("ungranted = %v, %v", trees, err)
	}
	if _, err = s.LocalAuthority().GrantSkill(ctx, "c", auth.ID, "review", digest, "r"); err != nil {
		t.Fatal(err)
	}
	trees, err := s.GrantedSkillTrees(ctx, d, host)
	if err != nil || string(trees["review"]["SKILL.md"]) != "# review\n" || string(trees["review"]["scripts/run.sh"]) != "echo ok\n" {
		t.Fatalf("granted trees = %v, %v", trees, err)
	}
	if err = s.LocalAuthority().RevokeAuthority(ctx, "c", auth.ID, "r"); err != nil {
		t.Fatal(err)
	}
	if trees, err = s.GrantedSkillTrees(ctx, d, host); err != nil || len(trees) != 0 {
		t.Fatalf("revoked authority still delivers: %v, %v", trees, err)
	}
}

// grant_same_as carries a skill forward only when its digest is unchanged.
func TestLoadAgentCarriesSkillGrantsForwardOnlyForUnchangedDigests(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	src, auth, digest := skillHost(t, s, "carry", "1", reviewSkillFiles)
	if _, err := s.LocalAuthority().GrantSkill(ctx, "c", auth.ID, "review", digest, "r"); err != nil {
		t.Fatal(err)
	}
	load := func(revision string, files map[string][]byte) AgentLoadResult {
		t.Helper()
		def, res, _ := skillDef(t, "def:skill-carry", revision, files)
		out, err := s.LoadAgent(ctx, AgentLoad{Definition: def, Resources: res, Agent: "carry", Title: "carry",
			Settings:  NativeHostSettings{Version: "1", Runtime: "api", Provider: "fixture-provider", Model: "fixture-model"},
			GrantFrom: src.ID, GrantedBy: "cred-admin", Reason: "carry"})
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	same := load("2", reviewSkillFiles)
	if len(same.SkillsAdded) != 1 || same.SkillsAdded[0] != "review" || skillState(t, s, same.HostSettings.ID) != "granted" {
		t.Fatalf("unchanged digest: added %v", same.SkillsAdded)
	}
	changed := load("3", map[string][]byte{"SKILL.md": []byte("# review v2\n")})
	if len(changed.SkillsAdded) != 0 || skillState(t, s, changed.HostSettings.ID) != "pending" {
		t.Fatalf("changed digest must stay pending: added %v", changed.SkillsAdded)
	}
}

// Migration 183 rebuilds the audit table: old rows survive, the new actions
// are accepted, and the append-only triggers are intact.
func TestMigration183KeepsAuditRowsAndAcceptsSkillActions(t *testing.T) {
	s := newTestStoreAtVersion(t, 182)
	ctx := context.Background()
	if _, err := s.DB.ExecContext(ctx, `INSERT INTO native_authority_audit(authority_id,action,tool_id,granted_by,reason) VALUES('a1','tool_granted','t1','cred','old reason')`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.ExecContext(ctx, `INSERT INTO native_authority_audit(authority_id,action,granted_by) VALUES('a1','skill_granted','cred')`); err == nil {
		t.Fatal("v182 must not accept skill actions")
	}
	migrationsDir, err := fs.Sub(migrationsFS, "migrations")
	if err != nil {
		t.Fatal(err)
	}
	provider, err := goose.NewProvider(goose.DialectSQLite3, s.DB, migrationsDir, goose.WithVerbose(false))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = provider.UpTo(ctx, 183); err != nil {
		t.Fatal(err)
	}
	var tool, reason string
	if err = s.DB.QueryRowContext(ctx, `SELECT tool_id,reason FROM native_authority_audit WHERE authority_id='a1' AND action='tool_granted'`).Scan(&tool, &reason); err != nil || tool != "t1" || reason != "old reason" {
		t.Fatalf("old audit row = %q %q, %v", tool, reason, err)
	}
	for _, action := range []string{"skill_granted", "skill_revoked"} {
		if _, err = s.DB.ExecContext(ctx, `INSERT INTO native_authority_audit(authority_id,action,granted_by,skill_name,skill_digest) VALUES('a1',?,'cred','review','sha256:x')`, action); err != nil {
			t.Fatalf("%s: %v", action, err)
		}
	}
	if _, err = s.DB.ExecContext(ctx, `INSERT INTO native_authority_audit(authority_id,action,granted_by) VALUES('a1','bogus','cred')`); err == nil {
		t.Fatal("the action CHECK must still reject unknown actions")
	}
	if _, err = s.DB.ExecContext(ctx, `INSERT INTO native_authority_audit(authority_id,action,granted_by) VALUES('a1','skill_granted','  ')`); err == nil {
		t.Fatal("the granted_by rule must survive")
	}
	if _, err = s.DB.ExecContext(ctx, `UPDATE native_authority_audit SET reason='x'`); err == nil {
		t.Fatal("audit must stay append-only (update)")
	}
	if _, err = s.DB.ExecContext(ctx, `DELETE FROM native_authority_audit`); err == nil {
		t.Fatal("audit must stay append-only (delete)")
	}
	// Down restores the old shape without losing the old rows.
	if _, err = provider.DownTo(ctx, 182); err != nil {
		t.Fatal(err)
	}
	if err = s.DB.QueryRowContext(ctx, `SELECT tool_id FROM native_authority_audit WHERE action='tool_granted'`).Scan(&tool); err != nil || tool != "t1" {
		t.Fatalf("after down: %q, %v", tool, err)
	}
}
