package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"testing"

	mesh "github.com/hollis-labs/substrate/mesh"
	"github.com/hollis-labs/substrate/mesh/agentdef"
)

func definitionTestBytes(id, revision, body string) []byte {
	return []byte(fmt.Sprintf("---\nschema_version: \"2\"\ndefinition_id: %s\nrevision: \"%s\"\nname: test-definition\ndescription: Storage fixture.\nbehavior:\n  purpose: Complete the requested work.\nrequirements: {}\nharness_profile:\n  context: {}\n  permissions:\n    profile: default\ncontinuity:\n  mode: ephemeral\n---\n%s\n", id, revision, body))
}

func TestDefinitionInstallImmutableAndIndependent(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	data := definitionTestBytes("def:storage-test", "1", "Original content.")
	pin, err := s.InstallAgentDefinition(ctx, data, nil)
	if err != nil {
		t.Fatal(err)
	}
	again, err := s.InstallAgentDefinition(ctx, data, nil)
	if err != nil || again != pin {
		t.Fatalf("idempotent install %v %v", again, err)
	}
	if _, err = s.InstallAgentDefinition(ctx, definitionTestBytes(pin.ID, pin.Revision, "Changed content."), nil); !errors.Is(err, ErrDefinitionRevisionConflict) {
		t.Fatalf("revision rebound: %v", err)
	}
	got, err := s.GetAgentDefinitionArtifact(ctx, pin)
	if err != nil || string(got.Data) != string(data) {
		t.Fatalf("stored artifact: %v", err)
	}
	wrong := pin
	wrong.Digest = agentdef.ArtifactDigest([]byte("wrong"))
	if _, err = s.GetAgentDefinitionArtifact(ctx, wrong); !errors.Is(err, ErrDefinitionContent) {
		t.Fatalf("incorrect pin accepted: %v", err)
	}
	if _, err = s.DB.ExecContext(ctx, `DELETE FROM agent_definitions WHERE definition_id=?`, pin.ID); err == nil {
		t.Fatal("immutable label deleted")
	}
	for _, table := range []string{"agent_actor_bindings", "actor_granted_tools", "actor_known_skills", "agent_host_settings"} {
		var n int
		if err = s.DB.QueryRowContext(ctx, `SELECT count(*) FROM `+table).Scan(&n); err != nil || n != 0 {
			t.Fatalf("install wrote %s: %d %v", table, n, err)
		}
	}
}

