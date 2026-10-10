package store

import "testing"

func TestDisabledActorSkillRevocationCannotReplayAuthority(t *testing.T) {
	s := newTestStore(t)
	ctx := t.Context()
	h := partitionHost(t, s, "private-revoke")
	const actor = "msg://agent/private-fixture/revoke"
	partitionExec(t, s, `INSERT INTO agent_actor_bindings(actor_uri,host_settings_id,binding_receipt) VALUES(?,?,'private-prior-authorized-binding')`, actor, h.ID)
	partitionExec(t, s, `INSERT INTO actor_known_skills(agent_id,skill_name,pinned,activation_count,reason,approved_content_hash,granted_at,granted_by,capabilities_granted) VALUES(?,'prior-skill',1,7,'retain metadata','old-hash','yesterday','prior-owner','["read"]')`, actor)
	partitionExec(t, s, `UPDATE agent_actor_bindings SET enabled=0 WHERE actor_uri=?`, actor)
	if _, err := s.DB.ExecContext(ctx, `UPDATE actor_known_skills SET reason='changed',approved_content_hash=NULL,granted_at=NULL,granted_by=NULL,capabilities_granted=NULL WHERE agent_id=?`, actor); err == nil {
		t.Fatal("disabled metadata mutation hid behind revocation")
	}
	revoked, err := s.RevokeAgentSkillGrant(ctx, actor, "prior-skill")
	if err != nil || !revoked {
		t.Fatalf("disabled revocation refused: %t %v", revoked, err)
	}
	var pinned bool
	var activations int
	var reason, approval string
	if err := s.DB.QueryRowContext(ctx, `SELECT pinned,activation_count,reason,COALESCE(approved_content_hash,'')||COALESCE(granted_at,'')||COALESCE(granted_by,'')||COALESCE(capabilities_granted,'') FROM actor_known_skills WHERE agent_id=?`, actor).Scan(&pinned, &activations, &reason, &approval); err != nil {
		t.Fatal(err)
	}
	if !pinned || activations != 7 || reason != "retain metadata" || approval != "" {
		t.Fatalf("revocation changed metadata or left authority: %t %d %q %q", pinned, activations, reason, approval)
	}
	if _, err := s.DB.ExecContext(ctx, `UPDATE actor_known_skills SET approved_content_hash='old-hash' WHERE agent_id=?`, actor); err == nil {
		t.Fatal("disabled binding re-granted skill")
	}
	partitionExec(t, s, `UPDATE agent_actor_bindings SET enabled=1 WHERE actor_uri=?`, actor)
	row, err := s.GetAgentKnownSkill(ctx, actor, "prior-skill")
	if err != nil || row.ApprovedContentHash != "" || row.GrantedBy != "" || row.CapabilitiesGranted != "" {
		t.Fatalf("reenabling restored authority: %+v %v", row, err)
	}
	revoked, err = s.RevokeAgentSkillGrant(ctx, actor, "prior-skill")
	if err != nil || revoked {
		t.Fatalf("repeat revocation=%t %v", revoked, err)
	}
}