func TestDefinitionResourcesAdmissionAndRollback(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	body := []byte("Verified instruction body.")
	data := definitionTestBytes("def:resource-test", "1", "Body.")
	data = []byte(strings.Replace(string(data), "  purpose: Complete the requested work.", fmt.Sprintf("  purpose: Complete the requested work.\n  instructions:\n    - uri: resource:instruction\n      digest: %s", agentdef.ArtifactDigest(body)), 1))
	if _, err := s.InstallAgentDefinition(ctx, data, nil); !errors.Is(err, ErrDefinitionContent) {
		t.Fatalf("missing resource: %v", err)
	}
	if _, err := s.InstallAgentDefinition(ctx, data, []DefinitionResource{{URI: "resource:instruction", Content: []byte("changed")}}); !errors.Is(err, ErrDefinitionContent) {
		t.Fatalf("incorrect body: %v", err)
	}
	if _, err := s.DB.ExecContext(ctx, `CREATE TRIGGER fail_resource BEFORE INSERT ON agent_definition_resources BEGIN SELECT RAISE(ABORT,'fixture failure'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.InstallAgentDefinition(ctx, data, []DefinitionResource{{URI: "resource:instruction", Content: body}}); err == nil {
		t.Fatal("storage failure accepted")
	}
	var n int
	if err := s.DB.QueryRowContext(ctx, `SELECT count(*) FROM agent_definitions WHERE definition_id='def:resource-test'`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("partial definition persisted: %d %v", n, err)
	}
	if _, err := s.DB.ExecContext(ctx, `DROP TRIGGER fail_resource`); err != nil {
		t.Fatal(err)
	}
	pin, err := s.InstallAgentDefinition(ctx, data, []DefinitionResource{{URI: "resource:instruction", Content: body}})
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.GetAgentDefinitionResource(ctx, pin, agentdef.Ref{URI: "resource:instruction", Digest: agentdef.ArtifactDigest(body)})
	if err != nil || string(got) != string(body) {
		t.Fatalf("resource result %q %v", got, err)
	}
}

func TestHostSettingsNoActorAndRetiredSlugRefusal(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	pin, err := s.InstallAgentDefinition(ctx, definitionTestBytes("def:host-test", "1", "Body."), nil)
	if err != nil {
		t.Fatal(err)
	}
	input := AgentHostSettings{Slug: "native-test", Title: "Native test", DefinitionRef: pin, Settings: NativeHostSettings{Version: "1", Runtime: "api"}, Enabled: true, Source: "explicit-authoring"}
	h, err := s.CreateAgentHostSettings(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.GetAgentHostSettings(ctx, h.ID)
	if err != nil || got.Revision != h.Revision || got.DefinitionRef != pin {
		t.Fatalf("host settings: %+v %v", got, err)
	}
	var n int
	if err = s.DB.QueryRowContext(ctx, `SELECT count(*) FROM agent_actor_bindings`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("host UUID became actor: %d %v", n, err)
	}
	input.DefinitionRef = mesh.DefinitionRef{ID: pin.ID, Revision: pin.Revision, Digest: agentdef.ArtifactDigest([]byte("wrong"))}
	input.Slug = "wrong-pin"
	if _, err = s.CreateAgentHostSettings(ctx, input); err == nil {
		t.Fatal("host settings accepted mismatched pin")
	}
	if _, err = s.DB.ExecContext(ctx, `INSERT INTO retired_agent_profiles(id,slug,source,plugin_id,export_id,digest,actor,reason) VALUES('historical','retired-slug','internal','','export','digest','operator','explicit retirement')`); err != nil {
		t.Fatal(err)
	}
	input.DefinitionRef = pin
	input.Slug = "retired-slug"
	if _, err = s.CreateAgentHostSettings(ctx, input); !errors.Is(err, ErrProfileIngestionRetired) {
		t.Fatalf("retired slug admitted: %v", err)
	}
}

func TestFreshReadersDoNotResolveHistoricalProfiles(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	if _, err := s.DB.ExecContext(ctx, `INSERT INTO agent_profiles(id,name,slug,system_prompt,source) VALUES('historical-only','Historical only','historical-only','Old intrinsic body','user')`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetAgent(ctx, "historical-only"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("ordinary read fell back: %v", err)
	}
	old, err := s.GetHistoricalAgentProfile(ctx, "historical-only")
	if err != nil || old.SystemPrompt != "Old intrinsic body" {
		t.Fatalf("historical audit read: %+v %v", old, err)
	}
	if err = s.UpdateAgent(ctx, old); !errors.Is(err, ErrImmutableAgentProfile) {
		t.Fatalf("legacy mutable writer accepted: %v", err)
	}
	if err = s.EnsureSessionAgent(ctx, "session", "historical-only", "default", true); err == nil {
		t.Fatal("legacy ID created actor binding")
	}
	pin, err := s.InstallAgentDefinition(ctx, definitionTestBytes("def:projection", "1", "Fresh intrinsic body."), nil)
	if err != nil {
		t.Fatal(err)
	}
	h, err := s.CreateAgentHostSettings(ctx, AgentHostSettings{Slug: "fresh-host", Title: "Fresh host", DefinitionRef: pin, Settings: NativeHostSettings{Version: "1", Runtime: "api"}, Source: "operator", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	p, err := s.GetAgent(ctx, h.ID)
	if err != nil || !strings.Contains(p.SystemPrompt, "Fresh intrinsic body.") || p.DefinitionPolicy == nil {
		t.Fatalf("fresh projection: %+v %v", p, err)
	}
	if _, err = s.GetAgentForActor(ctx, h.ID); !errors.Is(err, ErrVerifiedActorRequired) {
		t.Fatalf("host UUID treated as actor: %v", err)
	}
	h.Title = "New title"
	h, err = s.UpdateAgentHostSettings(ctx, h, h.Revision)
	if err != nil {
		t.Fatal(err)
	}
	original, err := s.GetAgentDefinitionArtifact(ctx, pin)
	if err != nil || !strings.Contains(string(original.Data), "Fresh intrinsic body.") {
		t.Fatalf("host edit mutated intrinsic content: %v", err)
	}
	oldAgain, err := s.GetHistoricalAgentProfile(ctx, "historical-only")
	if err != nil || oldAgain.Revision != old.Revision || oldAgain.SystemPrompt != old.SystemPrompt {
		t.Fatalf("historical row changed: %+v %v", oldAgain, err)
	}
}
